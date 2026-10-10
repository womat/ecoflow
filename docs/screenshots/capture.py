"""Capture the README screenshots of the ecoflowd web page.

Serves the real cmd/ecoflowd/ui/index.html with a mocked /status (noon: the battery charging from
PV, a little export, the fast stream on, one home automation renewing the discharge block; reports
and requests advance on every request, so the LEDs flash) and photographs it with headless
Chromium. Run from the project root, on demand only:

    docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/playwright/python:v1.52.0-noble \
        sh -c 'pip install -q --break-system-packages playwright==1.52.0 && python3 docs/screenshots/capture.py'

Writes docs/screenshots/web-ui*.png. The social preview stays docs/social-preview.html.
"""

import json
import threading
import time
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[2]
PAGE = (ROOT / "cmd/ecoflowd/ui/index.html").read_bytes()
OUT = ROOT / "docs/screenshots"
PORT = 8769
UPTIME = 6 * 3600 + 20 * 60
START = time.time()
SN = "HC31XXXXXXXXXXXX"


def iso(t):
    return datetime.fromtimestamp(t, timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def status():
    now = time.time()
    run = now - START
    return {
        "sn": SN, "version": "v0.8.0", "host": "pi-home", "uptimeSeconds": int(UPTIME + run),
        "cloud": {
            "connected": True, "server": "mqtt-e.ecoflow.com:8883", "since": iso(START - UPTIME + 40),
            "fast": True, "switchEverySeconds": 3, "reportsPerMinute": 21,
            # A new report on every poll, so the device LED flashes.
            "lastReport": iso(now - 0.5),
        },
        "mqtt": {"configured": True, "connected": True, "broker": "192.168.1.5:1883", "topic": "ecoflow",
                 "telegramsPerMinute": 20},
        # The device's own signs: positive battery is charging, positive grid is export, the
        # house is negative while it consumes.
        "state": {"sn": SN, "timestamp": iso(now - 1), "pv": 3240, "house": -1182, "battery": 1561,
                  "grid": 497, "soc": 72},
        "energy": {"sn": SN, "timestamp": iso(now - 1200), "pv": 18400, "house": 9700, "batteryIn": 6200,
                   "batteryOut": 2100, "gridIn": 800, "gridOut": 5400},
        "block": {"sn": SN, "timestamp": iso(now - 40), "task": 1, "requested": False, "enabled": False,
                  "running": False, "window": "22:00-06:00"},
        "listen": ["172.17.0.1:8089", "192.168.1.10:8089"],
        "callers": [{"address": "192.168.1.20", "perHour": 12, "errors": 0, "last": "PUT /block",
                     "lastSeen": iso(now - 180)}],
    }


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/":
            body, ctype = PAGE, "text/html; charset=utf-8"
        elif self.path == "/status":
            body, ctype = json.dumps(status()).encode(), "application/json"
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def shoot(browser, path, width, height, scheme, scale=1, full_page=True):
    ctx = browser.new_context(viewport={"width": width, "height": height},
                              color_scheme=scheme, device_scale_factor=scale)
    ctx.add_init_script("localStorage.setItem('ecoflowd.token', 'demo')")
    page = ctx.new_page()
    page.goto(f"http://localhost:{PORT}/")
    # The second poll brings a new report and its flash; catch a moment with a lit LED.
    page.wait_for_function("document.querySelector('.link .dot.on') !== null", polling="raf", timeout=10000)
    page.screenshot(path=str(path), full_page=full_page)
    ctx.close()
    print("wrote", path.relative_to(ROOT))


def main():
    server = ThreadingHTTPServer(("localhost", PORT), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    OUT.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch()
        shoot(browser, OUT / "web-ui.png", 1280, 900, "light")
        shoot(browser, OUT / "web-ui-dark.png", 1280, 900, "dark")
        # Phone: the first screen only.
        shoot(browser, OUT / "web-ui-phone.png", 390, 760, "light", scale=2, full_page=False)
        browser.close()
    server.shutdown()


if __name__ == "__main__":
    main()
