package frames

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// taskLine is one message of tasks.txt.
type taskLine struct {
	at    string // HH:MM:SS, local time of the capture
	reply bool   // set_reply rather than set
	frame []byte
}

// readTaskCapture reads tasks.txt: what the app sent on .../set and what the
// device answered on .../set_reply while the maintainer worked the scheduled
// tasks in the app on 26 September 2026, serial and user id replaced by
// placeholders of the same length.
func readTaskCapture(t *testing.T) []taskLine {
	t.Helper()

	f, err := os.Open("testdata/tasks.txt")
	if err != nil {
		t.Fatalf("open capture: %v", err)
	}
	defer f.Close()

	var out []taskLine
	s := bufio.NewScanner(f)
	for s.Scan() {
		parts := strings.Fields(s.Text())
		if len(parts) < 4 || len(parts[0]) < 19 {
			continue
		}
		b, err := hex.DecodeString(parts[3])
		if err != nil {
			continue
		}
		out = append(out, taskLine{
			at:    parts[0][11:19],
			reply: strings.HasSuffix(parts[1], "/set_reply"),
			frame: b,
		})
	}
	if err := s.Err(); err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return out
}

// captured returns the one message at that time in that direction.
func captured(t *testing.T, lines []taskLine, at string, reply bool) []byte {
	t.Helper()
	for _, l := range lines {
		if l.at == at && l.reply == reply {
			return l.frame
		}
	}
	t.Fatalf("no message at %s (reply %v) in the capture", at, reply)
	return nil
}

// listedTask reads the task list the device answered with at that time and
// returns the task with this number.
func listedTask(t *testing.T, lines []taskLine, at string, number uint64) Task {
	t.Helper()
	f, err := ParseReply(captured(t, lines, at, true))
	if err != nil {
		t.Fatalf("ParseReply: %v", err)
	}
	tasks, ok := f.Tasks()
	if !ok {
		t.Fatalf("frame at %s is %v, not a task list", at, f.Command)
	}
	for _, task := range tasks {
		if task.Number == number {
			return task
		}
	}
	t.Fatalf("task %d not in the list at %s", number, at)
	return Task{}
}

const placeholderSerial = "HC31XXXXXXXXXXXX"

