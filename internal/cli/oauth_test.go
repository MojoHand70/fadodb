package cli

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/signon"
)

var (
	onwardShown  = regexp.MustCompile(`content="0; url=([^"]+)"`)
	requestShown = regexp.MustCompile(`name="request" value="([^"]+)"`)
	grantShown   = regexp.MustCompile(`name="grant" value="([^"]+)"`)
	tagsShown    = regexp.MustCompile(`<[^>]+>`)
)

// client is a client of OAuth in a test: its id, where its codes go,
// the address it asks passes for, and the verifier of its next request.
type client struct {
	id, redirect, resource string
}

const testVerifier = "the-verifier-a-client-makes-for-one-request-and-keeps"

// authorize sends the person's browser to the server as a client does,
// and answers the page that asks the person, with the id of the request.
func (b *browser) authorize(c client, verifier string) (string, string) {
	b.t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {c.id}, "redirect_uri": {c.redirect}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "state": {"the client's state"}, "resource": {c.resource}}
	_, page := b.get("/auth/authorize?" + q.Encode())
	m := onwardShown.FindStringSubmatch(page)
	if m == nil {
		return "", page
	}
	_, page = b.get(html.UnescapeString(m[1]))
	r := requestShown.FindStringSubmatch(page)
	if r == nil {
		return "", page
	}
	return r[1], page
}

