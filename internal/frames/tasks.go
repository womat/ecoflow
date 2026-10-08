package frames

import (
	"errors"
	"fmt"
)

// Scheduled tasks ("Geplante Aufgaben" in the German app).
//
// A task switches the system into a mode for a time window. Type 1, which the
// app calls "Laden des Akkus" (charge the battery), keeps the battery from
// discharging while it runs - measured on the DC Fit, see docs/research/api-status.md,
// section 3. That is why anything here deals with tasks at all.
//
// Field by field, as measured on the DC Fit; the names follow
// shuette42/ecoflow-energy-ha, which implements these for other models:
//
//	 2 is_cfg     1 created, 2 modified (3 deletes, never sent from here)
//	 3 number     the app does not show it; new tasks get the highest + 1
//	 4 is_enable  1 = enabled; missing = disabled, it is never sent as 0
//	 5 is_effect  only in the lists: 1 = running now
//	 6 type       1 "Laden des Akkus", 2 "Mit der Batterie Lasten betreiben"
//	 7 power      W, 0 = "Auto"
//	 8 time_mode  129 daily, 130 weekdays, 132 once
//	 9 param      weekday mask or date, depending on field 8
//	10 window     bytes holding one varint: start | end << 16, minutes
const (
	taskConfig  = 2
	taskNumber  = 3
	taskEnable  = 4
	taskEffect  = 5
	taskType    = 6
	taskPower   = 7
	taskMode    = 8
	taskParam   = 9
	taskWindow  = 10
	taskModify  = 2
	listEntries = 1
)

// TypeCharge is the task type that blocks discharging.
const TypeCharge = 1

// MaxTaskSeq is the largest sequence number the task builders accept.
//
// Unlike the stream switch, the app's own task commands did reach two-byte
// sequence numbers (181 and 237 in the captures), so they are not held to one.
const MaxTaskSeq = 1<<14 - 1

// Task is one entry of the device's task list.
type Task struct {
	Number  uint64
	Config  uint64 // how it was last configured: 1 created, 2 modified
	Enabled bool
	Running bool
	Type    uint64
	Power   uint64
	Mode    uint64
	Param   uint64

	// Window is field 10 exactly as the device reported it. It is handed
	// back unchanged when the task is switched; decoding it is only for
	// showing it.
	Window []byte
}

// Minutes returns the start and end of the window in minutes after local
// midnight, and whether the field could be read.
func (t Task) Minutes() (start, end uint64, ok bool) {
	v, next, ok := readVarint(t.Window, 0)
	if !ok || next != len(t.Window) {
		return 0, 0, false
	}
	return v & 0xFFFF, v >> 16, true
}

// WindowText formats the window as HH:MM-HH:MM, or "?" if it is unreadable.
func (t Task) WindowText() string {
	start, end, ok := t.Minutes()
	if !ok {
		return "?"
	}
	hm := func(m uint64) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }
	return hm(start) + "-" + hm(end)
}

// Tasks returns the task list a frame carries, and whether it carries one.
//
// The device sends the list in two ways with the same layout: pushed by itself
// as 96/10 after every change and when a task starts, and as the answer to a
// 96/127 on set_reply. An empty list is a valid answer - no tasks set up.
func (f Frame) Tasks() ([]Task, bool) {
	if f.Command != TaskList && f.Command != TaskQuery {
		return nil, false
	}
	var out []Task
	for _, e := range parse(f.Payload) {
		if e.number != listEntries || e.wire != wireBytes {
			continue
		}
		if t, ok := readTask(e.bytes); ok {
			out = append(out, t)
		}
	}
	return out, true
}

func readTask(b []byte) (Task, bool) {
	var t Task
	seen := false
	for _, f := range parse(b) {
		switch {
		case f.number == taskWindow && f.wire == wireBytes:
			t.Window = append([]byte(nil), f.bytes...)
		case f.wire != wireVarint:
			continue
		case f.number == taskConfig:
			t.Config = f.varint
		case f.number == taskNumber:
			t.Number, seen = f.varint, true
		case f.number == taskEnable:
			t.Enabled = f.varint == 1
		case f.number == taskEffect:
			t.Running = f.varint == 1
		case f.number == taskType:
			t.Type = f.varint
		case f.number == taskPower:
			t.Power = f.varint
		case f.number == taskMode:
			t.Mode = f.varint
		case f.number == taskParam:
			t.Param = f.varint
		}
	}
	return t, seen
}

// Acknowledged returns the task number a 96/125 answer names, and whether the
// frame is one. The device answers every 96/125 on set_reply with a 96/125 of
// its own - payload "08 0x 10 yy", field 2 the task - carrying the sequence
// number of the command it answers. Field 1 matched the task's type in every
// answer seen and is presumably an echo, not a result code; it is not read.
func (f Frame) Acknowledged() (uint64, bool) {
	if f.Command != TaskConfig {
		return 0, false
	}
	n, ok := find(parse(f.Payload), 2)
	if !ok || n.wire != wireVarint {
		return 0, false
	}
	return n.varint, true
}

// BuildTaskQuery returns the request for the task list (96/127).
//
// Captured from the app, which sends it after every change and whenever the
// list is opened: the usual header, no payload and no dataLen. It changes
// nothing on the device; it goes to the .../set topic all the same.
func BuildTaskQuery(serial string, seq int) ([]byte, error) {
	if seq < 1 || seq > MaxTaskSeq {
		return nil, fmt.Errorf("sequence %d out of range (1-%d)", seq, MaxTaskSeq)
	}
	return appCommand(serial, seq, TaskQuery.ID, nil)
}

// BuildTaskSwitch returns the command that enables or disables one task.
//
// A 96/125 always carries the whole task, never just the switch: the time
// window, the repetition and the type go along every time. So the task is
// sent back exactly as the device listed it, with the configuration marked as
// "modified" and only field 4 changed - set to 1, or left out to disable,
// which is how the app does it. Field 5 is the device's to report and is not
// sent. Fields 6 to 9 go out even when they are 0, as in the app's frames.
//
// That is the whole of the write: nothing is created, deleted, moved or
// "repaired" here. The bytes for a task the app listed reproduce the app's own
// command byte for byte - see the test against the capture.
func BuildTaskSwitch(serial string, seq int, t Task, on bool) ([]byte, error) {
	if seq < 1 || seq > MaxTaskSeq {
		return nil, fmt.Errorf("sequence %d out of range (1-%d)", seq, MaxTaskSeq)
	}
	if t.Number == 0 {
		return nil, errors.New("task has no number")
	}
	if _, _, ok := t.Minutes(); !ok {
		return nil, fmt.Errorf("task %d has no readable time window", t.Number)
	}

	var p []byte
	p = appendVarint(p, taskConfig, taskModify)
	p = appendVarint(p, taskNumber, t.Number)
	if on {
		p = appendVarint(p, taskEnable, 1)
	}
	p = appendVarint(p, taskType, t.Type)
	p = appendVarint(p, taskPower, t.Power)
	p = appendVarint(p, taskMode, t.Mode)
	p = appendVarint(p, taskParam, t.Param)
	p = appendBytes(p, taskWindow, t.Window)

	return appCommand(serial, seq, TaskConfig.ID, p)
}
