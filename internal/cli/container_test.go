package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The image for containers (the Dockerfile at the root of the
// repository) and its chapter of the manual,
// docs/manual/in-a-container.md.
//
// What needs no Docker is held on every run: the compose file the
// chapter shows is the file of the repository, the image is built with
// the Go of the module, and nothing but the module reaches the builder.
//
// The chapter itself is played against an image when FADODB_IMAGE
// names one, the image of fadodb, with the analytical engine; and what
// the chapter says of the small image is held when FADODB_IMAGE_CORE
// names that of fadodb-core. scripts/image.sh does both, and GitHub
// with it on every push:
//
//	docker build -t fadodb:check .
//	docker build -t fadodb:check-core --target core .
//	FADODB_IMAGE=fadodb:check FADODB_IMAGE_CORE=fadodb:check-core go test -count=1 -run Container ./internal/cli

// fromTheRepository reads a file of the repository, by its path from
// the root.
func fromTheRepository(t *testing.T, path ...string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// containerBlock is a block of the chapter on containers, by the mark
// that stands before it.
func containerBlock(t *testing.T, mark string) string {
	t.Helper()
	chapter := fromTheRepository(t, "docs", "manual", "in-a-container.md")
	_, after, found := strings.Cut(chapter, "<!-- "+mark+" -->\n```")
	if !found {
		t.Fatalf("the chapter has no block marked %q", mark)
	}
	_, after, _ = strings.Cut(after, "\n")
	block, _, found := strings.Cut(after, "\n```")
	if !found {
		t.Fatalf("the block marked %q does not end", mark)
	}
	return block
}

// The files of the image and the chapter say the same, and the image
// is built from the module alone.
func TestTheContainerFiles(t *testing.T) {
	// The compose file of the chapter is the file of the repository.
	if shown, file := containerBlock(t, "the compose file"), fromTheRepository(t, "examples", "container", "compose.yaml"); shown+"\n" != file {
		t.Errorf("the chapter does not show examples/container/compose.yaml as it is:\n%s", shown)
	}

	// The image is built with the Go of the module: a newer Go in
	// go.mod is a newer Go in the Dockerfile.
	dockerfile := fromTheRepository(t, "Dockerfile")
	module := regexp.MustCompile(`(?m)^go (\S+)$`).FindStringSubmatch(fromTheRepository(t, "go.mod"))
	built := regexp.MustCompile(`(?m)^ARG GO_VERSION=(\S+)$`).FindStringSubmatch(dockerfile)
	if module == nil || built == nil || module[1] != built[1] {
		t.Errorf("go.mod says %v and the Dockerfile builds with %v", module, built)
	}

	// Nothing reaches the builder but what the Dockerfile copies: the
	// list of what is kept out begins with everything, and lets in
	// what is copied and no more.
	var let []string
	lines := strings.Split(fromTheRepository(t, ".dockerignore"), "\n")
	first := ""
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first == "" {
			first = line
		}
		if rest, ok := strings.CutPrefix(line, "!"); ok {
			let = append(let, rest)
		}
	}
	var copied []string
	for _, m := range regexp.MustCompile(`(?m)^COPY ([A-Za-z_./ ]+)$`).FindAllStringSubmatch(dockerfile, -1) {
		words := strings.Fields(m[1])
		copied = append(copied, words[:len(words)-1]...)
	}
	if first != "*" || strings.Join(let, " ") != strings.Join(copied, " ") || len(copied) == 0 {
		t.Errorf(".dockerignore begins with %q and lets in %v; the Dockerfile copies %v", first, let, copied)
	}
}

// filesOf lists the files an image holds, in the order of their names,
// with what they weigh together: the files of a container made from
// it, but those Docker puts into every container, which are empty.
func filesOf(p *played, image string) (holds []string, total int64) {
	p.t.Helper()
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		p.t.Fatal(err)
	}
	holder := p.name + "-files-" + hex.EncodeToString(random)
	if code, _, said := docker(p.t, "", nil, "create", "--name", holder, image); code != 0 {
		p.t.Fatalf("a container of the image %s: %s", image, said)
	}
	defer p.remove(holder)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files, err := exec.CommandContext(ctx, "docker", "export", holder).Output()
	if err != nil {
		p.t.Fatalf("docker export: %v", err)
	}
	for archive := tar.NewReader(bytes.NewReader(files)); ; {
		file, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			p.t.Fatal(err)
		}
		if file.Typeflag == tar.TypeReg && file.Size > 0 {
			holds = append(holds, file.Name)
			total += file.Size
		}
	}
	sort.Strings(holds)
	return holds, total
}

