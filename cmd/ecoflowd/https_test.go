package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

// writeCert creates a self-signed certificate for 127.0.0.1, as the README
// tells the operator to with openssl, and returns the paths.
func writeCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ecoflowd"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func call(t *testing.T, h http.Handler, method, auth string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/block", nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTokenIsRequired(t *testing.T) {
	b, _ := newTestBlocker(t)
	h := blockHandler(b, testToken)

	tests := []struct {
		name string
		auth string
	}{
		{"none", ""},
		{"wrong", "Bearer " + strings.Repeat("x", len(testToken))},
		{"without the scheme", testToken},
		{"basic instead", "Basic " + testToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
				if w := call(t, h, method, tc.auth); w.Code != http.StatusUnauthorized {
					t.Errorf("%s: got %d, want 401", method, w.Code)
				}
			}
		})
	}
	if b.state().Requested {
		t.Error("an unauthorised request was recorded")
	}
}

func TestHandlerAnswers(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	h := blockHandler(b, testToken)
	auth := "Bearer " + testToken

	if w := call(t, h, http.MethodPost, auth); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: got %d, want 405", w.Code)
	}
	if w := call(t, h, http.MethodPut, auth); w.Code != http.StatusServiceUnavailable {
		t.Errorf("PUT before any list: got %d, want 503", w.Code)
	}

	connect(b, d)
	w := call(t, h, http.MethodPut, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: got %d, want 200: %s", w.Code, w.Body)
	}
	var s blockState
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("PUT answer is not JSON: %v", err)
	}
	if !s.Requested || !s.Enabled || s.Task != 7 || s.SN != "HC31XXXXXXXXXXXX" {
		t.Errorf("PUT: got %+v", s)
	}

	if w := call(t, h, http.MethodGet, auth); w.Code != http.StatusOK {
		t.Errorf("GET: got %d, want 200", w.Code)
	}
	if w := call(t, h, http.MethodDelete, auth); w.Code != http.StatusOK {
		t.Errorf("DELETE: got %d, want 200", w.Code)
	}
}

// TestHTTPSOnly starts the real server: TLS 1.3 works, TLS 1.2 and plain HTTP
// do not.
func TestHTTPSOnly(t *testing.T) {
	certFile, keyFile := writeCert(t)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config{serial: "HC31XXXXXXXXXXXX", blockTTL: time.Minute,
		listen: []string{"127.0.0.1:0", "127.0.0.1:0"}, tlsCertificate: cert, httpToken: testToken}
	b, _ := newTestBlocker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrs, err := startHTTPS(ctx, cfg, newStatus(cfg), b, io.Discard)
	if err != nil {
		t.Fatalf("startHTTPS: %v", err)
	}
	if len(addrs) != 2 || addrs[0] == addrs[1] {
		t.Fatalf("got addresses %v, want two", addrs)
	}
	addr := addrs[1] // every address serves, not only the first

	pemBytes, _ := os.ReadFile(certFile)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pemBytes)
	client := func(maxVersion uint16) *http.Client {
		return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, MaxVersion: maxVersion}}}
	}

	req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/block", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := client(tls.VersionTLS13).Do(req)
	if err != nil {
		t.Fatalf("TLS 1.3: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("TLS 1.3: got %d, want 200", resp.StatusCode)
	}

	if resp, err := client(tls.VersionTLS12).Get("https://" + addr + "/block"); err == nil {
		resp.Body.Close()
		t.Error("TLS 1.2 was accepted")
	}
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/block"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Error("plain HTTP was served")
		}
	}
}

