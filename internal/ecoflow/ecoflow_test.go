package ecoflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// startCloud runs a stand-in for the EcoFlow endpoints and records what it was
// asked. Testing against the real cloud would need an account, a network and
// somebody's actual device; the point here is the request this package builds
// and the answers it copes with.
func startCloud(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &Client{Host: srv.URL, HTTP: srv.Client()}
}

func TestLogin(t *testing.T) {
	var gotBody map[string]string

	c := startCloud(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/login" {
			t.Errorf("got %s %s, want POST /auth/login", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("request body was not JSON: %v", err)
		}
		io.WriteString(w, `{"code":"0","data":{"token":"tok","user":{"userId":1000000000000000001}}}`)
	})

	s, err := c.Login(context.Background(), "someone@example.com", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if s.Token != "tok" {
		t.Errorf("got token %q, want %q", s.Token, "tok")
	}
	// The user id arrives as a JSON number past what a float64 holds exactly
	// (2^53), so it has to survive as digits rather than go through float. The
	// value here is synthetic but of the same magnitude as a real one.
	if s.UserID != "1000000000000000001" {
		t.Errorf("got user id %q, want %q", s.UserID, "1000000000000000001")
	}

	if got := gotBody["password"]; got != base64.StdEncoding.EncodeToString([]byte("hunter2")) {
		t.Errorf("password was not base64-encoded: %q", got)
	}
	if gotBody["scene"] != "IOT_APP" || gotBody["userType"] != "ECOFLOW" {
		t.Errorf("unexpected login body: %+v", gotBody)
	}
}

func TestLoginRejected(t *testing.T) {
	// The code and message here are plausible rather than measured - no list of
	// them is published, and this account has never been refused. What the test
	// pins down is the handling of any non-zero code, not the number 7.
	tests := []struct {
		name string
		body string
	}{
		{"wrong password", `{"code":"7","message":"account or password error"}`},
		{"numeric code", `{"code":7,"message":"account or password error"}`},
		{"no token despite code 0", `{"code":"0","data":{}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := startCloud(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			})

			_, err := c.Login(context.Background(), "someone@example.com", "wrong")
			if err == nil {
				t.Fatal("got no error, want one")
			}
			// A rejected password must be distinguishable from a hiccup: the
			// service stops on one and waits on the other.
			wantPermanent := !strings.Contains(tc.name, "no token")
			if got := errors.Is(err, ErrCredentials); got != wantPermanent {
				t.Errorf("errors.Is(err, ErrCredentials) = %v, want %v (err: %v)",
					got, wantPermanent, err)
			}
		})
	}
}

// TestLoginRefusedServiceIsNotACredentialError is about what a caller does
// with the answer. ErrCredentials makes the service exit for good, and the
// systemd unit declines to restart it - so a cloud that is merely rate-limiting
// or down must not produce one, however well-formed its reply. These bodies
// carry a non-zero code on purpose: the status is what tells them apart.
func TestLoginRefusedServiceIsNotACredentialError(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"rate limited", http.StatusTooManyRequests},
		{"server error", http.StatusInternalServerError},
		{"bad gateway", http.StatusBadGateway},
		{"unavailable", http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := startCloud(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"code":"1000","message":"too many requests"}`)
			})

			_, err := c.Login(context.Background(), "someone@example.com", "hunter2")
			if err == nil {
				t.Fatal("got no error, want one")
			}
			if errors.Is(err, ErrCredentials) {
				t.Errorf("HTTP %d was taken for a rejected password: %v", tc.status, err)
			}
		})
	}
}

func TestLoginWithoutCredentials(t *testing.T) {
	c := &Client{Host: "http://127.0.0.1:1"}
	if _, err := c.Login(context.Background(), "", ""); !errors.Is(err, ErrCredentials) {
		t.Errorf("got %v, want it to be ErrCredentials", err)
	}
}