// docker runs a command of Docker and answers how it ended and what it
// wrote.
func docker(t *testing.T, stdin string, env []string, args ...string) (code int, out, said string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ended *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ended):
		code = ended.ExitCode()
	default:
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return code, stdout.String(), stderr.String()
}

// played is the story of the chapter as the test plays it: the image
// under test in place of the chapter's, and names, ports and folders
// of its own in place of Acme's, so that it meets nothing else on the
// machine.
type played struct {
	t     *testing.T
	image string
	name  string            // the volume and the container, "acme" in the chapter
	paths map[string]string // a folder or a file of the chapter, and the test's
}

// in is the story within a part of the test, which fails by itself.
func (p played) in(t *testing.T) *played {
	p.t = t
	return &p
}

// line reads a line of the chapter as a shell would, and puts the
// test's own names in. A line begins with "docker"; what it sends to a
// file is answered apart.
func (p *played) line(line string) (args []string, into string) {
	p.t.Helper()
	words := strings.Fields(line)
	if len(words) < 2 || words[0] != "docker" || strings.ContainsAny(line, `'"$`) {
		p.t.Fatalf("the chapter has a line the test cannot type: %s", line)
	}
	words = words[1:]
	if n := len(words); n >= 2 && words[n-2] == ">" {
		words, into = words[:n-2], words[n-1]
	}
	for _, w := range words {
		switch {
		case w == "fadodb":
			w = p.image
		case w == "acme":
			w = p.name
		case w == "acme:/data":
			w = p.name + ":/data"
		case w == "container:acme":
			w = "container:" + p.name
		case w == "127.0.0.1:8080:8080":
			w = "127.0.0.1::8080"
		case w == "8443:8443":
			w = "127.0.0.1::8443"
		case w == "1000:1000":
			w = strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
		case w == "examples/container/compose.yaml":
			w = p.paths[w]
		case strings.HasPrefix(w, "/etc/acme/") || strings.HasPrefix(w, "/srv/acme"):
			from, to, _ := strings.Cut(w, ":")
			mine, known := p.paths[from]
			if !known {
				p.t.Fatalf("the chapter names %s, which the test has no place for", from)
			}
			w = mine + ":" + to
		}
		args = append(args, w)
	}
	return args, into
}

// run plays a line of the chapter.
func (p *played) run(line string, env ...string) (code int, out, said string) {
	p.t.Helper()
	args, _ := p.line(line)
	return docker(p.t, "", env, args...)
}

// must plays a line of the chapter that has to succeed.
func (p *played) must(line string, env ...string) (out, said string) {
	p.t.Helper()
	code, out, said := p.run(line, env...)
	if code != 0 {
		p.t.Fatalf("%s: exit %d\n%s%s", line, code, out, said)
	}
	return out, said
}

// address is where the machine reaches a port of the container.
func (p *played) address(container, port string) string {
	p.t.Helper()
	code, out, said := docker(p.t, "", nil, "port", container, port+"/tcp")
	if code != 0 {
		p.t.Fatalf("the port %s of %s: %s", port, container, said)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return first
}

// logs is what a container wrote.
func (p *played) logs(container string) string {
	p.t.Helper()
	_, out, said := docker(p.t, "", nil, "logs", container)
	return out + said
}

// healthy waits until the server at the address answers its health,
// and answers what it said.
func (p *played) healthy(client *http.Client, base, container string) string {
	p.t.Helper()
	for until := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		resp, err := client.Get(base + "/v1/health")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return strings.TrimSpace(string(body))
			}
		}
		if time.Now().After(until) {
			p.t.Fatalf("the server at %s did not answer: %v\n%s", base, err, p.logs(container))
		}
	}
}

// remove takes a container away, and says nothing when there is none.
func (p *played) remove(container string) {
	docker(p.t, "", nil, "rm", "--force", "--volumes", container)
}