func TestBlockFlags(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "someone@example.com")
	t.Setenv("ECOFLOW_PASSWORD", "secret")
	t.Setenv("ECOFLOWD_HTTP_TOKEN", testToken)
	certFile, keyFile := writeCert(t)
	tlsArgs := []string{"--tls-cert", certFile, "--tls-key", keyFile}

	tests := []struct {
		name  string
		args  []string
		token *string
		want  string
	}{
		{
			name: "certificate without listen",
			args: tlsArgs,
			want: "--tls-cert, --tls-key only work together with --listen",
		},
		{
			name: "listen without certificate",
			args: []string{"--listen", "127.0.0.1"},
			want: "HTTPS only",
		},
		{
			name: "ttl without block",
			args: []string{"--block-ttl", "2m", "--block-task", "7"},
			want: "--block-task, --block-ttl only work together with --block",
		},
		{
			name: "block without listen",
			args: append([]string{"--block"}, tlsArgs...),
			want: "--block needs --listen",
		},
		{
			name: "every interface",
			args: append([]string{"--block", "--listen", "0.0.0.0:8089"}, tlsArgs...),
			want: "every interface",
		},
		{
			name: "no address in the list",
			args: append([]string{"--listen", " , "}, tlsArgs...),
			want: "names no address",
		},
		{
			name: "every interface in a list",
			args: append([]string{"--listen", "172.17.0.1,0.0.0.0"}, tlsArgs...),
			want: "every interface",
		},
		{
			name: "port only",
			args: append([]string{"--block", "--listen", ":8089"}, tlsArgs...),
			want: "every interface",
		},
		{
			name: "no certificate",
			args: []string{"--block", "--listen", "127.0.0.1"},
			want: "HTTPS only",
		},
		{
			name: "unreadable certificate",
			args: []string{"--block", "--listen", "127.0.0.1", "--tls-cert", keyFile, "--tls-key", keyFile},
			want: "certificate:",
		},
		{
			name: "ttl too short",
			args: append([]string{"--block", "--listen", "127.0.0.1", "--block-ttl", "30s"}, tlsArgs...),
			want: "outside",
		},
		{
			name: "ttl too long",
			args: append([]string{"--block", "--listen", "127.0.0.1", "--block-ttl", "1h"}, tlsArgs...),
			want: "outside",
		},
		{
			name:  "token too short",
			args:  append([]string{"--block", "--listen", "127.0.0.1"}, tlsArgs...),
			token: new(string),
			want:  "ECOFLOWD_HTTP_TOKEN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.token != nil {
				t.Setenv("ECOFLOWD_HTTP_TOKEN", *tc.token)
			}
			code, _, stderr := exec(t, append([]string{"--sn", "HC31XXXXXXXXXXXX"}, tc.args...)...)
			if code != exitUsage {
				t.Errorf("got exit %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("got stderr %q, want it to mention %q", stderr, tc.want)
			}
		})
	}
}

func TestBlockConfigDefaults(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "someone@example.com")
	t.Setenv("ECOFLOW_PASSWORD", "secret")
	t.Setenv("ECOFLOWD_HTTP_TOKEN", testToken)
	certFile, keyFile := writeCert(t)

	opts, fs, err := parseArgs([]string{"ecoflowd", "--sn", "HC31XXXXXXXXXXXX", "--block",
		"--listen", "172.17.0.1", "--tls-cert", certFile, "--tls-key", keyFile}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := buildConfig(opts, fs)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if strings.Join(cfg.listen, " ") != "172.17.0.1:8089" || cfg.blockTTL != 5*time.Minute || cfg.blockTask != 0 {
		t.Errorf("got listen %s, ttl %s, task %d; want 172.17.0.1:8089, 5m, 0",
			cfg.listen, cfg.blockTTL, cfg.blockTask)
	}
}

