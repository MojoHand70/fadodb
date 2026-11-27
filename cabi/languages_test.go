package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The chapter "In a program of another language"
// (docs/manual/other-languages.md): each program it shows is its file
// under examples/languages, word for word, and each prints what the
// chapter says every program prints. A language whose tools are not on
// the machine is skipped, unless FADODB_LANGUAGES is must, as on one
// machine of GitHub, where a skipped language would not show.

const chapter = "../docs/manual/other-languages.md"

// shown are the blocks of the chapter, by the comment above each.
func shown(t *testing.T) map[string]string {
	t.Helper()
	page, err := os.ReadFile(chapter)
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[string]string{}
	for _, m := range regexp.MustCompile("(?s)<!-- (.+?) -->\r?\n```[a-z]*\r?\n(.*?)\r?\n```").FindAllSubmatch(page, -1) {
		blocks[string(m[1])] = string(m[2])
	}
	return blocks
}

func TestTheChapterShowsTheFiles(t *testing.T) {
	blocks := shown(t)
	files := []string{"invoice.json", "c/books.c", "python/books.py", "golang/main.go", "node/books.mjs", "dotnet/books.csproj", "dotnet/Program.cs"}
	for _, file := range files {
		path := "examples/languages/" + file
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		block, ok := blocks[path]
		if !ok {
			t.Errorf("the chapter does not show %s", path)
			continue
		}
		if block != strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			t.Errorf("the chapter shows %s otherwise than the file is", path)
		}
	}
	if len(blocks) != len(files)+1 {
		t.Errorf("the chapter has %d marked blocks, and the test knows %d", len(blocks), len(files)+1)
	}
}

// tool is the path of a program the machine has, or skips the language.
func tool(t *testing.T, names ...string) string {
	t.Helper()
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if os.Getenv("FADODB_LANGUAGES") == "must" {
		t.Fatalf("no %s on this machine, and FADODB_LANGUAGES is must", names[0])
	}
	t.Skipf("no %s on this machine", names[0])
	return ""
}

func TestEveryLanguageTellsTheStory(t *testing.T) {
	blocks := shown(t)
	want := blocks["what every program prints"]
	if want == "" {
		t.Fatal("the chapter does not say what every program prints")
	}
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	examples := filepath.Join(repository, "examples", "languages")
	cc := compiler(t)
	library := built(t, t.TempDir())

	// play runs a program of the story in an empty folder with
	// invoice.json, and holds what it prints to the chapter.
	play := func(t *testing.T, dir string, name string, args ...string) {
		t.Helper()
		invoice, err := os.ReadFile(filepath.Join(examples, "invoice.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "invoice.json"), invoice, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "FADODB_LIBRARY="+library, "PYTHONPATH="+filepath.Join(repository, "clients", "py"),
			"DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1")
		var out, failed bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &failed
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\n%s%s", filepath.Base(name), err, out.String(), failed.String())
		}
		if got := strings.TrimRight(strings.ReplaceAll(out.String(), "\r\n", "\n"), "\n"); got != want {
			t.Fatalf("it prints\n%s\nand the chapter says\n%s", got, want)
		}
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}

	t.Run("C", func(t *testing.T) {
		dir := t.TempDir()
		program := filepath.Join(dir, "books"+exe)
		args := []string{"-std=c11", "-Wall", "-Werror", "-I.", "-o", program, filepath.Join(examples, "c", "books.c")}
		switch runtime.GOOS {
		case "windows":
			args = append(args, library)
		default:
			args = append(args, "-L"+filepath.Dir(library), "-lfadodb-core")
			if runtime.GOOS == "linux" {
				args = append(args, "-Wl,-rpath,"+filepath.Dir(library))
			}
		}
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("books.c was not compiled: %v\n%s", err, out)
		}
		if runtime.GOOS == "windows" {
			// The library is found beside the program.
			data, err := os.ReadFile(library)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(library)), data, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if runtime.GOOS == "darwin" {
			t.Setenv("DYLD_LIBRARY_PATH", filepath.Dir(library))
		}
		play(t, dir, program)
	})

	t.Run("Python", func(t *testing.T) {
		python := tool(t, "python3", "python")
		if runtime.GOOS == "windows" {
			// python3 may be the stub of the store there.
			python = tool(t, "python", "python3")
		}
		play(t, t.TempDir(), python, filepath.Join(examples, "python", "books.py"))
	})

	t.Run("Go", func(t *testing.T) {
		golang := tool(t, "go")
		dir := t.TempDir()
		program := filepath.Join(dir, "books"+exe)
		build := exec.Command(golang, "build", "-o", program, "./examples/languages/golang")
		build.Dir = repository
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("the program of Go was not built: %v\n%s", err, out)
		}
		play(t, dir, program)
	})

	t.Run("Node", func(t *testing.T) {
		node := tool(t, "node")
		npm := tool(t, "npm", "npm.cmd")
		dir := t.TempDir()
		// koffi is fetched for the test, as the chapter says to.
		install := exec.Command(npm, "install", "--no-audit", "--no-fund", "--silent", "koffi")
		install.Dir = dir
		if out, err := install.CombinedOutput(); err != nil {
			if os.Getenv("FADODB_LANGUAGES") != "must" {
				t.Skipf("koffi could not be fetched: %v\n%s", err, out)
			}
			t.Fatalf("koffi could not be fetched: %v\n%s", err, out)
		}
		program, err := os.ReadFile(filepath.Join(examples, "node", "books.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "books.mjs"), program, 0o644); err != nil {
			t.Fatal(err)
		}
		play(t, dir, node, "books.mjs")
	})

	t.Run("C#", func(t *testing.T) {
		dotnet := tool(t, "dotnet")
		project := t.TempDir()
		for _, file := range []string{"books.csproj", "Program.cs"} {
			data, err := os.ReadFile(filepath.Join(examples, "dotnet", file))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, file), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		out := filepath.Join(project, "out")
		args := []string{"build", "--nologo", "-v", "quiet", "-c", "Release", "-o", out}
		// The project names .NET 10; a machine with an older .NET builds
		// the same program for the one it has.
		if version, err := exec.Command(dotnet, "--version").Output(); err == nil {
			if major, _, _ := strings.Cut(strings.TrimSpace(string(version)), "."); major != "10" && major != "" {
				args = append(args, "-p:TargetFramework=net"+major+".0")
			}
		}
		build := exec.Command(dotnet, args...)
		build.Dir = project
		build.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1")
		if text, err := build.CombinedOutput(); err != nil {
			t.Fatalf("the program of C# was not built: %v\n%s", err, text)
		}
		play(t, t.TempDir(), dotnet, filepath.Join(out, "books.dll"))
	})
}
