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
	"sort"
	"sync"
	"time"
)

// The HTTPS side: the web page, the status it reads, and with --block the
// endpoint through which the block is asked for.
//
// HTTPS and not an MQTT command topic: in the home automation this serves,
// MQTT coming in is telemetry and never an instruction, and a command topic on
// the broker would end that. HTTPS rather than plain HTTP and a token on top
// were the maintainer's decision (3 October 2026). The server listens on the
// concrete addresses given with --listen, never on every interface; the TLS
// keeps the token off the wire.
//
// The page itself is read-only. It shows the block but cannot switch it -
// a switch on the page would be a third way to write to the device, and that
// is a decision of its own (CLAUDE.md).

// authorized checks the token. Compared in constant time, so the answer's
// timing says nothing about how much of a guess was right.
func authorized(r *http.Request, want []byte) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) == 1
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong token"})
}

// blockHandler serves /block.
func blockHandler(b *blocker, token string) http.Handler {
	want := []byte("Bearer " + token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/block" {
			http.NotFound(w, r)
			return
		}
		if !authorized(r, want) {
			unauthorized(w)
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

// statusHandler serves /status, what the page polls.
func statusHandler(st *status, b *blocker, calls *callers, listen []string, token string) http.Handler {
	want := []byte("Bearer " + token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, want) {
			unauthorized(w)
			return
		}
		v := st.view()
		if b != nil {
			at, by := calls.renewed()
			v.Block = &blockView{blockState: b.state(), RenewedAt: utc(at), RenewedBy: by}
			v.Callers = calls.list()
			v.Listen = listen
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, v)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// newHandler puts the routes together. Without --block there is no /block at
// all: a 404 says more than an endpoint that answers but cannot switch.
func newHandler(st *status, b *blocker, calls *callers, listen []string, token string) http.Handler {
	mux := http.NewServeMux()
	// {$} matches "/" alone, so an unknown path is a 404 and not the page.
	mux.Handle("GET /{$}", uiHandler())
	mux.Handle("GET /status", statusHandler(st, b, calls, listen, token))
	if b != nil {
		mux.Handle("/block", calls.track(blockHandler(b, token)))
	}
	return mux
}

// startHTTPS listens and serves until the context ends, and returns the
// addresses it is bound to. It fails at once if any address cannot be bound,
// so that a wrong --listen stops the start instead of leaving a service that
// cannot be reached where it was meant to be. One server serves every
// address: same page, same token, same certificate. b is nil without --block.
func startHTTPS(ctx context.Context, cfg *config, st *status, b *blocker, stderr io.Writer) ([]string, error) {
	var (
		listeners []net.Listener
		addrs     []string
	)
	for _, a := range cfg.listen {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			for _, l := range listeners {
				l.Close()
			}
			return nil, fmt.Errorf("listen on %s: %w", a, err)
		}
		listeners = append(listeners, ln)
		addrs = append(addrs, ln.Addr().String())
	}

	srv := &http.Server{
		Handler: newHandler(st, b, newCallers(), addrs, cfg.httpToken),
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
	for _, ln := range listeners {
		go func() {
			if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
				fmt.Fprintln(stderr, "error: https:", err)
			}
		}()
		fmt.Fprintf(stderr, "web page on https://%s/\n", ln.Addr())
		if b != nil {
			fmt.Fprintf(stderr, "block endpoint on https://%s/block\n", ln.Addr())
		}
	}
	return addrs, nil
}

// callers remembers who asked for the block, for the page: one row per
// address with its requests of the last hour, the failed ones among them, and
// the last request. Only /block is counted - the page's own polling would put
// the viewer's browser on top of the list and say nothing.
//
// The address is the connection's: the endpoint is never behind a proxy, so
// there is no forwarded header to believe.
type callers struct {
	now func() time.Time

	mu        sync.Mutex
	byAddr    map[string]*callerLog
	renewedAt time.Time
	renewedBy string
}

type callerLog struct {
	events []callerEvent
	last   string // "PUT /block"
	lastAt time.Time
}

type callerEvent struct {
	at     time.Time
	failed bool
}

// caller is one row of the list.
type caller struct {
	Address  string `json:"address"`
	PerHour  int    `json:"perHour"`
	Errors   int    `json:"errors"`
	Last     string `json:"last"`
	LastSeen string `json:"lastSeen"`
}

// The list keeps an hour and at most this many addresses; more than a handful
// of callers is not what the endpoint is for, and the cap keeps a scan of the
// network from growing it without end.
const (
	callerWindow = time.Hour
	maxCallers   = 8
)

func newCallers() *callers {
	return &callers{now: time.Now, byAddr: map[string]*callerLog{}}
}

// track counts every request that reaches next, with its outcome.
func (c *callers) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		addr := r.RemoteAddr
		if host, _, err := net.SplitHostPort(addr); err == nil {
			addr = host
		}
		c.add(addr, r.Method+" "+r.URL.Path, rec.code)
	})
}

func (c *callers) add(addr, what string, code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.pruneLocked(now)

	l, ok := c.byAddr[addr]
	if !ok {
		if len(c.byAddr) >= maxCallers {
			c.dropOldestLocked()
		}
		l = &callerLog{}
		c.byAddr[addr] = l
	}
	l.events = append(l.events, callerEvent{at: now, failed: code >= 400})
	l.last, l.lastAt = what, now

	if what == http.MethodPut+" /block" && code == http.StatusOK {
		c.renewedAt, c.renewedBy = now, addr
	}
}

// pruneLocked drops events older than the window, and callers left without
// any. Callers hold mu.
func (c *callers) pruneLocked(now time.Time) {
	for addr, l := range c.byAddr {
		i := 0
		for i < len(l.events) && now.Sub(l.events[i].at) > callerWindow {
			i++
		}
		l.events = l.events[i:]
		if len(l.events) == 0 {
			delete(c.byAddr, addr)
		}
	}
}

func (c *callers) dropOldestLocked() {
	var oldest string
	var at time.Time
	for addr, l := range c.byAddr {
		if oldest == "" || l.lastAt.Before(at) {
			oldest, at = addr, l.lastAt
		}
	}
	delete(c.byAddr, oldest)
}

// list returns the callers, the busiest first.
func (c *callers) list() []caller {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(c.now())

	out := make([]caller, 0, len(c.byAddr))
	for addr, l := range c.byAddr {
		row := caller{Address: addr, PerHour: len(l.events), Last: l.last, LastSeen: utc(l.lastAt)}
		for _, e := range l.events {
			if e.failed {
				row.Errors++
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PerHour != out[j].PerHour {
			return out[i].PerHour > out[j].PerHour
		}
		return out[i].LastSeen > out[j].LastSeen
	})
	return out
}

// renewed says when the block was last switched on or renewed, and by whom.
func (c *callers) renewed() (time.Time, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renewedAt, c.renewedBy
}

// recorder keeps the status code a handler wrote.
type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}
