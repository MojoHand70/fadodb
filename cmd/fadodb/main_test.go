package main

import (
	"bytes"
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