func TestCertification(t *testing.T) {
	var gotPath, gotAuth, gotProduct, gotLang string

	c := startCloud(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotProduct = r.Header.Get("product-type")
		gotLang = r.Header.Get("lang")
		io.WriteString(w, `{"code":"0","data":{"certificateAccount":"app-1",`+
			`"certificatePassword":"s3cr3t","url":"mqtt-e.ecoflow.com","port":"8883"}}`)
	})

	b, err := c.Certification(context.Background(), Session{Token: "tok", UserID: "42"})
	if err != nil {
		t.Fatalf("Certification: %v", err)
	}

	if want := "/iot-auth/app/certification?userId=42"; gotPath != want {
		t.Errorf("got path %q, want %q", gotPath, want)
	}
	if gotAuth != "tok" {
		t.Errorf("got Authorization %q, want the bare token", gotAuth)
	}
	// Without the product-type header the endpoint answers code 0 and no data,
	// which reads like an empty account rather than a missing header.
	if gotProduct != DefaultProductType {
		t.Errorf("got product-type %q, want %q", gotProduct, DefaultProductType)
	}
	if gotLang != "en_US" {
		t.Errorf("got lang %q, want en_US", gotLang)
	}

	want := Broker{Host: "mqtt-e.ecoflow.com", Port: "8883", Account: "app-1", Password: "s3cr3t"}
	if b != want {
		t.Errorf("got %+v, want %+v", b, want)
	}
	if b.Address() != "mqtt-e.ecoflow.com:8883" {
		t.Errorf("got address %q", b.Address())
	}
}

// TestCertificationRetriesWithBearer covers the deployments that want the
// token prefixed. Sending it bare first and retrying is what the shell script
// does, and it is why both kinds of account work.
func TestCertificationRetriesWithBearer(t *testing.T) {
	var seen []string

	c := startCloud(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seen = append(seen, auth)
		if !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"code":"0","data":{"certificateAccount":"app-1",`+
			`"certificatePassword":"p","url":"h","port":"8883"}}`)
	})

	if _, err := c.Certification(context.Background(), Session{Token: "tok", UserID: "42"}); err != nil {
		t.Fatalf("Certification: %v", err)
	}
	if len(seen) != 2 || seen[0] != "tok" || seen[1] != "Bearer tok" {
		t.Errorf("got attempts %q, want the bare token then the prefixed one", seen)
	}
}

func TestCertificationRefused(t *testing.T) {
	// rejected says whether the cloud turned the token down, as opposed to
	// merely not answering properly. Only the first is a reason to discard it
	// and log in again, so the difference is worth stating case by case.
	tests := []struct {
		name     string
		status   int
		body     string
		rejected bool
	}{
		{"expired token", http.StatusUnauthorized, ``, true},
		{"non-zero code", http.StatusOK, `{"code":"1006","message":"not allowed"}`, true},
		{"code 0 but no data", http.StatusOK, `{"code":"0"}`, false},
		{"not json at all", http.StatusOK, `<html>gateway</html>`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := startCloud(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})

			_, err := c.Certification(context.Background(), Session{Token: "tok", UserID: "42"})
			if err == nil {
				t.Fatal("got no error, want one")
			}
			if got := errors.Is(err, ErrTokenRejected); got != tc.rejected {
				t.Errorf("errors.Is(err, ErrTokenRejected) = %v, want %v (err: %v)",
					got, tc.rejected, err)
			}
		})
	}
}

func TestClientID(t *testing.T) {
	// The broker refuses ids that do not look like the app's own, and refuses
	// one it has seen before, so the shape matters and so does the freshness.
	shape := regexp.MustCompile(`^ANDROID_[0-9A-F]{32}_42$`)

	first, err := ClientID("42")
	if err != nil {
		t.Fatalf("ClientID: %v", err)
	}
	if !shape.MatchString(first) {
		t.Errorf("got %q, want ANDROID_<32 hex uppercase>_<user id>", first)
	}

	second, err := ClientID("42")
	if err != nil {
		t.Fatalf("ClientID: %v", err)
	}
	if first == second {
		t.Error("two client ids came out the same; every connection needs a fresh one")
	}
}

func TestTopicsFor(t *testing.T) {
	got := TopicsFor("42", "HC31XXXXXXXXXXXX")

	want := Topics{
		Push:  "/app/device/property/HC31XXXXXXXXXXXX",
		State: "/app/device/status/HC31XXXXXXXXXXXX",
		Reply: "/app/42/HC31XXXXXXXXXXXX/thing/property/get_reply",
		Get:   "/app/42/HC31XXXXXXXXXXXX/thing/property/get",
		Set:   "/app/42/HC31XXXXXXXXXXXX/thing/property/set",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// Subscribing must never include the set topic: nothing reads commands
	// back, and having it in the list would invite publishing to it.
	for _, topic := range got.Subscribe() {
		if topic == got.Set {
			t.Error("the set topic is in the subscribe list")
		}
	}
	if len(got.Subscribe()) != 3 {
		t.Errorf("got %d topics to subscribe, want 3", len(got.Subscribe()))
	}
}