var (
	shownVersion = regexp.MustCompile(`"version":"[^"]*"`)
	shownSince   = regexp.MustCompile(`since \d{4}-\d\d-\d\d \d\d:\d\d:\d\d UTC`)
)

// The chapter of the manual on containers, played against the image:
// what the image is, the five commands from nothing to a server, a
// call, the door for agents, the compose file, a certificate, the
// settings, a folder of the machine, a second container, the health
// and the stop.
func TestTheChapterOnContainers(t *testing.T) {
	image := os.Getenv("FADODB_IMAGE")
	if image == "" {
		t.Skip("FADODB_IMAGE names no image to play the chapter against: scripts/image.sh check")
	}
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	p := &played{t: t, image: image, name: "fadodb-test-" + hex.EncodeToString(random), paths: map[string]string{}}
	t.Cleanup(func() {
		p.remove(p.name)
		docker(t, "", nil, "volume", "rm", "--force", p.name)
	})

	// The image: what it holds, what it runs, who it runs as, where a
	// database is kept and the port it names.
	t.Run("the image", func(t *testing.T) {
		rows := between(fromTheRepository(t, "docs", "manual", "in-a-container.md"), "<!-- the image -->\n", "\n\n")
		_, out, said := docker(t, "", nil, "image", "inspect", "--format", "{{json .Config}}", image)
		var config struct {
			User, WorkingDir string
			Entrypoint, Cmd  []string
			ExposedPorts     map[string]any
			Volumes          map[string]any
		}
		if err := json.Unmarshal([]byte(out), &config); err != nil {
			t.Fatalf("the image %s: %v\n%s%s", image, err, out, said)
		}
		_, port := config.ExposedPorts["8080/tcp"]
		_, volume := config.Volumes["/data"]
		if config.User != "65532:65532" || config.WorkingDir != "/data" || !volume || !port || len(config.ExposedPorts) != 1 ||
			strings.Join(config.Entrypoint, " ") != "/fadodb" || strings.Join(config.Cmd, " ") != "help" {
			t.Errorf("the image is set up as %+v", config)
		}
		for _, row := range []string{
			"| What it holds | the tool, `/fadodb`, the C and C++ libraries the analytical engine needs, and the certificates",
			"| What it runs | the tool:",
			"| Who it runs as | the user `65532`, who is not root |",
			"| Where a database is kept | `/data`.",
			"| The port it names | `8080` |",
		} {
			if !strings.Contains(rows, row) {
				t.Errorf("the chapter's table of the image has no row: %s", row)
			}
		}
		// Told nothing, it prints its help: that of the program with the
		// analytical engine, which ends with no word on which program it
		// is.
		if code, out, _ := docker(t, "", nil, "run", "--rm", image); code != 0 || out != usage {
			t.Errorf("the image, told nothing: exit %d\n%s", code, out)
		}
		if code, out, _ := docker(t, "", nil, "run", "--rm", image, "version"); code != 0 || !strings.HasPrefix(out, "fadodb ") || !strings.HasSuffix(strings.TrimSpace(out), ", with the analytical engine") {
			t.Errorf("the program in the image says of itself: exit %d %s", code, out)
		}
		// It holds the tool, the libraries it needs and the certificates,
		// and no shell or other program: the files of a container made
		// from it.
		holds, total := filesOf(p.in(t), image)
		has := func(part string) bool {
			for _, name := range holds {
				if strings.Contains(name, part) {
					return true
				}
			}
			return false
		}
		for _, part := range []string{"fadodb", "etc/ssl/certs/ca-certificates.crt", "libstdc++.so", "libc.so", "licenses/LICENSE", "licenses/LICENSE-duckdb", "licenses/THIRD_PARTY_NOTICES.md"} {
			if !has(part) {
				t.Errorf("the image holds no %s", part)
			}
		}
		for _, name := range holds {
			for _, programs := range []string{"bin/", "sbin/", "usr/bin/", "usr/sbin/", "usr/local/bin/"} {
				if strings.HasPrefix(name, programs) {
					t.Errorf("the image holds a program beside the tool: %s", name)
				}
			}
		}
		// "about 118 MB: the tool is 90 of them".
		if total < 100e6 || total > 135e6 {
			t.Errorf("the files of the image are %d bytes, and the chapter says about 118 MB", total)
		}
	})

	// The small image, of the program without the analytical engine:
	// what the chapter says it holds and runs.
	t.Run("the small image", func(t *testing.T) {
		small := os.Getenv("FADODB_IMAGE_CORE")
		if small == "" {
			t.Skip("FADODB_IMAGE_CORE names no image of fadodb-core: scripts/image.sh check")
		}
		rows := between(fromTheRepository(t, "docs", "manual", "in-a-container.md"), "<!-- the small image -->\n", "\n\n")
		if !strings.Contains(rows, "| What it holds | the tool, `/fadodb-core`, and the certificates; FadoDB's licence and the notices of the code of others the tool carries, in `/licenses`. No shell and no other program |") {
			t.Errorf("the chapter's table of the small image:\n%s", rows)
		}
		_, out, said := docker(t, "", nil, "image", "inspect", "--format", "{{json .Config}}", small)
		var config struct {
			User, WorkingDir string
			Entrypoint, Cmd  []string
			ExposedPorts     map[string]any
			Volumes          map[string]any
		}
		if err := json.Unmarshal([]byte(out), &config); err != nil {
			t.Fatalf("the image %s: %v\n%s%s", small, err, out, said)
		}
		_, port := config.ExposedPorts["8080/tcp"]
		_, volume := config.Volumes["/data"]
		if config.User != "65532:65532" || config.WorkingDir != "/data" || !volume || !port || len(config.ExposedPorts) != 1 ||
			strings.Join(config.Entrypoint, " ") != "/fadodb-core" || strings.Join(config.Cmd, " ") != "help" {
			t.Errorf("the small image is set up as %+v", config)
		}
		// Told nothing, it prints its help, which ends by saying which
		// program it is.
		if code, out, _ := docker(t, "", nil, "run", "--rm", small); code != 0 || out != usage+coreNote {
			t.Errorf("the small image, told nothing: exit %d\n%s", code, out)
		}
		if code, out, _ := docker(t, "", nil, "run", "--rm", small, "version"); code != 0 || !strings.HasPrefix(out, "fadodb-core ") || strings.Contains(out, "analytical engine") {
			t.Errorf("the program in the small image says of itself: exit %d %s", code, out)
		}
		// It holds the tool, the certificates and the licences, and no
		// other file.
		holds, total := filesOf(p.in(t), small)
		if got := strings.Join(holds, " "); got != "etc/ssl/certs/ca-certificates.crt fadodb-core licenses/LICENSE licenses/THIRD_PARTY_NOTICES.md" {
			t.Errorf("the small image holds %s", got)
		}
		// "about 17 MB": the tool has its own budget of 20 MiB, which the
		// size gate holds (ADR-0072).
		if total < 15<<20 || total > 20<<20 {
			t.Errorf("the files of the small image are %d bytes, and the chapter says about 17 MB", total)
		}
		// It makes a database, as the other does.
		volume2 := p.name + "-core"
		defer docker(t, "", nil, "volume", "rm", "--force", volume2)
		if code, out, said := docker(t, "", nil, "run", "--rm", "-v", volume2+":/data", small, "init", "acme.fado"); code != 0 || !strings.Contains(out, "made the database acme.fado") {
			t.Errorf("the small image makes a database: exit %d\n%s%s", code, out, said)
		}
	})

	// From nothing to a server, in five commands.
	lines := strings.Split(containerBlock(t, "from nothing to a server"), "\n")
	if len(lines) != 5 {
		t.Fatalf("from nothing to a server is %d lines in the chapter", len(lines))
	}
	p.must(lines[0])
	if out, _ := p.must(lines[1]); !strings.Contains(out, "made the database acme.fado") {
		t.Errorf("init: %s", out)
	}
	if out, _ := p.must(lines[2]); !strings.Contains(out, "made the user olga, a person, of the system groups administrators") {
		t.Errorf("user add: %s", out)
	}
	// The key is what the command prints, and all of it: what the tool
	// says of the key goes to the terminal and not into the file.
	if _, into := p.line(lines[3]); into != "olga.key" {
		t.Fatalf("the key is sent to %q in the chapter", into)
	}
	key, said := p.must(lines[3])
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, "fado_") || strings.ContainsAny(key, " \n") || !strings.Contains(said, "shown this once") {
		t.Fatalf("key new: %q\n%s", key, said)
	}
	mine := "FADODB_KEY=" + key
	p.must(lines[4])
	base := "http://" + p.address(p.name, "8080")
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Errorf("the server is reached at %s, and the chapter says on the machine only", base)
	}
	health := p.healthy(http.DefaultClient, base, p.name)
	if said := containerBlock(t, "what the server says"); !strings.Contains(p.logs(p.name), said+"\n") {
		t.Errorf("the server said\n%s\nand the chapter shows\n%s", p.logs(p.name), said)
	}
	// A caller on the machine, with the key.
	if status, body := ask(t, http.DefaultClient, http.MethodPost, base+"/v1/whoami", key, "{}"); status != http.StatusOK || !strings.Contains(body, `"id":"olga"`) {
		t.Errorf("whoami from the machine: %d %s", status, body)
	}

	// The image as a caller: the tool beside the server, the key by
	// its name.
	t.Run("the tool in the image", func(t *testing.T) {
		called := strings.Split(containerBlock(t, "olga calls it"), "\n")
		if len(called) != 2 || called[0] != "export FADODB_KEY=$(cat olga.key)" {
			t.Fatalf("the chapter calls the server with\n%s", strings.Join(called, "\n"))
		}
		code, out, said := p.in(t).run(called[1], mine)
		if want := containerBlock(t, "olga calls it, the answer") + "\n"; code != 0 || out != want {
			t.Errorf("exec whoami from the image: exit %d\n%s%s", code, out, said)
		}
		if strings.Contains(called[1], "fado_") {
			t.Error("the chapter shows a key on a line")
		}
	})

	// The door for agents, started from the image as the chapter's
	// settings of an agent's program say. Here it is put beside the
	// server, which the chapter reaches at its address.
	t.Run("the door for agents", func(t *testing.T) {
		var settings struct {
			Servers map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(containerBlock(t, "the door from the image")), &settings); err != nil {
			t.Fatal(err)
		}
		door := settings.Servers["acme"]
		if door.Command != "docker" || len(door.Args) < 3 || door.Args[0] != "run" || door.Env["FADODB_URL"] == "" || door.Env["FADODB_KEY"] != "fado_…" {
			t.Fatalf("the chapter starts the door with %+v", door)
		}
		args := []string{"run", "--network", "container:" + p.name}
		for _, a := range door.Args[1:] {
			if a == "fadodb" {
				a = image
			}
			args = append(args, a)
		}
		const call = `{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "whoami", "arguments": {}, "_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": {}}}}` + "\n"
		code, out, said := docker(t, call, []string{"FADODB_URL=http://127.0.0.1:8080", mine}, args...)
		if code != 0 || strings.Count(out, "\n") != 1 || !strings.Contains(out, `"isError":false`) || !strings.Contains(out, `\"id\":\"olga\"`) {
			t.Errorf("the door from the image: exit %d\n%s%s", code, out, said)
		}
	})

	// A second container on the volume of a served database is a
	// second program, and is refused.
	t.Run("a second container", func(t *testing.T) {
		p := p.in(t)
		args, _ := p.line(containerBlock(t, "a second container"))
		code, out, said := docker(t, "", nil, args...)
		want := containerBlock(t, "a second container, the refusal") + "\n"
		if got := shownSince.ReplaceAllString(said, shownSince.FindString(want)); code != 1 || out != "" || got != want {
			t.Errorf("a check of a served database: exit %d\n%s%s", code, out, said)
		}
		// A second server is refused the same way.
		args, _ = p.line("docker run --rm -v acme:/data fadodb serve acme.fado")
		if code, _, said := docker(t, "", nil, args...); code != 1 || !strings.Contains(said, "[in_use]") {
			t.Errorf("a second server on the volume: exit %d\n%s", code, said)
		}
	})

	// The health, asked by the tool in the container, which is how
	// Docker asks.
	t.Run("the health", func(t *testing.T) {
		p := p.in(t)
		args, _ := p.line(containerBlock(t, "the health check"))
		code, out, said := docker(t, "", nil, args...)
		want := containerBlock(t, "the health check, the answer")
		if got := shownVersion.ReplaceAllString(strings.TrimSpace(out), `"version":"dev"`); code != 0 || got != want || strings.TrimSpace(out) != health {
			t.Errorf("the health from the container: exit %d\n%s%s", code, out, said)
		}
	})

	// Stopping: the server is told, says so and ends by itself, well
	// within the twenty seconds the line gives it.
	t.Run("stopping", func(t *testing.T) {
		p := p.in(t)
		_, timeout, _ := docker(t, "", nil, "inspect", "--format", "{{.Config.StopTimeout}}", p.name)
		began := time.Now()
		if code, _, said := docker(t, "", nil, "stop", p.name); code != 0 {
			t.Fatalf("docker stop: %s", said)
		}
		took := time.Since(began)
		_, ended, _ := docker(t, "", nil, "inspect", "--format", "{{.State.ExitCode}}", p.name)
		if strings.TrimSpace(timeout) != "20" || strings.TrimSpace(ended) != "0" || took > 10*time.Second ||
			!strings.HasSuffix(p.logs(p.name), containerBlock(t, "stopping")+"\n") {
			t.Errorf("stopped after %s with %s, given %s seconds:\n%s", took, strings.TrimSpace(ended), strings.TrimSpace(timeout), p.logs(p.name))
		}
		// The three commands of the machine now have the database.
		args, _ := p.line(containerBlock(t, "a second container"))
		if code, _, said := docker(t, "", nil, args...); code != 0 {
			t.Errorf("a check of a database whose server stopped: exit %d\n%s", code, said)
		}
	})
	p.remove(p.name)

	// Kept running: the compose file, with the image, the name of the
	// volume and a port of the test's own.
	t.Run("the compose file", func(t *testing.T) {
		p := p.in(t)
		file := fromTheRepository(t, "examples", "container", "compose.yaml")
		for was, is := range map[string]string{
			"    image: fadodb\n":                  "    image: " + image + "\n",
			`      - "127.0.0.1:8080:8080"` + "\n": `      - "127.0.0.1::8080"` + "\n",
			"    external: true\n":                 "    external: true\n    name: " + p.name + "\n",
		} {
			if strings.Count(file, was) != 1 {
				t.Fatalf("the compose file has no line %q", was)
			}
			file = strings.Replace(file, was, is, 1)
		}
		mine := filepath.Join(t.TempDir(), "compose.yaml")
		if err := os.WriteFile(mine, []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		p.paths["examples/container/compose.yaml"] = mine
		up, _ := p.line(containerBlock(t, "kept running"))
		if strings.Join(up[:3], " ") != "compose -f "+mine {
			t.Fatalf("the chapter starts the file with %v", up)
		}
		project := append([]string{"compose", "--project-name", p.name}, up[1:3]...)
		defer docker(t, "", nil, append(project, "down", "--timeout", "20")...)
		// --wait comes back when the health check says the server
		// answers.
		if code, out, said := docker(t, "", nil, append(project, up[3:]...)...); code != 0 {
			_, logs, more := docker(t, "", nil, append(project, "logs")...)
			t.Fatalf("docker compose up: exit %d\n%s%s%s%s", code, out, said, logs, more)
		}
		_, id, _ := docker(t, "", nil, append(project, "ps", "--quiet", "fadodb")...)
		id = strings.TrimSpace(id)
		_, state, _ := docker(t, "", nil, "inspect", "--format",
			"{{.State.Health.Status}} {{.HostConfig.ReadonlyRootfs}} {{.HostConfig.RestartPolicy.Name}} {{.HostConfig.CapDrop}}", id)
		if strings.TrimSpace(state) != "healthy true unless-stopped [ALL]" {
			t.Errorf("the server of the compose file is %s", state)
		}
		base := "http://" + p.address(id, "8080")
		if status, body := ask(t, http.DefaultClient, http.MethodPost, base+"/v1/whoami", key, "{}"); status != http.StatusOK || !strings.Contains(body, `"id":"olga"`) {
			t.Errorf("whoami of the server of the compose file: %d %s", status, body)
		}
		// Taking down what the file made, volumes and all, leaves the
		// database: the volume is not the file's.
		if code, _, said := docker(t, "", nil, append(project, "down", "--volumes", "--timeout", "20")...); code != 0 {
			t.Errorf("docker compose down: %s", said)
		}
		if code, _, said := docker(t, "", nil, "volume", "inspect", p.name); code != 0 {
			t.Errorf("the volume of the database is gone: %s", said)
		}
	})

	// Reaching it from other machines: the server shows a certificate
	// it reads from a folder of the machine, as the user of the image.
	t.Run("with a certificate", func(t *testing.T) {
		p := p.in(t)
		certFile, keyFile, pool := certificate(t)
		p.paths["/etc/acme/tls"] = filepath.Dir(certFile)
		line := containerBlock(t, "with a certificate")
		args, _ := p.line(line)
		// A key of the certificate that only its owner reads is one
		// the server cannot read.
		if code, _, said := docker(t, "", nil, args...); code != 0 {
			t.Fatalf("%s: %s", line, said)
		}
		for until := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			_, running, _ := docker(t, "", nil, "inspect", "--format", "{{.State.Running}}", p.name)
			if strings.TrimSpace(running) == "false" {
				break
			}
			if time.Now().After(until) {
				t.Fatalf("the server read a certificate it may not read:\n%s", p.logs(p.name))
			}
		}
		if logs := p.logs(p.name); !strings.Contains(logs, "permission denied") {
			t.Errorf("the server without its certificate said:\n%s", logs)
		}
		p.remove(p.name)
		// Allowed to read them, it serves over https.
		for _, file := range []string{certFile, keyFile} {
			if err := os.Chmod(file, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if code, _, said := docker(t, "", nil, args...); code != 0 {
			t.Fatalf("%s: %s", line, said)
		}
		defer p.remove(p.name)
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
		defer client.CloseIdleConnections()
		base := "https://" + p.address(p.name, "8443")
		p.healthy(client, base, p.name)
		if status, body := ask(t, client, http.MethodPost, base+"/v1/whoami", key, "{}"); status != http.StatusOK || !strings.Contains(body, `"id":"olga"`) {
			t.Errorf("whoami over https: %d %s", status, body)
		}
	})

	// The settings and the secrets, handed to the container to read.
	t.Run("with the settings", func(t *testing.T) {
		p := p.in(t)
		dir := t.TempDir()
		settings, secrets := filepath.Join(dir, "settings.json"), filepath.Join(dir, "secrets")
		if err := os.WriteFile(settings, []byte(blockOfTheChapter(t, "the settings of the case")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(secrets, 0o755); err != nil {
			t.Fatal(err)
		}
		p.paths["/etc/acme/settings.json"], p.paths["/etc/acme/secrets"] = settings, secrets
		line := containerBlock(t, "with the settings")
		args, _ := p.line(line)
		if code, _, said := docker(t, "", nil, args...); code != 0 {
			t.Fatalf("%s: %s", line, said)
		}
		defer p.remove(p.name)
		p.healthy(http.DefaultClient, "http://"+p.address(p.name, "8080"), p.name)
		if logs := p.logs(p.name); !strings.Contains(logs, "fadodb serve: its actions may call over the connection books, "+booksOfTheChapter+"\n") {
			t.Errorf("the server with the settings said:\n%s", logs)
		}
	})

	// A folder of the machine in place of a volume, as its owner.
	t.Run("a folder of the machine", func(t *testing.T) {
		p := p.in(t)
		dir := t.TempDir()
		p.paths["/srv/acme"] = dir
		line := containerBlock(t, "a folder of the machine")
		args, _ := p.line(line)
		if code, out, said := docker(t, "", nil, args...); code != 0 || !strings.Contains(out, "made the database acme.fado") {
			t.Fatalf("%s: exit %d\n%s%s", line, code, out, said)
		}
		// The database is in the folder, closed to every user but its
		// owner.
		info, err := os.Stat(filepath.Join(dir, "acme.fado"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the database in the folder: %v %v", info, err)
		}
		// As the user of the image, who does not own the folder, the
		// machine refuses.
		without := make([]string, 0, len(args))
		for i := 0; i < len(args); i++ {
			if args[i] == "--user" {
				i++
				continue
			}
			without = append(without, args[i])
		}
		without[len(without)-1] = "other.fado"
		if code, _, said := docker(t, "", nil, without...); code == 0 || !strings.Contains(said, "permission denied") {
			t.Errorf("a database in a folder of another user: exit %d\n%s", code, said)
		}
	})
}
