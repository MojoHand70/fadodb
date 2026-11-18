package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/signon/signontest"
)

// browser is a person's browser in a test: it keeps cookies, follows
// the server's redirects and says which site a form comes from.
type browser struct {
	t      *testing.T
	client *http.Client
	base   string
	origin string
}

func newBrowser(t *testing.T, base string) *browser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &browser{t: t, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}, base: base, origin: base}
}

func (b *browser) get(path string) (int, string) {
	b.t.Helper()
	resp, err := b.client.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	text, _ := readAll(resp)
	return resp.StatusCode, text
}

func (b *browser) post(path string, form url.Values) (int, string) {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodPost, b.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.origin)
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	text, _ := readAll(resp)
	return resp.StatusCode, text
}

// command sends a command as a page of the server does: with the
// session the browser keeps.
func (b *browser) command(name, body string) (int, string) {
	b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, b.base+"/v1/"+name, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.origin)
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	text, _ := readAll(resp)
	return resp.StatusCode, text
}

func readAll(resp *http.Response) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

// fenceAfter is the fenced block that follows a marker of a chapter.
func fenceAfter(t *testing.T, text, marker string) string {
	t.Helper()
	_, rest, found := strings.Cut(text, marker+"\n")
	if !found {
		t.Fatalf("the chapter has no %s", marker)
	}
	_, rest, _ = strings.Cut(rest, "\n")
	body, _, _ := strings.Cut(rest, "\n```")
	return body
}

// lastCode is the code of the last request to a page or a command, as
// the server's log of requests says it.
func lastCode(t *testing.T, said *buffer) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(said.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var line struct {
			Command string `json:"command"`
			Code    string `json:"code"`
		}
		if json.Unmarshal([]byte(lines[i]), &line) == nil && line.Command != "" {
			return line.Code
		}
	}
	return ""
}

var (
	secretShown = regexp.MustCompile(`<p><code>([A-Z2-7 ]+)</code></p>`)
	codeShown   = regexp.MustCompile(`<li>([0-9A-Z]{5}-[0-9A-Z]{5})</li>`)
	signInShown = regexp.MustCompile(`name="signin" value="([^"]+)"`)
)

