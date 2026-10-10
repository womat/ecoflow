package main

import (
	_ "embed"
	"net/http"
)

// The web page, embedded in the binary and served at GET /.
//
// It holds no data itself, so it is served without the token: it asks for the
// token once, keeps it in the browser and polls /status with it. Everything is
// in the one file - no external fonts or scripts, the machine it runs on may
// have no internet access - which is what lets the CSP below be this strict.
// The page follows the sibling projects' pages (s0meter, smartmeter,
// modbusgateway): same tokens, same parts, flow strip with the client left.

//go:embed ui/index.html
var uiPage []byte

const uiCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func uiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Security-Policy", uiCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(uiPage)
	})
}