// answer answers the page that asks, and reads where the server sent
// the browser: the client's address, with what the client is told.
func (b *browser) answer(request, do string) url.Values {
	b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, b.base+"/auth/allow", strings.NewReader(url.Values{"request": {request}, "do": {do}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.origin)
	stay := *b.client
	stay.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := stay.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	resp.Body.Close()
	to, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusSeeOther {
		b.t.Fatalf("the answer of the page: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	return to.Query()
}

// tokens asks the server's token address, as a client does.
func tokens(t *testing.T, base string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(base+"/auth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	text, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(text, &got); err != nil {
		t.Fatalf("the token address answers %s", text)
	}
	return resp.StatusCode, got
}

// connected runs a whole connection for a person: the client asks, the
// person allows it, and the client exchanges its code.
func connected(t *testing.T, b *browser, c client) (map[string]any, url.Values) {
	t.Helper()
	request, page := b.authorize(c, testVerifier)
	if request == "" {
		t.Fatalf("the server did not ask the person:\n%s", page)
	}
	back := b.answer(request, "allow")
	_, passes := tokens(t, b.base, url.Values{"grant_type": {"authorization_code"}, "code": {back.Get("code")}, "redirect_uri": {c.redirect},
		"client_id": {c.id}, "code_verifier": {testVerifier}, "resource": {c.resource}})
	return passes, back
}

// The chapter on signing in, its part on connecting a client, played
// against a server: Ann connects Claude, known by a document published
// over HTTPS on this machine, and FadoFace, which Olga adds; every row
// of the chapter's table.
func TestTheChapterOnConnectingAClient(t *testing.T) {
	chapter, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "signing-in.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(chapter), "\r\n", "\n")

	// Claude's document, which the server reads over HTTPS.
	var published string
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, published)
	}))
	defer site.Close()
	roots := x509.NewCertPool()
	roots.AddCert(site.Certificate())
	documentOptions = signon.DocumentOptions{Loopback: true, Roots: roots}
	defer func() { documentOptions = signon.DocumentOptions{} }()
	claude := client{id: site.URL + "/oauth/mcp-oauth-client-metadata", redirect: "http://127.0.0.1:9/api/mcp/auth_callback"}
	published = `{"client_id": "` + claude.id + `", "client_name": "Claude", "redirect_uris": ["` + claude.redirect + `"],
		"token_endpoint_auth_method": "none", "grant_types": ["authorization_code", "refresh_token"], "response_types": ["code"]}`
	host := strings.TrimPrefix(site.URL, "https://")
	host = host[:strings.LastIndexByte(host, ':')]

	path := filepath.Join(t.TempDir(), "acme.fado")
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
	addr, said, _ := serving(t, "--listen", "127.0.0.1:0", path)
	base := "http://" + addr
	claude.resource = base + "/mcp"
	web := &http.Client{Timeout: 10 * time.Second}

	// Olga adds Ann, who sets her password and signs in, and FadoFace,
	// with the chapter's line.
	if code, body := ask(t, web, "POST", base+"/v1/create_user", key, `{"id": "ann", "kind": "person", "groups": ["finance"]}`); code != 200 {
		t.Fatalf("create_user: %d %s", code, body)
	}
	_, body := ask(t, web, "POST", base+"/v1/create_password_link", key, `{"user": "ann"}`)
	var made struct {
		Link fado.PasswordLink `json:"link"`
	}
	json.Unmarshal([]byte(body), &made)
	ann := newBrowser(t, base)
	ann.post("/auth/password", url.Values{"link": {made.Link.Token}, "password": {"ann's long password"}, "again": {"ann's long password"}})
	if code, page := ann.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"ann's long password"}}); code != 200 || !strings.Contains(page, "signed in as <strong>ann</strong>") {
		t.Fatalf("Ann signs in: %d\n%s", code, page)
	}
	add := strings.TrimPrefix(strings.TrimSpace(between(text, "**FadoFace is added by Olga**", "\n```\n\n")), ", as a face or an app of Acme's would be,\nwith the addresses its codes are sent to and the address it is given\npasses for:\n\n```\nfadodb exec add_client ")
	if code, body := ask(t, web, "POST", base+"/v1/add_client", key, strings.Trim(add, "'")); code != 200 {
		t.Fatalf("Olga adds FadoFace: %d %s\n%q", code, body, add)
	}
	face := client{id: "fadoface", redirect: "https://face.acme.example/callback", resource: base + "/v1"}

	// What the server publishes.
	for _, row := range tableAfter(t, text, "<!-- how passes are given -->") {
		address := row[0]
		resp, err := web.Get(base + address)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 404 {
			t.Errorf("there is nothing at %s", address)
		}
	}
	_, meta := ask(t, web, "GET", base+"/.well-known/oauth-authorization-server", "", "")
	var server map[string]any
	json.Unmarshal([]byte(meta), &server)
	if server["issuer"] != base || server["token_endpoint"] != base+"/auth/token" || server["client_id_metadata_document_supported"] != true ||
		!strings.Contains(meta, `"code_challenge_methods_supported":["S256"]`) || !strings.Contains(meta, `"token_endpoint_auth_methods_supported":["none"]`) {
		t.Errorf("how the server gives passes: %s", meta)
	}

	// Where passes for the door for agents come from, which a request to
	// it that proves nobody is told (ADR-0071, item 8).
	_, meta = ask(t, web, "GET", base+"/.well-known/oauth-protected-resource/mcp", "", "")
	if !strings.Contains(meta, `"resource":"`+base+`/mcp"`) || !strings.Contains(meta, `"authorization_servers":["`+base+`"]`) {
		t.Errorf("where passes for /mcp come from: %s", meta)
	}
	if resp, err := web.Post(base+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if want := `Bearer resource_metadata="` + base + `/.well-known/oauth-protected-resource/mcp"`; resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != want {
			t.Errorf("/mcp with no pass: %d, WWW-Authenticate %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}

	// The page that asks says what the chapter says.
	request, page := ann.authorize(claude, testVerifier)
	quote := between(text, `She reads: *"`, `"* She allows`)
	quote = strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(quote, "**", ""), "claude.ai", host)), " ")
	shown := strings.Join(strings.Fields(html.UnescapeString(tagsShown.ReplaceAllString(page, " "))), " ")
	shown = strings.ReplaceAll(strings.ReplaceAll(shown, " ,", ","), " .", ".")
	if request == "" || !strings.Contains(shown, quote) {
		t.Errorf("the page does not say what the chapter says:\n%s\nwant\n%s", shown, quote)
	}
	ann.answer(request, "deny")

	refused := func(code int, passes map[string]any) string {
		if code != 400 {
			return "status " + string(rune('0'+code/100))
		}
		return "refused, " + passes["error"].(string)
	}
	whoami := func(pass string) (int, string) { return ask(t, web, "POST", base+"/v1/whoami", pass, `{}`) }
	grants := func() []fado.Grant {
		_, body := ask(t, web, "POST", base+"/v1/list_grants", key, `{"user": "ann"}`)
		var list struct {
			Grants []fado.Grant `json:"grants"`
		}
		json.Unmarshal([]byte(body), &list)
		return list.Grants
	}
	var claudes map[string]any
	for _, row := range tableAfter(t, text, "<!-- connecting a client -->") {
		var got string
		switch row[0] {
		case "Ann allows Claude":
			passes, back := connected(t, ann, claude)
			claudes = passes
			list := grants()
			if strings.HasPrefix(passes["access_token"].(string), "fadoat_") && back.Get("state") == "the client's state" && back.Get("iss") == base &&
				len(list) == 1 && list[0].Address == "/mcp" && list[0].Agent == "ann~"+host {
				got = "Claude is given a pass for /mcp, as " + strings.Replace(list[0].Agent, host, "claude.ai", 1) + " acting for Ann"
			}
		case "FadoFace sends whoami with its pass":
			passes, _ := connected(t, ann, face)
			code, body := whoami(passes["access_token"].(string))
			var who struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			}
			json.Unmarshal([]byte(body), &who)
			if code == 200 {
				got = "answered " + who.User.ID
			}
		case "Claude's pass is sent to the commands":
			code, body := whoami(claudes["access_token"].(string))
			if code == 401 {
				got = "refused, " + codeIn(body) + ": a pass is for one address"
			}
		case "Claude calls whoami at /mcp with its pass":
			code, body := ask(t, web, "POST", base+"/mcp", claudes["access_token"].(string), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami"}}`)
			var answered struct {
				Result struct {
					Content []struct{ Text string } `json:"content"`
					IsError bool                    `json:"isError"`
				} `json:"result"`
			}
			var who struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
				For string `json:"for"`
			}
			if json.Unmarshal([]byte(body), &answered) == nil && len(answered.Result.Content) == 1 && !answered.Result.IsError &&
				json.Unmarshal([]byte(answered.Result.Content[0].Text), &who) == nil && code == 200 {
				got = "answered " + strings.Replace(who.User.ID, host, "claude.ai", 1) + ", acting for " + who.For
			} else {
				t.Errorf("Claude's whoami at /mcp: %d %s", code, body)
			}
		case "FadoFace's pass is sent to /mcp":
			passes, _ := connected(t, ann, face)
			code, body := ask(t, web, "POST", base+"/mcp", passes["access_token"].(string), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami"}}`)
			if code == 401 {
				got = "refused, " + codeIn(body) + ": a pass is for one address"
			}
		case "a code is exchanged without the proof it was asked with":
			request, _ := ann.authorize(face, testVerifier)
			back := ann.answer(request, "allow")
			got = refused(tokens(t, base, url.Values{"grant_type": {"authorization_code"}, "code": {back.Get("code")}, "redirect_uri": {face.redirect},
				"client_id": {face.id}, "code_verifier": {strings.Repeat("x", 50)}}))
		case "a code is exchanged a second time":
			request, _ := ann.authorize(face, testVerifier)
			back := ann.answer(request, "allow")
			form := url.Values{"grant_type": {"authorization_code"}, "code": {back.Get("code")}, "redirect_uri": {face.redirect}, "client_id": {face.id}, "code_verifier": {testVerifier}}
			_, first := tokens(t, base, form)
			got = refused(tokens(t, base, form))
			if code, _ := whoami(first["access_token"].(string)); code == 401 {
				got += ", and the grant ends"
			}
		case "a renewing pass is used a second time":
			passes, _ := connected(t, ann, face)
			form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {passes["refresh_token"].(string)}, "client_id": {face.id}}
			_, renewed := tokens(t, base, form)
			if code, _ := whoami(renewed["access_token"].(string)); code != 200 {
				t.Errorf("the renewed pass is refused")
			}
			got = refused(tokens(t, base, form))
			if code, _ := whoami(renewed["access_token"].(string)); code == 401 {
				got += ", and the grant ends: it was stolen"
			}
		case "Ann does not allow it":
			request, _ := ann.authorize(claude, testVerifier)
			back := ann.answer(request, "deny")
			got = "the client is told " + back.Get("error")
		case "a client the server does not know":
			stay := *ann.client
			stay.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			resp, err := stay.Get(base + "/auth/authorize?" + url.Values{"client_id": {"nobody"}, "redirect_uri": {"https://evil.example/cb"}, "response_type": {"code"}}.Encode())
			if err != nil {
				t.Fatal(err)
			}
			page, _ := readAll(resp)
			resp.Body.Close()
			if resp.StatusCode == 400 && resp.Header.Get("Location") == "" && strings.Contains(page, "This client is not known") {
				got = "the page says so, and nothing is sent to the client"
			}
		case "Ann ends the grant on her account page":
			before := grants()
			_, account := ann.get("/auth/account")
			if !strings.Contains(account, "<strong>Claude</strong>, acting for you as ann~"+host) {
				t.Errorf("the account page does not list Claude:\n%s", account)
			}
			for _, m := range grantShown.FindAllStringSubmatch(account, -1) {
				ann.post("/auth/account", url.Values{"do": {"end-grant"}, "grant": {m[1]}})
			}
			code, renewed := tokens(t, base, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {claudes["refresh_token"].(string)}, "client_id": {claude.id}})
			if code == 400 && renewed["error"] == "invalid_grant" && len(grants()) == 0 {
				got = "its passes are refused"
			}
			for _, g := range before {
				if g.Substitution == "" {
					continue
				}
				_, body := ask(t, web, "POST", base+"/v1/get", key, `{"class": "Substitution", "id": "`+g.Substitution+`"}`)
				if strings.Contains(body, `"state":"withdrawn"`) {
					got += ", and the substitution is withdrawn"
				}
			}
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %s, the chapter says %s\n%s", row[0], got, row[1], said)
		}
	}
}

