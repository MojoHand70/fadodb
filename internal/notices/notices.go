// Package notices writes THIRD_PARTY_NOTICES.md, the notices of the code
// of others that the programs, images and libraries of FadoDB carry: Go,
// its standard library and runtime, every module each is built from, and
// DuckDB. The licences of most of that code ask for their notice in every
// copy, so the file travels in every archive, image and wheel. A test
// holds it to the modules as they are (notices_test.go).
package notices

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A thing FadoDB ships that carries the code of others: a program or a
// library, the module it is built in, and how.
type shipped struct {
	name, module, pkg string
	cgo               bool
	tags              string
}

// What is shipped. fadodb links DuckDB's prebuilt library, which comes in
// modules of its own for each system, each carrying DuckDB; it is listed
// with the tag duckdb_use_lib, which leaves them out, and DuckDB is named
// once, below. libfadodb is built with that tag, and DuckDB's own shared
// library travels beside it (ADR-0102).
var ships = []shipped{
	{"fadodb-core", ".", "./cmd/fadodb-core", false, ""},
	{"fadodb", "analytics", "./cmd/fadodb", true, "duckdb_use_lib"},
	{"libfadodb-core", "cabi", ".", true, ""},
	{"libfadodb", "cabi", ".", true, "duckdb,duckdb_use_lib"},
}

// The systems a release is built for.
var systems = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}}

// A piece of others' code: what it is, its version, what of FadoDB
// carries it, and the files of its notice.
type piece struct {
	name, version string
	in            []string
	files         []file
}

type file struct{ name, text string }

// noticeFile is a file of a notice at the top of a module.
var noticeFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents|authors)([-._].*)?$`)

// Write answers the file for the repository at root.
func Write(root string) ([]byte, error) {
	goLine, err := goVersion(root)
	if err != nil {
		return nil, err
	}
	modules := map[string]*piece{}
	for _, s := range ships {
		for _, sys := range systems {
			if err := listed(root, s, sys, modules); err != nil {
				return nil, err
			}
		}
	}
	pieces := make([]*piece, 0, len(modules)+2)
	for _, p := range modules {
		pieces = append(pieces, p)
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].name < pieces[j].name })

	// Go itself is in every one of them.
	goroot, err := run(root, nil, "go", "env", "GOROOT")
	if err != nil {
		return nil, err
	}
	golang := &piece{name: "Go, its standard library and runtime", version: goLine, in: names()}
	if golang.files, err = filesOf(strings.TrimSpace(goroot)); err != nil {
		return nil, err
	}
	// DuckDB, linked into fadodb and beside libfadodb. Its Go driver
	// carries DuckDB's own licence, word for word, and records the
	// release it is built from.
	bindings := modules["github.com/duckdb/duckdb-go-bindings"]
	if bindings == nil {
		return nil, fmt.Errorf("no program is built with DuckDB's Go driver, and fadodb is")
	}
	dir, err := run(filepath.Join(root, "analytics"), nil, "go", "list", "-m", "-f", "{{.Dir}}", "github.com/duckdb/duckdb-go-bindings")
	if err != nil {
		return nil, err
	}
	var release struct {
		Version string `json:"duckdb_version"`
	}
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(dir), "duckdb-release.json"))
	if err == nil {
		err = json.Unmarshal(raw, &release)
	}
	if err != nil {
		return nil, fmt.Errorf("the release of DuckDB the Go driver is built from: %w", err)
	}
	duckdb := &piece{name: "DuckDB", version: release.Version, in: bindings.in, files: bindings.files}
	pieces = append([]*piece{golang, duckdb}, pieces...)

	var b bytes.Buffer
	b.WriteString(`# Third-party notices

FadoDB is the work of its authors, under the Apache License 2.0
([LICENSE](LICENSE)). Its programs, images and libraries carry the code
of others below, each under its own licence, whose notice follows in
full. The licences of most of it ask for their notice in every copy, so
this file travels in every archive, image and wheel of a release.

