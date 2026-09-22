package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/womat/ecoflow/internal/ecoflow"
)

// cloud counts what the service asked of it.
type cloud struct {
	logins int
	certs  int
	fail   bool // make certification refuse, as an expired token would
	hangup bool // make certification drop the connection, as a bad line does
}

func (c *cloud) start(t *testing.T) *config {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/login"):
			c.logins++
			io.WriteString(w, `{"code":"0","data":{"token":"tok","user":{"userId":42}}}`)
		case strings.Contains(r.URL.Path, "certification"):
			c.certs++
			if c.hangup {
				panic(http.ErrAbortHandler) // closes the connection, no reply
			}
			if c.fail {
				io.WriteString(w, `{"code":"1006","message":"not allowed"}`)
				return
			}
			// Credentials for a broker that is not there: the connection then
			// fails, which is exactly the retry this test is about.
			io.WriteString(w, `{"code":"0","data":{"certificateAccount":"a",`+
				`"certificatePassword":"p","url":"127.0.0.1","port":"1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return &config{
		serial: "HC31XXXXXXXXXXXX",
		email:  "a@b.c", password: "x",
		host: srv.URL,
	}
}

// TestSessionIsReusedAcrossAttempts is the point of holding the session in
// memory. Before this, every failed connection logged in again - so a network
// that came and went meant a login each time, against an undocumented endpoint
// and with the account password on the wire.
func TestSessionIsReusedAcrossAttempts(t *testing.T) {
	c := &cloud{}
	cfg := c.start(t)

	var s ecoflow.Session
	for range 4 {
		// Each of these fails at the broker, which is the ordinary case this
		// is about: the cloud is fine, the connection is not.
		connected, err := session(context.Background(), cfg, &s, nil, io.Discard, io.Discard)
		if err == nil {
			t.Fatal("expected the connection to fail")
		}
		// An attempt that never reached a subscription must not count as one
		// that stood, or the backoff would reset on every failure and the
		// service would hammer a cloud that is plainly not answering.
		if connected {
			t.Error("a failed connection reported itself as having stood")
		}
	}

	if c.logins != 1 {
		t.Errorf("logged in %d times over four attempts, want exactly 1", c.logins)
	}
	if c.certs != 4 {
		t.Errorf("fetched credentials %d times, want 4", c.certs)
	}
	if s.Token == "" {
		t.Error("the session was not kept")
	}
}

// TestExpiredTokenIsForgotten covers the other half: a token that stopped
// working must not be retried for ever. Certification failing drops it, so the
// next attempt logs in again.
func TestExpiredTokenIsForgotten(t *testing.T) {
	c := &cloud{fail: true}
	cfg := c.start(t)

	var s ecoflow.Session
	for range 3 {
		if _, err := session(context.Background(), cfg, &s, nil, io.Discard, io.Discard); err == nil {
			t.Fatal("expected certification to fail")
		}
		if s.Token != "" {
			t.Fatal("a token that failed certification was kept")
		}
	}

	if c.logins != 3 {
		t.Errorf("logged in %d times, want one per attempt after the token was dropped", c.logins)
	}
}

// TestATokenSurvivesABadLine is the counterpart to the test above, and the
// distinction the whole thing turns on. Certification failing because nothing
// answered says nothing about the token; throwing it away there would make
// every hiccup on the line cost another login - and the login is the one
// request that carries the account password.
func TestATokenSurvivesABadLine(t *testing.T) {
	c := &cloud{hangup: true}
	cfg := c.start(t)

	var s ecoflow.Session
	for range 3 {
		if _, err := session(context.Background(), cfg, &s, nil, io.Discard, io.Discard); err == nil {
			t.Fatal("expected certification to fail")
		}
		if s.Token == "" {
			t.Fatal("the token was discarded although the cloud never turned it down")
		}
	}

	if c.logins != 1 {
		t.Errorf("logged in %d times over three unreachable attempts, want exactly 1", c.logins)
	}
}

// TestBadCredentialsSurfaceAsSuch keeps the one error the service must not
// retry distinguishable after passing through session().
func TestBadCredentialsSurfaceAsSuch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":"7","message":"account or password error"}`)
	}))
	defer srv.Close()

	cfg := &config{serial: "HC31XXXXXXXXXXXX", email: "a@b.c", password: "wrong", host: srv.URL}

	var s ecoflow.Session
	_, err := session(context.Background(), cfg, &s, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("got no error, want one")
	}
	if !isCredentialError(err) {
		t.Errorf("got %v, want it to be recognised as a credential error", err)
	}
}

func isCredentialError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "credentials were rejected")
}
