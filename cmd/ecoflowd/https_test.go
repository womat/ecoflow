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
		listen: "127.0.0.1:0", tlsCertificate: cert, httpToken: testToken}
	b, _ := newTestBlocker(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := startHTTPS(ctx, cfg, b, io.Discard)
	if err != nil {
		t.Fatalf("startHTTPS: %v", err)
	}

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
			name: "listen without block",
			args: []string{"--listen", "127.0.0.1"},
			want: "--listen only work together with --block",
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
	if cfg.listen != "172.17.0.1:8089" || cfg.blockTTL != 5*time.Minute || cfg.blockTask != 0 {
		t.Errorf("got listen %s, ttl %s, task %d; want 172.17.0.1:8089, 5m, 0",
			cfg.listen, cfg.blockTTL, cfg.blockTask)
	}
}