// The rules of OAuth at the server's addresses, beside the chapter's
// rows: what a request for a code must hold and where its refusal goes,
// what the token address takes, the page that asks a person who signs
// in on the way, and a pass given back.
func TestTheRulesOfOAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
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
	addr, _, _ := serving(t, "--listen", "127.0.0.1:0", path)
	base := "http://" + addr
	web := &http.Client{Timeout: 10 * time.Second}
	ask(t, web, "POST", base+"/v1/create_user", key, `{"id": "ann", "kind": "person", "groups": ["finance"]}`)
	ask(t, web, "POST", base+"/v1/add_client", key, `{"id": "fadoface", "name": "FadoFace", "redirect_uris": ["https://face.acme.example/callback"], "address": "/v1"}`)
	_, body := ask(t, web, "POST", base+"/v1/create_password_link", key, `{"user": "ann"}`)
	var made struct {
		Link fado.PasswordLink `json:"link"`
	}
	json.Unmarshal([]byte(body), &made)
	ann := newBrowser(t, base)
	ann.post("/auth/password", url.Values{"link": {made.Link.Token}, "password": {"ann's long password"}, "again": {"ann's long password"}})
	face := client{id: "fadoface", redirect: "https://face.acme.example/callback", resource: base + "/v1"}

	// Ann's browser has no session yet: she signs in on the way, and
	// comes to the page that asks her.
	request, page := ann.authorize(face, testVerifier)
	if request != "" || !strings.Contains(page, `action="/auth/signin"`) {
		t.Fatalf("a person with no session is not asked to sign in:\n%s", page)
	}
	if !strings.Contains(page, `name="next" value="/auth/allow?request=`) {
		t.Errorf("the sign-in does not lead back to the request:\n%s", page)
	}
	next := html.UnescapeString(regexp.MustCompile(`name="next" value="([^"]+)"`).FindStringSubmatch(page)[1])
	if _, page := ann.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"ann's long password"}, "next": {next}}); !strings.Contains(page, "<strong>FadoFace</strong> (fadoface) wants to use this database as you") {
		t.Fatalf("after signing in, Ann is not asked:\n%s", page)
	}

	// The page that asks lets its form go to the client alone.
	sum := sha256.Sum256([]byte(testVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{"response_type": {"code"}, "client_id": {face.id}, "redirect_uri": {face.redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"s1"}}
	_, page = ann.get("/auth/authorize?" + q.Encode())
	resp, err := ann.client.Get(base + html.UnescapeString(onwardShown.FindStringSubmatch(page)[1]))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if policy := resp.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "form-action 'self' https://face.acme.example;") {
		t.Errorf("the policy of the page that asks: %s", policy)
	}
	// A form from another site is refused.
	other := *ann
	other.origin = "https://evil.example"
	if code, _ := other.post("/auth/allow", url.Values{"request": {"x"}, "do": {"allow"}}); code != http.StatusForbidden {
		t.Errorf("a form from another site: %d", code)
	}

	// What a request for a code must hold, refused back to the client.
	stay := *ann.client
	stay.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for name, c := range map[string]struct {
		change func(url.Values)
		want   string
	}{
		"no challenge":      {func(v url.Values) { v.Del("code_challenge") }, "invalid_request"},
		"a plain challenge": {func(v url.Values) { v.Set("code_challenge_method", "plain") }, "invalid_request"},
		"a token at once":   {func(v url.Values) { v.Set("response_type", "token") }, "unsupported_response_type"},
		"another address":   {func(v url.Values) { v.Set("resource", base+"/mcp") }, "invalid_target"},
		"another server":    {func(v url.Values) { v.Set("resource", "https://other.example/v1") }, "invalid_target"},
	} {
		v := url.Values{}
		for k, list := range q {
			v[k] = list
		}
		c.change(v)
		resp, err := stay.Get(base + "/auth/authorize?" + v.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		to, _ := url.Parse(resp.Header.Get("Location"))
		if got := to.Query(); resp.StatusCode != http.StatusSeeOther || to.Host != "face.acme.example" || got.Get("error") != c.want || got.Get("state") != "s1" || got.Get("iss") != base {
			t.Errorf("%s: %d %s", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	for name, v := range map[string]url.Values{
		"an address the client is not sent codes at": {"client_id": {face.id}, "redirect_uri": {"https://evil.example/cb"}},
		"a member given twice":                       {"client_id": {face.id, face.id}, "redirect_uri": {face.redirect}},
		"a document the server cannot read":          {"client_id": {"https://127.0.0.1:1/client"}, "redirect_uri": {face.redirect}},
	} {
		resp, err := stay.Get(base + "/auth/authorize?" + v.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
			t.Errorf("%s: %d %s", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}

	// What the token address takes.
	for name, c := range map[string]struct {
		form   url.Values
		status int
		want   string
	}{
		"another grant":       {url.Values{"grant_type": {"password"}, "username": {"ann"}}, 400, "unsupported_grant_type"},
		"a client secret":     {url.Values{"grant_type": {"authorization_code"}, "client_id": {face.id}, "client_secret": {"s"}}, 401, "invalid_client"},
		"a member twice":      {url.Values{"grant_type": {"refresh_token", "refresh_token"}}, 400, "invalid_request"},
		"a code that is none": {url.Values{"grant_type": {"authorization_code"}, "client_id": {face.id}, "code": {"x"}}, 400, "invalid_grant"},
	} {
		code, got := tokens(t, base, c.form)
		if code != c.status || got["error"] != c.want {
			t.Errorf("%s: %d %v", name, code, got)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/auth/token", strings.NewReader(`{"grant_type": "authorization_code"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = web.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("a body of JSON at the token address: %d %v", resp.StatusCode, resp.Header)
	}

	// A pass given back ends the grant; any other is answered the same.
	passes, _ := connected(t, ann, face)
	for _, token := range []string{"not a pass", passes["access_token"].(string)} {
		resp, err := web.PostForm(base+"/auth/revoke", url.Values{"token": {token}, "client_id": {face.id}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("a pass given back: %d", resp.StatusCode)
		}
	}
	if code, _ := ask(t, web, "POST", base+"/v1/whoami", passes["access_token"].(string), `{}`); code != 401 {
		t.Errorf("a pass given back still opens the commands: %d", code)
	}
}