It is written from the modules each is built from, for every system a
release is built for, and a test holds it to them:
` + "`FADODB_UPDATE_NOTICES=1 go test ./internal/notices/`" + ` writes it again.

| Code | Version | Carried by |
|---|---|---|
`)
	for _, p := range pieces {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", p.name, p.version, strings.Join(p.in, ", "))
	}
	for _, p := range pieces {
		fmt.Fprintf(&b, "\n## %s %s\n", p.name, p.version)
		for _, f := range p.files {
			fmt.Fprintf(&b, "\n%s:\n\n````text\n%s\n````\n", f.name, strings.TrimRight(f.text, "\n"))
		}
	}
	return b.Bytes(), nil
}

// listed adds the modules one program or library is built from on one
// system.
func listed(root string, s shipped, sys [2]string, modules map[string]*piece) error {
	env := []string{"GOOS=" + sys[0], "GOARCH=" + sys[1], "CGO_ENABLED=0"}
	if s.cgo {
		env[2] = "CGO_ENABLED=1"
	}
	args := []string{"list", "-deps", "-f", "{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}"}
	if s.tags != "" {
		args = append(args, "-tags", s.tags)
	}
	out, err := run(filepath.Join(root, s.module), env, "go", append(args, s.pkg)...)
	if err != nil {
		return fmt.Errorf("the modules of %s for %s/%s: %w", s.name, sys[0], sys[1], err)
	}
	lines := bufio.NewScanner(strings.NewReader(out))
	for lines.Scan() {
		fields := strings.Split(lines.Text(), "\t")
		if len(fields) != 3 || strings.HasPrefix(fields[0], "github.com/MojoHand70/fadodb") {
			continue
		}
		p := modules[fields[0]]
		if p == nil {
			p = &piece{name: fields[0], version: fields[1]}
			if p.files, err = filesOf(fields[2]); err != nil {
				return fmt.Errorf("%s %s: %w", fields[0], fields[1], err)
			}
			modules[fields[0]] = p
		}
		if p.version != fields[1] {
			return fmt.Errorf("%s is built at %s in one program and at %s in %s: tidy the go.mod of %s", fields[0], p.version, fields[1], s.name, s.module)
		}
		if !slices.Contains(p.in, s.name) {
			p.in = append(p.in, s.name)
			slices.SortFunc(p.in, byShipped)
		}
	}
	return lines.Err()
}

// filesOf reads the files of a notice at the top of a directory, the
// licence first.
func filesOf(dir string) ([]file, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !noticeFile.MatchString(e.Name()) {
			continue
		}
		text, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, file{name: e.Name(), text: strings.ReplaceAll(string(text), "\r\n", "\n")})
	}
	if !slices.ContainsFunc(files, func(f file) bool {
		return strings.HasPrefix(strings.ToUpper(f.name), "LICEN") || strings.HasPrefix(strings.ToUpper(f.name), "COPYING")
	}) {
		return nil, fmt.Errorf("no licence at the top of %s", dir)
	}
	sort.SliceStable(files, func(i, j int) bool { return rank(files[i].name) < rank(files[j].name) })
	return files, nil
}

// rank puts the licence before the others.
func rank(name string) int {
	switch strings.ToUpper(name) {
	case "LICENSE", "LICENCE", "COPYING", "LICENSE.MD", "LICENSE.TXT":
		return 0
	}
	return 1
}

func names() []string {
	out := make([]string, len(ships))
	for i, s := range ships {
		out[i] = s.name
	}
	slices.SortFunc(out, byShipped)
	return out
}

func byShipped(a, b string) int { return strings.Compare(a, b) }

// goVersion is the go line of the core's go.mod: the Go a release is
// built with.
func goVersion(root string) (string, error) {
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(mod)
	if m == nil {
		return "", fmt.Errorf("go.mod has no go line")
	}
	return "go" + string(m[1]), nil
}

func run(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, failed bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &failed
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(failed.String()))
	}
	return out.String(), nil
}
