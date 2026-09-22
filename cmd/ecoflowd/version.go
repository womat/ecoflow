package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version can be set at build time with
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/ecoflowd
//
// When it is empty — the normal case — the value comes from the build
// information Go embeds automatically, so there is no version constant in the
// source that could drift away from what was actually built.
var version = ""

func versionString() string {
	v, rev, dirty := version, "", false

	if bi, ok := debug.ReadBuildInfo(); ok {
		if v == "" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}

	// A module version already carries the commit and dirty state for local
	// builds (Go stamps a pseudo-version), so only fall back to the raw
	// revision when there is nothing better.
	if v == "" || v == "(devel)" {
		switch {
		case rev != "":
			v = rev[:min(len(rev), 12)]
			if dirty {
				v += "+dirty"
			}
		default:
			v = "unknown"
		}
	}

	return fmt.Sprintf("modbusread %s (%s, %s/%s)", v, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
