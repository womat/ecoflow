package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/womat/ecoflow/internal/ecoflow"
	"github.com/womat/ecoflow/internal/frames"
)

// serve connects, reads, and keeps doing so until the context ends.
//
// Every failure that waiting might fix leads back into the loop with a longer
// pause. The one that waiting never fixes - credentials the cloud rejected -
// ends the program with its own status, so the unit can decline to restart it.
func serve(ctx context.Context, cfg *config, stdout, stderr io.Writer) int {
	wait := backoffStart

	for {
		err := session(ctx, cfg, stdout, stderr)
		switch {
		case ctx.Err() != nil:
			fmt.Fprintln(stderr, "stopping")
			return exitOK
		case errors.Is(err, ecoflow.ErrCredentials):
			fmt.Fprintln(stderr, "error:", err)
			fmt.Fprintln(stderr, "this will not fix itself; check ECOFLOW_EMAIL and ECOFLOW_PASSWORD")
			return exitCredentials
		case err != nil:
			fmt.Fprintf(stderr, "error: %v; retrying in %s\n", err, wait)
		}

		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "stopping")
			return exitOK
		case <-time.After(wait):
		}

		// Back off towards a quarter hour. The cloud being briefly away is
		// ordinary; knocking every five seconds for hours is not.
		if wait *= 2; wait > backoffMax {
			wait = backoffMax
		}
	}
}

// session runs one connection from start to finish.
func session(ctx context.Context, cfg *config, stdout, stderr io.Writer) error {
	client := &ecoflow.Client{Host: cfg.host}

	s, err := sessionToken(ctx, cfg, client, stderr)
	if err != nil {
		return err
	}

	broker, err := client.Certification(ctx, s)
	if err != nil {
		// The cached token may simply have expired. Drop it and let the next
		// pass log in again rather than failing forever on a stale file.
		forgetToken(cfg, stderr)
		return fmt.Errorf("fetch broker credentials: %w", err)
	}

	topics := ecoflow.TopicsFor(s.UserID, cfg.serial)
	return listen(ctx, cfg, broker, s, topics, stdout, stderr)
}

// listen subscribes and reports what arrives, until the connection or the
// context ends.
func listen(ctx context.Context, cfg *config, broker ecoflow.Broker,
	s ecoflow.Session, topics ecoflow.Topics, stdout, stderr io.Writer) error {

	id, err := ecoflow.ClientID(s.UserID)
	if err != nil {
		return err
	}

	// Paho's own reconnect is deliberately off: it would reconnect with the
	// same client id, and this broker refuses an id it has already seen. Every
	// attempt needs a fresh one, so reconnecting is the outer loop's job.
	opts := mqtt.NewClientOptions().
		AddBroker("ssl://" + broker.Address()).
		SetClientID(id).
		SetUsername(broker.Account).
		SetPassword(broker.Password).
		SetCleanSession(true).
		SetAutoReconnect(false).
		SetConnectTimeout(30 * time.Second).
		SetKeepAlive(60 * time.Second).
		SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})

	lost := make(chan error, 1)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		select {
		case lost <- err:
		default:
		}
	})

	incoming := make(chan []byte, 256)
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		select {
		case incoming <- m.Payload():
		default: // never block the MQTT thread on a slow reader
		}
	})

	c := mqtt.NewClient(opts)
	if token := c.Connect(); !token.WaitTimeout(30*time.Second) || token.Error() != nil {
		return fmt.Errorf("connect to %s: %w", broker.Address(), tokenErr(token))
	}
	defer c.Disconnect(250)

	for _, topic := range topics.Subscribe() {
		if token := c.Subscribe(topic, 0, nil); !token.WaitTimeout(30*time.Second) || token.Error() != nil {
			return fmt.Errorf("subscribe to %s: %w", topic, tokenErr(token))
		}
	}
	fmt.Fprintf(stderr, "connected to %s, subscribed to %d topics\n",
		broker.Address(), len(topics.Subscribe()))

	var seen readings
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-lost:
			return fmt.Errorf("connection lost: %w", err)
		case payload := <-incoming:
			f, err := frames.Parse(payload)
			if err != nil {
				continue // not every frame on these topics is one we know
			}
			if cfg.verbose {
				fmt.Fprintf(stderr, "frame %v, %d bytes\n", f.Command, len(f.Payload))
			}
			if line, _, ok := seen.add(f); ok && cfg.stdout {
				fmt.Fprintln(stdout, line)
			}
		}
	}
}

func tokenErr(t mqtt.Token) error {
	if err := t.Error(); err != nil {
		return err
	}
	return errors.New("timed out")
}

// sessionToken returns a usable session, from the cache if there is one.
//
// The token was observed to last 30 days. Throwing it away because the service
// restarted would mean logging in again every time systemd bounces it, which
// is both wasteful and the kind of traffic that makes an account stand out.
func sessionToken(ctx context.Context, cfg *config, c *ecoflow.Client, stderr io.Writer) (ecoflow.Session, error) {
	if s, ok := loadToken(cfg); ok {
		return s, nil
	}

	s, err := c.Login(ctx, cfg.email, cfg.password)
	if err != nil {
		return ecoflow.Session{}, err
	}
	fmt.Fprintf(stderr, "logged in as user %s\n", s.UserID)
	saveToken(cfg, s, stderr)
	return s, nil
}

func tokenPath(cfg *config) string {
	if cfg.state == "" {
		return ""
	}
	return filepath.Join(cfg.state, "session.json")
}

func loadToken(cfg *config) (ecoflow.Session, bool) {
	path := tokenPath(cfg)
	if path == "" {
		return ecoflow.Session{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ecoflow.Session{}, false
	}
	var s ecoflow.Session
	if err := json.Unmarshal(b, &s); err != nil || s.Token == "" || s.UserID == "" {
		return ecoflow.Session{}, false
	}
	return s, true
}

func saveToken(cfg *config, s ecoflow.Session, stderr io.Writer) {
	path := tokenPath(cfg)
	if path == "" {
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	// 0600: the token is as good as the password for as long as it lasts.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		fmt.Fprintln(stderr, "warning: could not cache the session token:", err)
	}
}

func forgetToken(cfg *config, stderr io.Writer) {
	path := tokenPath(cfg)
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "warning: could not discard the cached token:", err)
	}
}
