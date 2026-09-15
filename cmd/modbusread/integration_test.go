package main

import (
	"bytes"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

// testHandler is a tiny Modbus server backing store. Addresses outside the
// populated range answer with "illegal data address", which is what a real
// device does for a register that does not exist.
type testHandler struct {
	mu   sync.Mutex
	base uint16
	regs []uint16
}

func (h *testHandler) set(addr uint16, value uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.regs[addr-h.base] = value
}

func (h *testHandler) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req.IsWrite {
		return nil, modbus.ErrIllegalFunction
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.Addr < h.base || int(req.Addr-h.base)+int(req.Quantity) > len(h.regs) {
		return nil, modbus.ErrIllegalDataAddress
	}
	off := req.Addr - h.base
	out := make([]uint16, req.Quantity)
	copy(out, h.regs[off:off+req.Quantity])
	return out, nil
}

func (h *testHandler) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}
func (h *testHandler) HandleCoils(*modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
func (h *testHandler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}

// startServer brings up a Modbus TCP server holding 300 registers starting at
// 42000, and returns its host:port.
func startServer(t *testing.T) (string, *testHandler) {
	t.Helper()

	h := &testHandler{base: 42000, regs: make([]uint16, 300)}
	h.regs[82] = 100     // 42082: a plausible SOC
	h.regs[100] = 0x0000 // 42100/42101: 1.0f, low word first
	h.regs[101] = 0x3F80 // (as the EcoFlow devices encode floats)
	h.regs[200] = 0xFFFB // 42200: -5 as int16

	addr := freePort(t)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        "tcp://" + addr,
		Timeout:    2 * time.Second,
		MaxClients: 4,
	}, h)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return addr, h
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// exec runs the command exactly as a user would and returns stdout plus the
// exit code.
func exec(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	if errOut.Len() > 0 {
		t.Logf("stderr: %s", errOut.String())
	}
	return out.String(), code
}

func TestReadSingleRegister(t *testing.T) {
	addr, _ := startServer(t)

	out, code := exec(t, addr, "42082", "uint16")
	if code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "42082") || !strings.Contains(out, "0x0064") || !strings.HasSuffix(strings.TrimSpace(out), "100") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// The hex form of an address must reach the same register as the decimal one.
func TestHexAddressEqualsDecimal(t *testing.T) {
	addr, _ := startServer(t)

	dec, _ := exec(t, addr, "42082", "uint16")
	hex, _ := exec(t, addr, fmt.Sprintf("0x%X", 42082), "uint16")
	if dec != hex {
		t.Fatalf("decimal and hex address disagree:\n%s\n%s", dec, hex)
	}
}

func TestHexOutput(t *testing.T) {
	addr, _ := startServer(t)

	out, code := exec(t, addr, "42200", "int16", "--out", "hex")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "0xA4D8") { // the address
		t.Errorf("address not in hex:\n%s", out)
	}
	if !strings.Contains(strings.TrimSpace(out), "0xFFFB") { // two's complement
		t.Errorf("value not in hex:\n%s", out)
	}

	dec, _ := exec(t, addr, "42200", "int16")
	if !strings.Contains(dec, "-5") {
		t.Errorf("decimal output should show -5:\n%s", dec)
	}
}

func TestFloatWordOrder(t *testing.T) {
	addr, _ := startServer(t)

	out, code := exec(t, addr, "42100", "float32", "--word-order", "low")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "1\n") {
		t.Fatalf("expected 1, got:\n%s", out)
	}
}

// A single invalid address inside a range must not take the whole scan down:
// the surrounding registers still have to be reported.
func TestRangeIsolatesBadAddress(t *testing.T) {
	addr, _ := startServer(t)

	// 42000..42299 exist, so a range crossing the end mixes good and bad.
	out, code := exec(t, addr, "42295", "raw", "--count", "10")
	if code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out)
	}
	lines := dataLines(out)
	if len(lines) != 10 {
		t.Fatalf("expected 10 rows, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(out, "illegal data address") {
		t.Errorf("expected an illegal data address row:\n%s", out)
	}
	if !strings.Contains(lines[0], "42295") {
		t.Errorf("first row should be 42295:\n%s", out)
	}
}

// Reading more than 125 registers has to be split into several requests.
func TestChunking(t *testing.T) {
	addr, _ := startServer(t)

	out, code := exec(t, addr, "42000", "raw", "--count", "200")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if got := len(dataLines(out)); got != 200 {
		t.Fatalf("expected 200 rows, got %d", got)
	}
	if strings.Contains(out, "illegal") {
		t.Fatalf("chunked read should have succeeded:\n%s", out)
	}
}

func TestJSONOutput(t *testing.T) {
	addr, _ := startServer(t)

	out, code := exec(t, addr, "42082", "uint16", "--json", "--out", "hex")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	// --out must not leak into JSON: it stays decimal and numeric.
	want := `{"address":42082,"raw":["0x0064"],"value":100}`
	if strings.TrimSpace(out) != want {
		t.Fatalf("got %s, want %s", strings.TrimSpace(out), want)
	}
}

func TestPollingSamples(t *testing.T) {
	addr, _ := startServer(t)

	out, code := exec(t, addr, "42082", "uint16", "--interval", "20ms", "--samples", "3")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if got := len(dataLines(out)); got != 3 {
		t.Fatalf("expected 3 samples, got %d:\n%s", got, out)
	}
}

// --on-change prints the baseline, then only what actually changed.
func TestPollingOnChange(t *testing.T) {
	addr, h := startServer(t)

	go func() {
		time.Sleep(120 * time.Millisecond)
		h.set(42084, 7)
	}()

	out, code := exec(t, addr, "42082", "uint16", "--count", "4", "--interval", "40ms", "--samples", "8", "--on-change")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	lines := dataLines(out)
	if len(lines) != 5 {
		t.Fatalf("expected 4 baseline rows plus 1 change, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[4], "42084") || !strings.HasSuffix(strings.TrimSpace(lines[4]), "7") {
		t.Fatalf("the change row should be 42084 = 7:\n%s", out)
	}
}

func TestUnreachableHostFails(t *testing.T) {
	addr := freePort(t) // nothing listening there
	_, code := exec(t, addr, "42082", "uint16", "--timeout", "200ms")
	if code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
}

func TestBadArguments(t *testing.T) {
	for _, args := range [][]string{
		{"127.0.0.1"},
		{"127.0.0.1", "42082"},
		{"127.0.0.1", "42082", "word"},
		{"127.0.0.1", "0xZZ", "uint16"},
		{"127.0.0.1", "42082", "uint16", "--fc", "coil"},
		{"127.0.0.1", "42082", "uint16", "--out", "oct"},
		{"127.0.0.1", "42082", "uint16", "--count", "0"},
		{"127.0.0.1", "65535", "uint32"}, // would run past the address space
	} {
		if _, code := exec(t, args...); code == 0 {
			t.Errorf("%v: expected a non-zero exit code", args)
		}
	}
}

// dataLines returns the output rows without the header.
func dataLines(out string) []string {
	var rows []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" || strings.Contains(l, "addr") && strings.Contains(l, "raw") {
			continue
		}
		rows = append(rows, l)
	}
	return rows
}

func TestVersionFlag(t *testing.T) {
	out, code := exec(t, "--version")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.HasPrefix(out, "modbusread ") || !strings.Contains(out, runtime.GOOS) {
		t.Fatalf("unexpected version output: %q", out)
	}
}
