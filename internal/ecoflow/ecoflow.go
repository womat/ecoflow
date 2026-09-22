// Package ecoflow talks to the consumer app's side of the EcoFlow cloud.
//
// This is not the documented Developer API. That one refuses the PowerOcean
// family with error 1006 and its MQTT topics stay silent; see api-status.md.
// What works is the channel the phone app uses: a session token from the
// consumer portal, credentials fetched with it, and an MQTT broker that
// actually publishes. None of it is documented or promised by EcoFlow, so
// every step here can stop working without notice, and failures have to be
// loud rather than quiet.
//
// The package covers getting in - logging in, fetching credentials, naming
// topics. Decoding what arrives is internal/frames; neither knows about the
// other.
package ecoflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultHost is the EU endpoint. US accounts use https://api-a.ecoflow.com.
const DefaultHost = "https://api-e.ecoflow.com"

// DefaultProductType is the product family the portal endpoints want as a
// header. Without a matching value they answer with no data at all, which
// looks like an empty account rather than a missing header.
const DefaultProductType = "85"

// ErrCredentials reports an e-mail and password the cloud rejected.
//
// It is kept apart from every other failure on purpose: a network that is
// down heals by itself, a wrong password never does. A service that treats
// them alike retries a typo against an undocumented endpoint forever.
var ErrCredentials = errors.New("the account credentials were rejected")

// Client reaches the consumer cloud.
type Client struct {
	Host        string       // defaults to DefaultHost
	ProductType string       // defaults to DefaultProductType
	HTTP        *http.Client // defaults to a client with a 30 second timeout
}

func (c *Client) host() string {
	if c.Host != "" {
		return strings.TrimRight(c.Host, "/")
	}
	return DefaultHost
}

func (c *Client) productType() string {
	if c.ProductType != "" {
		return c.ProductType
	}
	return DefaultProductType
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Session is what a successful login yields.
type Session struct {
	Token  string
	UserID string
}

// envelope is the shape every endpoint here answers with. The code arrives as
// a string on some endpoints and a number on others, hence json.RawMessage.
type envelope struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
}

// code returns the response code without its quotes, if it had any.
func (e envelope) code() string {
	s := strings.TrimSpace(string(e.Code))
	return strings.Trim(s, `"`)
}

// Login exchanges account credentials for a session token.
//
// The endpoint takes the password base64-encoded, which is encoding and not
// hashing: it is protected by TLS alone, and it is the account password rather
// than an application token. Anything that stores it should say so.
func (c *Client) Login(ctx context.Context, email, password string) (Session, error) {
	if email == "" || password == "" {
		return Session{}, fmt.Errorf("%w: e-mail or password missing", ErrCredentials)
	}

	body, err := json.Marshal(map[string]string{
		"email":    email,
		"password": base64.StdEncoding.EncodeToString([]byte(password)),
		"scene":    "IOT_APP",
		"userType": "ECOFLOW",
	})
	if err != nil {
		return Session{}, fmt.Errorf("build login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.host()+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return Session{}, fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")

	raw, err := c.do(req)
	if err != nil {
		return Session{}, err
	}

	var answer struct {
		envelope
		Data struct {
			Token string `json:"token"`
			User  struct {
				UserID json.Number `json:"userId"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Session{}, fmt.Errorf("login answered with something other than JSON: %w", err)
	}
	if answer.code() != "0" {
		return Session{}, fmt.Errorf("%w: %s (code %s)",
			ErrCredentials, message(answer.Message), answer.code())
	}
	if answer.Data.Token == "" {
		return Session{}, errors.New("login succeeded but carried no token")
	}

	return Session{
		Token:  answer.Data.Token,
		UserID: answer.Data.User.UserID.String(),
	}, nil
}

// Broker is where to reach the MQTT channel, and as whom.
type Broker struct {
	Host     string
	Port     string
	Account  string
	Password string
}

// Address is the broker as host:port.
func (b Broker) Address() string { return b.Host + ":" + b.Port }

// Certification fetches the MQTT credentials belonging to a session.
//
// The portal has a second endpoint for this that returns the same fields
// AES-encrypted and needs no user id. It is described in api-status.md and
// deliberately not used: this one answers in plain JSON, and a crypto path in
// a service is code that can quietly produce the wrong bytes.
func (c *Client) Certification(ctx context.Context, s Session) (Broker, error) {
	if s.Token == "" || s.UserID == "" {
		return Broker{}, errors.New("certification needs a token and a user id")
	}

	url := c.host() + "/iot-auth/app/certification?userId=" + s.UserID

	raw, err := c.authorized(ctx, url, s.Token)
	if err != nil {
		return Broker{}, err
	}

	var answer struct {
		envelope
		Data struct {
			Account  string      `json:"certificateAccount"`
			Password string      `json:"certificatePassword"`
			URL      string      `json:"url"`
			Port     json.Number `json:"port"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Broker{}, fmt.Errorf("certification answered with something other than JSON: %w", err)
	}
	if answer.code() != "0" {
		return Broker{}, fmt.Errorf("certification refused: %s (code %s)",
			message(answer.Message), answer.code())
	}
	if answer.Data.Account == "" || answer.Data.URL == "" {
		return Broker{}, errors.New("certification succeeded but carried no credentials")
	}

	return Broker{
		Host:     answer.Data.URL,
		Port:     answer.Data.Port.String(),
		Account:  answer.Data.Account,
		Password: answer.Data.Password,
	}, nil
}

// authorized performs a GET with the session token.
//
// Whether the token is sent bare or with a "Bearer " prefix differs between
// deployments, so it goes as given and is retried prefixed on 401 or 403.
func (c *Client) authorized(ctx context.Context, url, token string) ([]byte, error) {
	raw, status, err := c.get(ctx, url, token)
	if err != nil {
		return nil, err
	}
	if (status == http.StatusUnauthorized || status == http.StatusForbidden) &&
		!strings.HasPrefix(token, "Bearer ") {
		raw, status, err = c.get(ctx, url, "Bearer "+token)
		if err != nil {
			return nil, err
		}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, fmt.Errorf("HTTP %d - the portal token is missing, wrong or expired", status)
	}
	return raw, nil
}

func (c *Client) get(ctx context.Context, url, auth string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("product-type", c.productType())
	req.Header.Set("lang", "en_US")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func message(s string) string {
	if s == "" {
		return "no message"
	}
	return s
}

// ClientID builds an identifier the broker accepts.
//
// This is not cosmetic: the broker refuses ids that do not carry the app's
// prefix and the account's user id, and it refuses one it has already seen
// after a disconnect. So every connection needs a fresh one.
func ClientID(userID string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("draw a client id: %w", err)
	}
	return "ANDROID_" + strings.ToUpper(hex.EncodeToString(b[:])) + "_" + userID, nil
}