// TestListenWithoutBlock: --listen alone starts the page, without /block.
func TestListenWithoutBlock(t *testing.T) {
	t.Setenv("ECOFLOW_EMAIL", "someone@example.com")
	t.Setenv("ECOFLOW_PASSWORD", "secret")
	t.Setenv("ECOFLOWD_HTTP_TOKEN", testToken)
	certFile, keyFile := writeCert(t)

	opts, fs, err := parseArgs([]string{"ecoflowd", "--sn", "HC31XXXXXXXXXXXX",
		"--listen", "172.17.0.1, 192.168.1.10:8443", "--tls-cert", certFile, "--tls-key", keyFile}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := buildConfig(opts, fs)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if got := strings.Join(cfg.listen, " "); got != "172.17.0.1:8089 192.168.1.10:8443" || cfg.block || cfg.httpToken != testToken {
		t.Errorf("got listen %s, block %v; want 172.17.0.1:8089 192.168.1.10:8443 without block", got, cfg.block)
	}
}

func get(t *testing.T, h http.Handler, path, auth string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestPageIsPublicAndOnlyAtRoot: the page carries no data, so it needs no
// token; its CSP is strict; and an unknown path is a 404, not the page.
func TestPageIsPublicAndOnlyAtRoot(t *testing.T) {
	cfg := &config{serial: "HC31XXXXXXXXXXXX", topic: "ecoflow"}
	h := newHandler(newStatus(cfg), nil, newCallers(), []string{"127.0.0.1:8089"}, testToken)

	w := get(t, h, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /: got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /: Content-Type %q", ct)
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("no nosniff")
	}
	for _, path := range []string{"/x", "/index.html", "/block"} {
		if w := get(t, h, path, "Bearer "+testToken); w.Code != http.StatusNotFound {
			t.Errorf("GET %s without --block: got %d, want 404", path, w.Code)
		}
	}
}

// TestPageHasNoExternalResources: the page must work on a machine without
// internet access, and the CSP would block anything external anyway.
func TestPageHasNoExternalResources(t *testing.T) {
	// The SVG namespace in the favicon is a name, not something fetched.
	page := strings.ReplaceAll(string(uiPage), "http://www.w3.org/2000/svg", "")
	for _, bad := range []string{"http://", "https://", "//fonts.", "<script src", "<link rel=\"stylesheet\""} {
		if strings.Contains(page, bad) {
			t.Errorf("the page contains %q", bad)
		}
	}
	// Without it, a class that sets display would show an element marked hidden.
	if !strings.Contains(page, "[hidden] { display: none !important; }") {
		t.Error("the page lacks the [hidden] rule")
	}
}

func TestStatusNeedsToken(t *testing.T) {
	cfg := &config{serial: "HC31XXXXXXXXXXXX", topic: "ecoflow"}
	h := newHandler(newStatus(cfg), nil, newCallers(), []string{"127.0.0.1:8089"}, testToken)

	if w := get(t, h, "/status", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("without token: got %d, want 401", w.Code)
	}
	if w := get(t, h, "/status", "Bearer wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", w.Code)
	}
	w := get(t, h, "/status", "Bearer "+testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("with token: got %d, want 200", w.Code)
	}
	var v statusView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if v.SN != "HC31XXXXXXXXXXXX" || v.Block != nil || v.Callers != nil || v.MQTT.Configured {
		t.Errorf("got %+v", v)
	}
}

// TestStatusWithBlock: with --block the status carries the block, and the
// callers of /block - but not the page's own polling.
func TestStatusWithBlock(t *testing.T) {
	b, d := newTestBlocker(t, chargeTask(7, false))
	connect(b, d)
	cfg := &config{serial: "HC31XXXXXXXXXXXX", topic: "ecoflow"}
	h := newHandler(newStatus(cfg), b, newCallers(), []string{"127.0.0.1:8089", "192.0.2.10:8089"}, testToken)
	auth := "Bearer " + testToken

	if w := call(t, h, http.MethodPut, auth); w.Code != http.StatusOK {
		t.Fatalf("PUT: got %d: %s", w.Code, w.Body)
	}
	call(t, h, http.MethodGet, "Bearer wrong")
	get(t, h, "/status", auth) // the page itself, not to be counted

	w := get(t, h, "/status", auth)
	var v statusView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if v.Block == nil || !v.Block.Requested || v.Block.Task != 7 || v.Block.RenewedBy != "192.0.2.1" {
		t.Errorf("block: got %+v", v.Block)
	}
	if len(v.Callers) != 1 {
		t.Fatalf("callers: got %+v, want one", v.Callers)
	}
	if c := v.Callers[0]; c.Address != "192.0.2.1" || c.PerHour != 2 || c.Errors != 1 || c.Last != "GET /block" {
		t.Errorf("caller: got %+v", c)
	}
	if strings.Join(v.Listen, " ") != "127.0.0.1:8089 192.0.2.10:8089" {
		t.Errorf("listen: got %v", v.Listen)
	}
}

func TestCallersArePrunedAndCapped(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := newCallers()
	c.now = func() time.Time { return now }

	for i := range maxCallers + 3 {
		c.add(net.IPv4(192, 0, 2, byte(i+1)).String(), "PUT /block", http.StatusOK)
		now = now.Add(time.Second)
	}
	if got := len(c.list()); got != maxCallers {
		t.Errorf("got %d callers, want the cap of %d", got, maxCallers)
	}
	if _, by := c.renewed(); by != "192.0.2.11" {
		t.Errorf("renewed by %q, want the last PUT", by)
	}

	now = now.Add(callerWindow + time.Minute)
	if got := c.list(); len(got) != 0 {
		t.Errorf("after an hour: got %+v, want none", got)
	}
}
