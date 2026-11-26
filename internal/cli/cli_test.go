package cli

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
	// The program of this package's tests has no analytical engine: it
	// is fadodb-core, and says so.
	if !strings.HasPrefix(out.String(), "fadodb-core dev") {
		t.Fatalf("output = %q, want prefix %q", out.String(), "fadodb-core dev")
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
			"fadodb-core 0.1.0 (1737118, 2026-11-06T20:44:42Z)"},
		{"go install module@version", "dev", "", "",
			&debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			"fadodb-core 0.1.0"},
		{"build from a checkout", "dev", "", "",
			&debug.BuildInfo{
				Main:     debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1737118abcdef"}},
			},
			"fadodb-core dev (1737118)"},
		{"no build information", "dev", "", "", nil, "fadodb-core dev"},
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

// A command says its help once, when it is asked for it and when it
// could not read its line.
func TestACommandSaysItsHelpOnce(t *testing.T) {
	for _, line := range [][]string{
		{"init"}, {"user", "add"}, {"key", "new"}, {"serve"}, {"exec"}, {"file"},
		{"mcp"}, {"health"}, {"class", "check"}, {"action", "check"}, {"inlet", "check"},
	} {
		for _, flag := range []string{"--help", "--no-such-flag"} {
			code, out, said := tool(append(append([]string{}, line...), flag)...)
			want := 0
			if flag != "--help" {
				want = 2
			}
			if n := strings.Count(out+said, "Usage:"); code != want || n != 1 {
				t.Errorf("fadodb %s %s: exit %d, its help %d times\n%s%s", strings.Join(line, " "), flag, code, n, out, said)
			}
		}
	}
}