// The chapter on signing in, played against a server: Olga's first
// password through the link the machine made, her second step, Ann's
// password and session, a command from a page of the server, and every
// refusal of the chapter's table.
func TestTheChapterOnSigningIn(t *testing.T) {
	chapter, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "signing-in.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(chapter), "\r\n", "\n")
	path := filepath.Join(t.TempDir(), "acme.fado")

	// Olga on the machine, line by line.
	var key, link string
	for _, line := range strings.Split(fenceAfter(t, text, "<!-- olga on the machine -->"), "\n") {
		args := strings.Fields(strings.ReplaceAll(line, "acme.fado", path))
		if len(args) < 2 || args[0] != "fadodb" {
			t.Fatalf("a line of the chapter: %q", line)
		}
		if args[1] == "serve" {
			continue
		}
		code, out, said := tool(args[1:]...)
		if code != 0 {
			t.Fatalf("%s: %d %s", line, code, said)
		}
		switch args[1] + " " + args[2] {
		case "key new":
			key = strings.TrimSpace(out)
		case "user password":
			link = strings.TrimSpace(out)
		}
	}
	if !strings.HasPrefix(link, "http://localhost:8080/auth/password?link=fadol_") || !strings.Contains(text, "http://localhost:8080/auth/password?link=fadol_") {
		t.Fatalf("the link the machine printed: %q", link)
	}
	addr, said, _ := serving(t, "--listen", "127.0.0.1:0", path)
	base := "http://" + addr
	if !strings.Contains(said.String(), "people sign in at "+base+"/auth/signin") {
		t.Errorf("the server does not say where people sign in:\n%s", said)
	}
	link = strings.Replace(link, "http://localhost:8080", "", 1)

	// Olga sets her password with the link, and signs in.
	olga := newBrowser(t, base)
	if code, page := olga.get(link); code != 200 || !strings.Contains(page, "<strong>olga</strong>") {
		t.Fatalf("the link's page: %d\n%s", code, page)
	}
	token := strings.TrimPrefix(link, "/auth/password?link=")
	if code, page := olga.post("/auth/password", url.Values{"link": {token}, "password": {"olga's long password"}, "again": {"olga's long password"}}); code != 200 || !strings.Contains(page, "is set") {
		t.Fatalf("Olga sets her password: %d\n%s", code, page)
	}

	// The second step, row by row.
	rows := tableAfter(t, text, "<!-- the second step -->")
	if len(rows) != 5 {
		t.Fatalf("the table of the second step has %d rows", len(rows))
	}
	_, page := olga.post("/auth/signin", url.Values{"user": {"olga"}, "password": {"olga's long password"}})
	m := secretShown.FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, "otpauth://totp/FadoDB:olga") {
		t.Fatalf("%s: the page shows no key:\n%s", rows[0][0], page)
	}
	secret := m[1]
	signIn := signInShown.FindStringSubmatch(page)[1]
	_, page = olga.post("/auth/code", url.Values{"signin": {signIn}, "code": {fado.StepCode(secret, time.Now())}})
	codes := codeShown.FindAllStringSubmatch(page, -1)
	if len(codes) != 10 {
		t.Fatalf("%s: %d codes for a lost phone:\n%s", rows[1][0], len(codes), page)
	}
	if code, _ := olga.command("whoami", "{}"); code != 200 {
		t.Errorf("Olga's session sends no command: %d", code)
	}
	olga.post("/auth/signout", url.Values{})
	_, page = olga.post("/auth/signin", url.Values{"user": {"olga"}, "password": {"olga's long password"}})
	if !strings.Contains(page, "Enter the six digits") {
		t.Fatalf("%s: the page does not ask for the code:\n%s", rows[2][0], page)
	}
	// The code of the next step of thirty seconds is one she has not
	// given yet.
	fresh := fado.StepCode(secret, time.Now().Add(30*time.Second))
	if code, page := olga.post("/auth/code", url.Values{"signin": {signInShown.FindStringSubmatch(page)[1]}, "code": {fresh}}); code != 200 || !strings.Contains(page, "signed in as <strong>olga</strong>") {
		t.Fatalf("Olga gives the code of her app: %d\n%s", code, page)
	}
	olga.post("/auth/signout", url.Values{})
	_, page = olga.post("/auth/signin", url.Values{"user": {"olga"}, "password": {"olga's long password"}})
	if code, _ := olga.post("/auth/code", url.Values{"signin": {signInShown.FindStringSubmatch(page)[1]}, "code": {fresh}}); code != http.StatusUnauthorized || lastCode(t, said) != "sign_in_refused" {
		t.Errorf("%s: %d %s", rows[3][0], code, lastCode(t, said))
	}
	_, page = olga.post("/auth/signin", url.Values{"user": {"olga"}, "password": {"olga's long password"}})
	if code, page := olga.post("/auth/code", url.Values{"signin": {signInShown.FindStringSubmatch(page)[1]}, "code": {codes[4][1]}}); code != 200 || !strings.Contains(page, "signed in as <strong>olga</strong>") {
		t.Errorf("%s: %d", rows[4][0], code)
	}

	// Olga makes Ann a link with her key; Ann sets her password and
	// signs in with no second step.
	client := &http.Client{Timeout: 10 * time.Second}
	if code, body := ask(t, client, "POST", base+"/v1/create_user", key, `{"id": "ann", "kind": "person", "groups": ["finance"]}`); code != 200 {
		t.Fatalf("create_user: %d %s", code, body)
	}
	code, body := ask(t, client, "POST", base+"/v1/create_password_link", key, `{"user": "ann"}`)
	var made struct {
		Link fado.PasswordLink `json:"link"`
	}
	if json.Unmarshal([]byte(body), &made); code != 200 || made.Link.User != "ann" || made.Link.MadeBy != "olga" {
		t.Fatalf("create_password_link: %d %s", code, body)
	}
	ann := newBrowser(t, base)
	ann.post("/auth/password", url.Values{"link": {made.Link.Token}, "password": {"ann's long password"}, "again": {"ann's long password"}})
	if code, page := ann.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"ann's long password"}}); code != 200 || !strings.Contains(page, "signed in as <strong>ann</strong>") {
		t.Fatalf("Ann signs in: %d\n%s", code, page)
	}

	// Ann signs in on a second browser and changes her password on the
	// first: the second's session ends.
	other := newBrowser(t, base)
	other.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"ann's long password"}})
	if code, _ := other.command("whoami", "{}"); code != 200 {
		t.Fatalf("Ann's second browser: %d", code)
	}
	if code, page := ann.post("/auth/account", url.Values{"do": {"password"}, "old": {"ann's long password"}, "password": {"ann's new long password"}, "again": {"ann's new long password"}}); code != 200 || !strings.Contains(page, "your other sessions are ended") {
		t.Fatalf("Ann changes her password: %d\n%s", code, page)
	}
	if code, _ := other.command("whoami", "{}"); code != http.StatusUnauthorized {
		t.Errorf("the other session after the password changed: %d", code)
	}
	if code, _ := ann.command("whoami", "{}"); code != 200 {
		t.Errorf("the session the password was changed in: %d", code)
	}

	// A command from a page of the server, with the session.
	if !strings.Contains(text, "<!-- ann asks who she is -->\n```\nPOST /v1/whoami") {
		t.Error("the chapter does not show Ann's command")
	}
	if code, body := ann.command("whoami", "{}"); code != 200 || !strings.Contains(body, `"id":"ann"`) {
		t.Errorf("Ann asks who she is: %d %s", code, body)
	}
	if code, body := ask(t, client, "POST", base+"/v1/sign_ins", key, `{"user": "ann"}`); code != 200 || !strings.Contains(body, `"way":"link"`) || !strings.Contains(body, `"way":"password"`) {
		t.Errorf("the sign-ins of Ann: %d %s", code, body)
	}
	if code, body := ask(t, client, "POST", base+"/v1/list_sessions", key, `{"user": "ann"}`); code != 200 || strings.Count(body, `"id"`) != 1 {
		t.Errorf("the sessions of Ann: %d %s", code, body)
	}

	// Each refusal of the table.
	ask(t, client, "POST", base+"/v1/create_user", key, `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`)
	stranger := newBrowser(t, base)
	var wrongWords string
	for _, row := range tableAfter(t, text, "<!-- refused -->") {
		var got string
		switch row[0] {
		case "a wrong password":
			code, page := stranger.post("/auth/signin", url.Values{"user": {"olga"}, "password": {"not olga's password"}})
			wrongWords = regexp.MustCompile(`role="alert">([^<]+)<`).FindStringSubmatch(page)[1]
			got = "refused, " + lastCode(t, said)
			if code != http.StatusUnauthorized {
				got += ", status " + http.StatusText(code)
			}
		case "a user the database does not have", "an agent signs in with a password":
			user := "nobody"
			if strings.Contains(row[0], "agent") {
				user = "ap-agent"
			}
			_, page := stranger.post("/auth/signin", url.Values{"user": {user}, "password": {"a long enough guess"}})
			got = "refused, " + lastCode(t, said)
			if strings.Contains(page, wrongWords) {
				got += ", in the same words"
			}
			if strings.Contains(row[1], "an agent calls with a key") {
				got += ": an agent calls with a key"
			}
		case "a sixth wrong password within a quarter of an hour":
			for i := 0; i < 5; i++ {
				stranger.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"a long enough guess"}})
			}
			stranger.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"ann's new long password"}})
			got = "refused, " + lastCode(t, said) + ", for a while that grows"
		case "a link used a second time":
			_, page := stranger.get("/auth/password?link=" + made.Link.Token)
			if strings.Contains(page, "This link does not hold") {
				got = "the page says the link does not hold"
			}
		case "a password of 11 characters":
			_, body := ask(t, client, "POST", base+"/v1/create_password_link", key, `{"user": "ann"}`)
			json.Unmarshal([]byte(body), &made)
			stranger.post("/auth/password", url.Values{"link": {made.Link.Token}, "password": {"elevenchars"}, "again": {"elevenchars"}})
			got = "refused, " + lastCode(t, said)
		case "create_password_link for an agent":
			_, body := ask(t, client, "POST", base+"/v1/create_password_link", key, `{"user": "ap-agent"}`)
			got = "refused, " + codeIn(body)
		case "a command with a session, from another site":
			ann.origin = "https://evil.example"
			code, body := ann.command("whoami", "{}")
			ann.origin = base
			got = "refused, " + codeIn(body)
			if code != http.StatusUnauthorized {
				got += ", status " + http.StatusText(code)
			}
		case "a form of the pages sent from another site":
			ann.origin = "https://evil.example"
			code, _ := ann.post("/auth/signout", url.Values{})
			ann.origin = base
			got = "refused, with the status " + strconv.Itoa(code)
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %s, the chapter says %s", row[0], got, row[1])
		}
	}
	// Ann's session holds after the form from another site.
	if code, _ := ann.command("whoami", "{}"); code != 200 {
		t.Errorf("Ann's session after a form from another site: %d", code)
	}

	// The commands of the chapter are the engine's.
	for _, row := range tableAfter(t, text, "<!-- the commands -->") {
		for _, name := range strings.Split(row[0], ", ") {
			found := false
			for _, c := range fado.Commands() {
				found = found || c.Name == name
			}
			if !found {
				t.Errorf("the chapter names %s, which is no command", name)
			}
		}
	}
	// The settings of the chapter are taken.
	s, err := parseSettings([]byte(fenceAfter(t, text, "<!-- the settings -->")))
	if err != nil || s.url != "https://fado.acme.example" || s.sessions.Idle != 4*time.Hour || s.sessions.Max != 7*24*time.Hour || s.grants != 14*24*time.Hour {
		t.Errorf("the chapter's settings: %v %+v", err, s)
	}
}

