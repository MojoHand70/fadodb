package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The library as a program of C has it (ADR-0101, item 12): built with
// -buildmode=c-shared, testdata/try.c compiled against fadodb.h and
// linked to it, and run. The test needs a C compiler. Without one it is
// skipped, unless FADODB_CABI is must, as it is on GitHub, where a
// skipped test would not show.

// libraryName is the file the library is on this system (ADR-0101,
// item 10).
func libraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "fadodb-core.dll"
	case "darwin":
		return "libfadodb-core.dylib"
	}
	return "libfadodb-core.so"
}

func compiler(t *testing.T) string {
	t.Helper()
	for _, cc := range []string{os.Getenv("CC"), "cc", "gcc", "clang"} {
		if cc == "" {
			continue
		}
		if path, err := exec.LookPath(cc); err == nil {
			return path
		}
	}
	if os.Getenv("FADODB_CABI") == "must" {
		t.Fatal("no C compiler: FADODB_CABI is must, and the library is tried from C or not at all")
	}
	t.Skip("no C compiler; FADODB_CABI=must makes this a failure")
	return ""
}

// built builds the library into dir and answers its path; or, when
// FADODB_LIBRARY names one, answers a copy of that one in dir: the very
// file a release publishes (scripts/library.sh try).
func built(t *testing.T, dir string) string {
	t.Helper()
	lib := filepath.Join(dir, libraryName())
	if given := os.Getenv("FADODB_LIBRARY"); given != "" {
		data, err := os.ReadFile(given)
		if err != nil {
			t.Fatalf("FADODB_LIBRARY: %v", err)
		}
		if err := os.WriteFile(lib, data, 0o755); err != nil {
			t.Fatal(err)
		}
		return lib
	}
	golang, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("no go to build the library with: %v", err)
	}
	cmd := exec.Command(golang, "build", "-buildmode=c-shared", "-o", lib, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the library was not built: %v\n%s", err, out)
	}
	return lib
}

func TestTheLibraryFromC(t *testing.T) {
	cc := compiler(t)
	dir := t.TempDir()
	lib := built(t, dir)

	program := filepath.Join(dir, "try")
	if runtime.GOOS == "windows" {
		program += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Werror", "-I.", "-o", program, filepath.Join("testdata", "try.c")}
	switch runtime.GOOS {
	case "windows":
		// The DLL itself is what the program is linked to, and it
		// is found beside the program when it runs.
		args = append(args, lib)
	default:
		args = append(args, "-L"+dir, "-lfadodb-core", "-pthread")
		if runtime.GOOS == "linux" {
			args = append(args, "-Wl,-rpath,"+dir)
		}
	}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("try.c was not compiled against fadodb.h and the library: %v\n%s", err, out)
	}

	run := exec.Command(program, filepath.Join(dir, "books.fado"))
	run.Dir = dir
	switch runtime.GOOS {
	case "darwin":
		run.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+dir)
	case "linux":
		run.Env = append(os.Environ(), "LD_LIBRARY_PATH="+dir)
	}
	out, err := run.CombinedOutput()
	t.Logf("try:\n%s", out)
	if err != nil {
		t.Fatalf("try ended with %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(out)), "ok") {
		t.Fatal("try did not end with ok")
	}
}

// fadodb.h declares what the library exports, and nothing else: the
// header Go writes beside the library lists the functions it has.
func TestTheHeaderIsTheLibrarys(t *testing.T) {
	compiler(t)
	t.Setenv("FADODB_LIBRARY", "")
	dir := t.TempDir()
	lib := built(t, dir)
	generated, err := os.ReadFile(strings.TrimSuffix(lib, filepath.Ext(lib)) + ".h")
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile("fadodb.h")
	if err != nil {
		t.Fatal(err)
	}
	names := func(text []byte, pattern string) []string {
		var out []string
		for _, m := range regexp.MustCompile(pattern).FindAllSubmatch(text, -1) {
			out = append(out, string(m[1]))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	exported := names(generated, `(?m)^extern .*\b(fado_\w+)\(`)
	declared := names(written, `(?m)^[a-z].*\b(fado_\w+)\(`)
	if !slices.Equal(exported, declared) {
		t.Fatalf("the library exports %v and fadodb.h declares %v", exported, declared)
	}
}
