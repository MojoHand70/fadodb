package main

import (
	"bytes"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "fadodb dev") {
		t.Fatalf("output = %q, want prefix %q", out.String(), "fadodb dev")
	}
}

func TestVersionStringFrom(t *testing.T) {
	suffix := " " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
	cases := []struct {
		name            string
		ver, rev, built string
		info            *debug.BuildInfo
		want            string
	}{
		{"release build", "0.1.0", "1737118", "2026-11-06T20:44:42Z", nil,
			"fadodb 0.1.0 (1737118, 2026-11-06T20:44:42Z)"},
		{"go install module@version", "dev", "", "",
			&debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			"fadodb 0.1.0"},
		{"build from a checkout", "dev", "", "",
			&debug.BuildInfo{
				Main:     debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1737118abcdef"}},
			},
			"fadodb dev (1737118)"},
		{"no build information", "dev", "", "", nil, "fadodb dev"},
	}
	for _, c := range cases {
		if got := versionStringFrom(c.ver, c.rev, c.built, c.info); got != c.want+suffix {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want+suffix)
		}
	}
}

func TestRunNoArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "Usage:") {
		t.Fatalf("stderr = %q, want usage text", errOut.String())
	}
}

func TestRunUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"frobnicate"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), `unknown command "frobnicate"`) {
		t.Fatalf("stderr = %q, want unknown-command message", errOut.String())
	}
}