func codeIn(body string) string {
	var answer struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal([]byte(body), &answer)
	return answer.Error.Code
}

// tableAfter reads the rows of the table that follows a marker of a
// chapter, the code marks taken off.
func tableAfter(t *testing.T, text, marker string) [][]string {
	t.Helper()
	_, rest, found := strings.Cut(text, marker+"\n")
	if !found {
		t.Fatalf("the chapter has no %s", marker)
	}
	body, _, _ := strings.Cut(rest, "\n\n")
	var out [][]string
	for i, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if i < 2 {
			continue
		}
		var cells []string
		for _, cell := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
			cells = append(cells, strings.ReplaceAll(strings.TrimSpace(cell), "`", ""))
		}
		out = append(out, cells)
	}
	return out
}

// The chapter's company sign-on, played against a server and a provider
// of OpenID Connect that the test runs: the settings of the chapter
// with the provider's address, the secret from the environment, Ewa
// added with her e-mail, and every row of its table.
func TestTheChapterOnACompanysSignOn(t *testing.T) {
	chapter, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "signing-in.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(chapter), "\r\n", "\n")
	provider := signontest.New()
	defer provider.Close()

	// The chapter's settings, with the address of this provider and no
	// url: the server is on this machine.
	told, err := parseSettings([]byte(fenceAfter(t, text, "<!-- a company's sign-on -->")))
	if err != nil || len(told.signon) != 1 || told.signon[0].Name != "microsoft" || !told.signon[0].CompanyEmails || told.url != "https://fado.acme.example" {
		t.Fatalf("the chapter's settings: %v %+v", err, told)
	}
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	written, _ := json.Marshal(map[string]any{"signon": map[string]any{"microsoft": map[string]any{
		"label": told.signon[0].Label, "issuer": provider.Issuer, "client_id": provider.ClientID, "company_emails": false}}})
	if err := os.WriteFile(settings, written, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FADODB_SECRET_MICROSOFT__CLIENT_SECRET", provider.Secret)
	path := filepath.Join(dir, "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: %s", args, said)
		}
	}
	_, key, _ := tool("key", "new", "olga", "--db", path)
	key = strings.TrimSpace(key)
	addr, said, _ := serving(t, "--listen", "127.0.0.1:0", "--settings", settings, path)
	base := "http://" + addr
	if strings.Contains(said.String(), "the sign-on microsoft reads the secret") {
		t.Errorf("the server says the sign-on has no secret:\n%s", said)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	create := strings.TrimPrefix(strings.TrimSpace(between(text, "**Olga adds Ewa first**, with her company e-mail:\n\n```\n", "\n```")), "fadodb exec create_user ")
	if code, body := ask(t, client, "POST", base+"/v1/create_user", key, strings.Trim(create, "'")); code != 200 || !strings.Contains(body, `"email":"ewa@acme.example"`) {
		t.Fatalf("Olga adds Ewa: %d %s", code, body)
	}

	// The page offers the button.
	ewa := newBrowser(t, base)
	if _, page := ewa.get("/auth/signin"); !strings.Contains(page, ">Sign in with Microsoft</button>") {
		t.Fatalf("the page offers no sign-on:\n%s", page)
	}
	signOn := func(b *browser) (int, string) {
		return b.post("/auth/signon", url.Values{"provider": {"microsoft"}, "next": {"/auth/account"}})
	}
	yes := true
	for _, row := range tableAfter(t, text, "<!-- signing on -->") {
		var got string
		switch row[0] {
		case "Ewa signs in with Microsoft the first time":
			provider.Person = signontest.Person{Subject: "oid-ewa", Email: "ewa@acme.example", Verified: &yes}
			_, page := signOn(ewa)
			_, account := ewa.get("/auth/account")
			if strings.Contains(page, `http-equiv="refresh" content="0; url=/auth/account"`) && strings.Contains(account, "signed in as <strong>ewa</strong>") {
				got = "she is signed in"
			}
			if _, body := ask(t, client, "POST", base+"/v1/user_changes", key, `{"id": "ewa"}`); strings.Contains(body, `"what":"signon_linked"`) {
				got += ", and her Microsoft account is tied to her user"
			}
		case "Ewa signs in again, her e-mail at Microsoft changed":
			provider.Person = signontest.Person{Subject: "oid-ewa", Email: "ewa.nowak@acme.example"}
			again := newBrowser(t, base)
			signOn(again)
			if _, account := again.get("/auth/account"); strings.Contains(account, "signed in as <strong>ewa</strong>") {
				got = "she is signed in: her account finds her"
			}
		case "Zed, who has no user, signs in with Microsoft", "another Microsoft account with Ewa's e-mail signs in":
			provider.Person = signontest.Person{Subject: "oid-zed", Email: "zed@acme.example", Verified: &yes}
			if strings.HasPrefix(row[0], "another") {
				provider.Person = signontest.Person{Subject: "oid-other", Email: "ewa@acme.example", Verified: &yes}
			}
			code, _ := signOn(newBrowser(t, base))
			got = "refused, " + lastCode(t, said)
			if code != http.StatusUnauthorized {
				got += ", status " + strconv.Itoa(code)
			}
			if strings.HasPrefix(row[0], "another") {
				got += ": her user is tied to her own"
			}
		case "Microsoft's answer comes to another browser than the one that began":
			provider.Person = signontest.Person{Subject: "oid-ewa", Email: "ewa@acme.example", Verified: &yes}
			began := newBrowser(t, base)
			began.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			req, _ := http.NewRequest(http.MethodPost, base+"/auth/signon", strings.NewReader("provider=microsoft"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", base)
			resp, err := began.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			resp, err = began.client.Get(resp.Header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			callback := resp.Header.Get("Location")
			other := newBrowser(t, base)
			resp, err = other.client.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			got = "refused, " + lastCode(t, said)
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %s, the chapter says %s", row[0], got, row[1])
		}
	}
	if _, body := ask(t, client, "POST", base+"/v1/sign_ins", key, `{"user": "ewa"}`); !strings.Contains(body, `"way":"signon"`) {
		t.Errorf("the sign-ins of Ewa: %s", body)
	}
}