// TestBuildTaskSwitchIsTheAppsCommand is the test the write path rests on:
// a task taken from the device's own list and switched must give exactly the
// bytes the app sent for the same switch.
//
// Task 7 was enabled by the app at 22:25:32 and disabled at 22:33:37. The list
// before the disable (22:31:32, enabled and running) gives the "off", the list
// after it (22:33:45) gives the "on"; the window and every other field are the
// same in both.
func TestBuildTaskSwitchIsTheAppsCommand(t *testing.T) {
	lines := readTaskCapture(t)

	tests := []struct {
		name   string
		listAt string
		on     bool
		seq    int
		sentAt string
	}{
		{"disable", "22:31:32", false, 237, "22:33:37"},
		{"enable", "22:33:45", true, 181, "22:25:32"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := listedTask(t, lines, tc.listAt, 7)
			got, err := BuildTaskSwitch(placeholderSerial, tc.seq, task, tc.on)
			if err != nil {
				t.Fatalf("BuildTaskSwitch: %v", err)
			}
			want := captured(t, lines, tc.sentAt, false)
			if !bytes.Equal(got, want) {
				t.Errorf("built frame differs from the app's:\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestBuildTaskQueryIsTheAppsRequest(t *testing.T) {
	lines := readTaskCapture(t)

	tests := []struct {
		name   string
		seq    int
		sentAt string
	}{
		{"one-byte sequence", 5, "22:34:45"},
		{"two-byte sequence", 158, "22:22:16"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildTaskQuery(placeholderSerial, tc.seq)
			if err != nil {
				t.Fatalf("BuildTaskQuery: %v", err)
			}
			if want := captured(t, lines, tc.sentAt, false); !bytes.Equal(got, want) {
				t.Errorf("built request differs from the app's:\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestTaskList(t *testing.T) {
	lines := readTaskCapture(t)

	f, err := ParseReply(captured(t, lines, "22:22:16", true))
	if err != nil {
		t.Fatalf("ParseReply: %v", err)
	}
	if f.Seq != 158 {
		t.Errorf("got sequence %d, want 158 - the device copies the request's", f.Seq)
	}
	tasks, ok := f.Tasks()
	if !ok {
		t.Fatalf("not a task list: %v", f.Command)
	}

	want := []struct {
		number  uint64
		enabled bool
		typ     uint64
		mode    uint64
		window  string
	}{
		{7, true, 1, 132, "23:00-23:30"},
		{6, false, 1, 132, "15:30-16:00"},
		{4, false, 2, 129, "11:30-15:30"},
	}
	if len(tasks) != len(want) {
		t.Fatalf("got %d tasks, want %d", len(tasks), len(want))
	}
	for i, w := range want {
		got := tasks[i]
		if got.Number != w.number || got.Enabled != w.enabled || got.Type != w.typ ||
			got.Mode != w.mode || got.WindowText() != w.window || got.Running {
			t.Errorf("task %d: got %+v (%s), want %+v", i, got, got.WindowText(), w)
		}
	}
}

func TestTaskRunning(t *testing.T) {
	lines := readTaskCapture(t)

	if task := listedTask(t, lines, "22:25:52", 7); !task.Enabled || task.Running {
		t.Errorf("22:25:52: got enabled %v running %v, want enabled and not yet running",
			task.Enabled, task.Running)
	}
	if task := listedTask(t, lines, "22:31:32", 7); !task.Enabled || !task.Running {
		t.Errorf("22:31:32: got enabled %v running %v, want both", task.Enabled, task.Running)
	}
	if task := listedTask(t, lines, "22:33:45", 7); task.Enabled || task.Running {
		t.Errorf("22:33:45: got enabled %v running %v, want neither", task.Enabled, task.Running)
	}
}

func TestAcknowledgement(t *testing.T) {
	lines := readTaskCapture(t)

	f, err := ParseReply(captured(t, lines, "22:33:37", true))
	if err != nil {
		t.Fatalf("ParseReply: %v", err)
	}
	n, ok := f.Acknowledged()
	if !ok || n != 7 {
		t.Errorf("got task %d (%v), want 7", n, ok)
	}
	if f.Seq != 237 {
		t.Errorf("got sequence %d, want 237, that of the command it answers", f.Seq)
	}
}

// TestRepliesAreNotObfuscated pins down why ParseReply exists: read as if it
// came from the push topic, a list from set_reply turns into noise.
func TestRepliesAreNotObfuscated(t *testing.T) {
	lines := readTaskCapture(t)
	b := captured(t, lines, "22:22:16", true)

	plain, _ := ParseReply(b)
	scrambled, _ := Parse(b)
	if bytes.Equal(plain.Payload, scrambled.Payload) {
		t.Fatal("Parse and ParseReply agree; the sequence key should have made them differ")
	}
	if tasks, _ := scrambled.Tasks(); len(tasks) == 3 {
		t.Error("the deobfuscated list still read as three tasks")
	}
}

func TestBuildTaskSwitchRejects(t *testing.T) {
	good := Task{Number: 7, Type: TypeCharge, Mode: 129, Window: []byte{0xc6, 0x8a, 0x90, 0x2b}}

	tests := []struct {
		name string
		seq  int
		task Task
	}{
		{"sequence zero", 0, good},
		{"sequence too large", MaxTaskSeq + 1, good},
		{"no number", 1, Task{Type: TypeCharge, Window: good.Window}},
		{"no window", 1, Task{Number: 7, Type: TypeCharge}},
		{"truncated window", 1, Task{Number: 7, Type: TypeCharge, Window: []byte{0xc6, 0x8a}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildTaskSwitch(placeholderSerial, tc.seq, tc.task, true); err == nil {
				t.Error("got no error, want one")
			}
		})
	}
}

func TestWindowText(t *testing.T) {
	tests := []struct {
		start, end uint64
		want       string
	}{
		{0, 1440, "00:00-24:00"},
		{1350, 1380, "22:30-23:00"},
	}
	for _, tc := range tests {
		task := Task{Window: appendUvarint(nil, tc.start|tc.end<<16)}
		if got := task.WindowText(); got != tc.want {
			t.Errorf("got %s, want %s", got, tc.want)
		}
	}
}
