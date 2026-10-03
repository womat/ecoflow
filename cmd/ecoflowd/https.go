package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// The endpoint through which the block is asked for.
//
// HTTPS and not an MQTT command topic: in the home automation this serves,
// MQTT coming in is telemetry and never an instruction, and a command topic on
// the broker would end that. HTTPS rather than plain HTTP and a token on top
// were the maintainer's decision (3 October 2026). The endpoint is meant to
// listen on the Docker bridge of the machine it runs on, so that the home
// automation's container reaches it and the LAN does not; the TLS mainly keeps
// the token off the wire should that ever change.

// blockHandler serves /block.
func blockHandler(b *blocker, token string) http.Handler {
	want := []byte("Bearer " + token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/block" {
			http.NotFound(w, r)
			return
		}
		// Compared in constant time, so the answer's timing says nothing
		// about how much of a guess was right.
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong token"})
			return
		}

		var (
			s   blockState
			err error
		)
		switch r.Method {
		case http.MethodGet:
			s = b.state()
		case http.MethodPut:
			s, err = b.request(true)
		case http.MethodDelete:
			s, err = b.request(false)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET, PUT or DELETE"})
			return
		}

		if err != nil {
			writeJSON(w, statusFor(err), struct {
				Error string `json:"error"`
				blockState
			}{err.Error(), s})
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// startHTTPS listens and serves until the context ends, and returns the
// address it is bound to. It fails at once if the address cannot be bound, so
// that a wrong --listen stops the start instead of leaving a service that
// cannot be reached.
func startHTTPS(ctx context.Context, cfg *config, b *blocker, stderr io.Writer) (string, error) {
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", cfg.listen, err)
	}

	srv := &http.Server{
		Handler: blockHandler(b, cfg.httpToken),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{cfg.tlsCertificate},
		},
		// A switch waits for the device: up to ten seconds for the
		// acknowledgement and five for the list that shows it.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
		ErrorLog:          log.New(stderr, "https: ", 0),
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(stderr, "error: https:", err)
		}
	}()

	fmt.Fprintf(stderr, "block endpoint on https://%s/block\n", ln.Addr())
	return ln.Addr().String(), nil
}
