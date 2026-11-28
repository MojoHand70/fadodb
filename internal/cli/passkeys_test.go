package cli

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/qrcode/qrcodetest"
	"github.com/MojoHand70/fadodb/internal/webauthn/webauthntest"
)

var (
	questionShown  = regexp.MustCompile(`data-question="([^"]*)"`)
	phoneCodeShown = regexp.MustCompile(`<figure class="code"[^>]*>(<svg.*?</svg>)`)
	heldShownRe    = regexp.MustCompile(`<form id="held"[^>]*data-question="([^"]*)"`)
)

// phoneLinkOf reads the code for the phone a page shows, as a phone's
// camera does, and answers the path of its link.
func phoneLinkOf(t *testing.T, base, page string) string {
	t.Helper()
	m := phoneCodeShown.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page shows no code for the phone:\n%s", page)
	}
	link, err := qrcodetest.ReadSVG(m[1])
	if err != nil || !strings.HasPrefix(link, base+"/auth/phone?t=fadop_") {
		t.Fatalf("the code reads %q, %v", link, err)
	}
	return strings.TrimPrefix(link, base)
}

// questionOf is the question a page gives the browser to make or use a
// passkey.
func questionOf(t *testing.T, page string) string {
	t.Helper()
	m := questionShown.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page asks no passkey:\n%s", page)
	}
	return html.UnescapeString(m[1])
}

// signInWith signs a browser in with the passkey a device holds, as the
// sign-in page's script does, and answers the page it ends on and what
// it sent.
func signInWith(t *testing.T, b *browser, d *webauthntest.Device) (int, string, string) {
	t.Helper()
	_, page := b.get("/auth/signin")
	answer, err := d.Use(b.base, questionOf(t, page))
	if err != nil {
		t.Fatal(err)
	}
	code, page := b.post("/auth/signin", url.Values{"do": {"passkey"}, "response": {answer}})
	return code, page, answer
}

// The chapter on signing in, its passkeys played against a server
// (ADR-0088): Olga invited on the machine, each row of the chapter's
// tables of invitations, of signing in with a passkey and of a person's
// passkeys, with a device that makes and uses passkeys as a phone or a
// computer does, in the browser's place.
func TestTheChapterOnPasskeys(t *testing.T) {
	chapter, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "signing-in.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(chapter), "\r\n", "\n")
	path := filepath.Join(t.TempDir(), "acme.fado")

	// Olga on the machine, line by line.
	var key, link string
	for _, line := range strings.Split(fenceAfter(t, text, "<!-- olga invited on the machine -->"), "\n") {
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
		case "user invite":
			link = strings.TrimSpace(out)
		}
	}
	if !strings.HasPrefix(link, "http://localhost:8080/auth/invite?t=fadoi_") || !strings.Contains(text, "http://localhost:8080/auth/invite?t=fadoi_") {
		t.Fatalf("the invitation the machine printed: %q", link)
	}
	addr, said, _ := serving(t, "--listen", "127.0.0.1:0", path)
	base := "http://" + addr
	link = strings.Replace(link, "http://localhost:8080", "", 1)
	client := &http.Client{Timeout: 10 * time.Second}
	command := func(name, body string) (int, string) { return ask(t, client, "POST", base+"/v1/"+name, key, body) }
	invitationOf := func(user string) string {
		t.Helper()
		code, body := command("create_invitation", `{"user": "`+user+`"}`)
		var made struct {
			Invitation struct{ Token string } `json:"invitation"`
		}
		if json.Unmarshal([]byte(body), &made); code != 200 || made.Invitation.Token == "" {
			t.Fatalf("create_invitation: %d %s", code, body)
		}
		return "/auth/invite?t=" + made.Invitation.Token
	}
	doesNotHold := func(b *browser, link string) bool {
		code, page := b.get(link)
		return code == http.StatusNotFound && strings.Contains(page, "This invitation does not hold")
	}
	// accept makes a passkey on a device with the invitation's page.
	accept := func(b *browser, link string, d *webauthntest.Device, device string) (int, string) {
		t.Helper()
		_, page := b.get(link)
		answer, err := d.Make(b.base, questionOf(t, page))
		if err != nil {
			t.Fatal(err)
		}
		token := strings.TrimPrefix(link, "/auth/invite?t=")
		return b.post("/auth/invite", url.Values{"t": {token}, "device": {device}, "response": {answer}})
	}

	// An invitation, row by row.
	olga := newBrowser(t, base)
	laptop := webauthntest.New(webauthntest.ES256)
	var ann3 string
	for _, row := range tableAfter(t, text, "<!-- an invitation -->") {
		var got string
		switch row[0] {
		case "Olga adds a passkey with her invitation":
			if _, page := olga.get(link); !strings.Contains(page, "<h1>Welcome, olga</h1>") || !strings.Contains(page, `data-device="olga"`) {
				t.Fatalf("the invitation's page:\n%s", page)
			}
			code, page := accept(olga, link, laptop, "Olga's laptop")
			if code == 200 && strings.Contains(page, "signed in as <strong>olga</strong>") && strings.Contains(page, "Olga&#39;s laptop") {
				got = "she is signed in, and her account page lists the passkey"
			}
		case "her invitation is opened again":
			if doesNotHold(newBrowser(t, base), link) {
				got = "the page says the invitation does not hold"
			}
		case "Olga makes Ann an invitation, then another":
			if code, body := command("create_user", `{"id": "ann", "kind": "person", "name": "Ann Nowak", "groups": ["finance"]}`); code != 200 {
				t.Fatalf("create_user: %d %s", code, body)
			}
			first := invitationOf("ann")
			invitationOf("ann")
			if doesNotHold(newBrowser(t, base), first) {
				got = "the first does not hold"
			}
		case "Olga withdraws Ann's invitation":
			second := invitationOf("ann")
			if code, body := command("withdraw_invitation", `{"user": "ann"}`); code != 200 {
				t.Fatalf("withdraw_invitation: %d %s", code, body)
			}
			if doesNotHold(newBrowser(t, base), second) {
				got = "its page says the invitation does not hold"
			}
			ann3 = invitationOf("ann")
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %q, the chapter says %s", row[0], got, row[1])
		}
	}

	// The code for the phone, row by row: Ann's computer shows it, her
	// phone reads it.
	annPC := newBrowser(t, base)
	onPhone := newBrowser(t, base)
	phone := webauthntest.New(webauthntest.EdDSA)
	phone.Counts, phone.Synced = false, true
	var phoneLink, phonePage, heldPC string
	// heldQuestion is the question of the form that signs in with a
	// passkey the device holds, which the page hides until the device
	// says it holds one.
	heldQuestion := func(page string) string {
		t.Helper()
		m := heldShownRe.FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("the page offers no sign-in with a passkey held:\n%s", page)
		}
		return html.UnescapeString(m[1])
	}
	for _, row := range tableAfter(t, text, "<!-- the code for the phone -->") {
		var got string
		switch row[0] {
		case "Ann opens her invitation on her computer":
			_, page := annPC.get(ann3)
			phoneLink = phoneLinkOf(t, base, page)
			if strings.Contains(page, "Point your phone&#39;s camera at the code. Your phone keeps the key.") && strings.Contains(page, ">Use this computer instead</button>") &&
				strings.Index(page, `<figure class="code"`) < strings.Index(page, "Use this computer instead") {
				got = "the page shows the code for her phone, and **Use this computer instead** below it"
			}
		case "her phone reads the code and opens its link":
			code, page := onPhone.get(phoneLink)
			phonePage = page
			if code == 200 && strings.Contains(page, "<h1>Welcome, Ann</h1>") && strings.Contains(page, `data-device="Ann Nowak"`) && !strings.Contains(page, `<figure class="code"`) {
				got = "the phone's page greets her, **Welcome, Ann**"
			}
		case "Ann adds the passkey on her phone":
			answer, err := phone.Make(base, questionOf(t, phonePage))
			if err != nil {
				t.Fatal(err)
			}
			if _, state := annPC.get("/auth/phone/state"); state != `{"state":"waiting"}` {
				t.Errorf("before the phone, the computer reads %s", state)
			}
			token := strings.TrimPrefix(phoneLink, "/auth/phone?t=")
			code, page := onPhone.post("/auth/phone", url.Values{"t": {token}, "device": {"Ann's iPhone"}, "response": {answer}})
			if code == 200 && strings.Contains(page, "signed in as <strong>ann</strong>") && strings.Contains(page, "Ann&#39;s iPhone") {
				got = "the phone is signed in"
			}
		case "the computer that showed the code":
			if _, state := annPC.get("/auth/phone/state"); state != `{"state":"done"}` {
				t.Errorf("after the phone, the computer reads %s", state)
			}
			other := newBrowser(t, base)
			otherCode, _ := other.post("/auth/phone/enter", url.Values{})
			code, page := annPC.post("/auth/phone/enter", url.Values{})
			again, _ := annPC.post("/auth/phone/enter", url.Values{})
			// The code signs one browser in, once.
			if code == 200 && strings.Contains(page, "signed in as <strong>ann</strong>") && otherCode == http.StatusUnauthorized && again == http.StatusUnauthorized {
				got = "is signed in too, and no other browser is"
			}
			if _, body := command("sign_ins", `{"user": "ann"}`); !strings.Contains(body, `"way":"phone"`) {
				t.Errorf("the computer's sign-in is not recorded with the way phone: %s", body)
			}
		case "the code's link is opened again":
			if code, page := newBrowser(t, base).get(phoneLink); code == http.StatusNotFound && strings.Contains(page, "This code has ended") {
				got = "the page says the code has ended"
			}
		case "Ann's account page, on her computer":
			_, page := annPC.get("/auth/account")
			phoneLinkOf(t, base, page)
			if strings.Contains(page, "To add a passkey on your phone, point its camera at the code.") {
				got = "shows a code of the same kind, to add a passkey on another phone"
			}
		case "her phone, which holds her synced passkey, reads the code of a new invitation":
			pc := newBrowser(t, base)
			heldPC = invitationOf("ann")
			_, page := pc.get(heldPC)
			phoneLink = phoneLinkOf(t, base, page)
			annPC = pc
			code, page := onPhone.get(phoneLink)
			phonePage = page
			// The phone's own refusal is the browser's: the page holds the
			// form that takes the button's place, hidden until then.
			// The phone's own refusal is the browser's, which a phone may
			// show only in a sheet of its own: the way to sign in with the
			// passkey it holds is offered from the start, under the button.
			if code == 200 && strings.Contains(page, `data-held="held"`) && strings.Contains(page, `<form id="held"`) &&
				strings.Contains(page, "This phone holds a passkey of yours for this server already.") && strings.Contains(page, ">Sign in with it</button>") &&
				strings.Contains(page, `data-show-held="held">A passkey of yours is on this phone already? Sign in with it</button>`) {
				heldQuestion(page)
				got = "its page offers **Sign in with it** under its button from the start, and in the button's place once the phone makes no other"
			}
		case "Ann signs in with it on her phone":
			answer, err := phone.Use(base, heldQuestion(phonePage))
			if err != nil {
				t.Fatal(err)
			}
			token := strings.TrimPrefix(phoneLink, "/auth/phone?t=")
			code, page := onPhone.post("/auth/phone", url.Values{"t": {token}, "do": {"held"}, "response": {answer}})
			entered, pcPage := annPC.post("/auth/phone/enter", url.Values{})
			if code == 200 && strings.Contains(page, "signed in as <strong>ann</strong>") && entered == 200 && strings.Contains(pcPage, "signed in as <strong>ann</strong>") &&
				doesNotHold(newBrowser(t, base), heldPC) {
				got = "the phone and the computer are signed in, and the invitation is used"
			}
		case "a computer that holds her synced passkey opens a new invitation of hers":
			pc := newBrowser(t, base)
			link := invitationOf("ann")
			_, page := pc.get(link)
			offered := strings.Contains(page, `data-held="held"`) && strings.Contains(page, "This device holds a passkey of yours for this server already.") &&
				strings.Contains(page, `data-show-held="held">A passkey of yours is on this device already? Sign in with it</button>`)
			answer, err := phone.Use(base, heldQuestion(page))
			if err != nil {
				t.Fatal(err)
			}
			token := strings.TrimPrefix(link, "/auth/invite?t=")
			code, page := pc.post("/auth/invite", url.Values{"t": {token}, "do": {"held"}, "response": {answer}})
			if offered && code == 200 && strings.Contains(page, "signed in as <strong>ann</strong>") && doesNotHold(newBrowser(t, base), link) {
				got = "the page offers **Sign in with it** too, and she is signed in with the invitation"
			}
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %q, the chapter says %s", row[0], got, row[1])
		}
	}

	// Signing in with a passkey, row by row.
	for _, row := range tableAfter(t, text, "<!-- a passkey -->") {
		var got string
		stranger := newBrowser(t, base)
		switch row[0] {
		case "Olga signs in with her passkey":
			code, page, answer := signInWith(t, stranger, laptop)
			if code == 200 && strings.Contains(page, "signed in as <strong>olga</strong>") {
				got = "she is signed in"
			}
			// What the browser sent, sent again.
			code, _ = newBrowser(t, base).post("/auth/signin", url.Values{"do": {"passkey"}, "response": {answer}})
			if code == http.StatusUnauthorized && lastCode(t, said) == "sign_in_refused" {
				laptop.Type = "replayed"
			}
		case "the browser's answer is sent a second time":
			if laptop.Type == "replayed" {
				got = "refused, sign_in_refused"
			}
			laptop.Type = ""
		case "a passkey whose counter went back, as a copy's does":
			ahead := laptop.Count
			laptop.Count = 1
			if code, _, _ := signInWith(t, stranger, laptop); code == http.StatusUnauthorized {
				got = "refused, " + lastCode(t, said)
			}
			laptop.Count = ahead
			if _, body := command("user_changes", `{"id": "olga"}`); strings.Contains(body, `"what":"passkey_counter_fell"`) {
				got += ", and recorded among Olga's changes"
			}
		case "a passkey made at another address":
			laptop.Host = "fado.acme.example"
			if code, _, _ := signInWith(t, stranger, laptop); code == http.StatusUnauthorized {
				got = "refused, " + lastCode(t, said)
			}
			laptop.Host = ""
		case "a passkey that was removed":
			command("create_user", `{"id": "ewa", "kind": "person", "groups": ["finance"]}`)
			key := webauthntest.New(webauthntest.ES256)
			accept(newBrowser(t, base), invitationOf("ewa"), key, "Ewa's key")
			_, body := command("list_passkeys", `{"user": "ewa"}`)
			var listed struct {
				Passkeys []struct{ ID string } `json:"passkeys"`
			}
			if json.Unmarshal([]byte(body), &listed); len(listed.Passkeys) != 1 {
				t.Fatalf("Ewa's passkeys: %s", body)
			}
			command("remove_passkey", `{"passkey": "`+listed.Passkeys[0].ID+`"}`)
			if code, _, _ := signInWith(t, stranger, key); code == http.StatusUnauthorized {
				got = "refused, " + lastCode(t, said)
			}
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %q, the chapter says %s", row[0], got, row[1])
		}
	}

	// A person's passkeys, row by row. Ann's phone holds the passkey of
	// her invitation, and her phone's session.
	computer := newBrowser(t, base)
	pc := webauthntest.New(webauthntest.RS256)
	var ids []string
	for _, row := range tableAfter(t, text, "<!-- passkeys -->") {
		var got string
		switch row[0] {
		case "Ann adds a passkey on her computer, on her account page":
			// Her computer holds none of hers: her phone lets her in, as
			// the browser's window to use a phone does.
			signInWith(t, computer, phone)
			_, page := computer.get("/auth/account")
			answer, err := pc.Make(base, questionOf(t, page))
			if err != nil {
				t.Fatal(err)
			}
			code, page := computer.post("/auth/account", url.Values{"do": {"add-passkey"}, "device": {"Ann's PC"}, "response": {answer}})
			if code == 200 && strings.Contains(page, "The passkey on Ann&#39;s PC is added.") && strings.Contains(page, "Ann&#39;s iPhone") {
				got = "her two passkeys are listed"
			}
			// From now on her computer signs in with its own.
			computer.post("/auth/signout", url.Values{})
			signInWith(t, computer, pc)
			// The id of each passkey, the phone's first, by the name of
			// its device.
			for _, name := range []string{"Ann&#39;s iPhone", "Ann&#39;s PC"} {
				_, after, _ := strings.Cut(page, `<div class="what"><span>`+name)
				if m := regexp.MustCompile(`name="passkey" value="([^"]+)"`).FindStringSubmatch(after); m != nil {
					ids = append(ids, m[1])
				}
			}
			if len(ids) != 2 {
				t.Fatalf("Ann's passkeys on her account page:\n%s", page)
			}
			// Signed in with a passkey, she is asked for no password and
			// offered no second step: they go with passwords.
			if _, mine := computer.get("/auth/account"); strings.Contains(mine, "<h2>Password</h2>") || strings.Contains(mine, "<h2>Second step</h2>") || strings.Contains(mine, `type="password"`) {
				t.Errorf("the account page of a passkey's session asks for a password:\n%s", mine)
			}
		case "Ann removes her phone's passkey":
			code, _ := computer.post("/auth/account", url.Values{"do": {"remove-passkey"}, "passkey": {ids[0]}})
			phoneCode, _ := onPhone.command("whoami", "{}")
			pcCode, _ := computer.command("whoami", "{}")
			if code == 200 && phoneCode == http.StatusUnauthorized && pcCode == 200 {
				got = "the session her phone began ends"
			}
		case "Ann removes her last passkey":
			if code, _ := computer.post("/auth/account", url.Values{"do": {"remove-passkey"}, "passkey": {ids[1]}}); code == http.StatusUnprocessableEntity {
				got = "refused, " + lastCode(t, said)
			}
		case "Olga removes Ann's last passkey, for a lost phone":
			code, _ := command("remove_passkey", `{"passkey": "`+ids[1]+`"}`)
			if pcCode, _ := computer.command("whoami", "{}"); code == 200 && pcCode == http.StatusUnauthorized {
				got = "it is removed, and Ann's session with it ends"
			}
		case "Ann lists Olga's passkeys":
			_, body := command("create_key", `{"user": "ann", "label": "laptop"}`)
			var made struct {
				Key struct{ Secret string } `json:"key"`
			}
			json.Unmarshal([]byte(body), &made)
			_, body = ask(t, client, "POST", base+"/v1/list_passkeys", made.Key.Secret, `{"user": "olga"}`)
			got = "refused, " + codeIn(body)
		case "create_invitation for an agent":
			command("create_user", `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`)
			_, body := command("create_invitation", `{"user": "ap-agent"}`)
			got = "refused, " + codeIn(body)
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %q, the chapter says %s", row[0], got, row[1])
		}
	}

	// The sign-ins of Olga are kept, as passkey.
	if code, body := command("sign_ins", `{"user": "olga"}`); code != 200 || !strings.Contains(body, `"way":"passkey","address":"127.0.0.1","ok":true`) || !strings.Contains(body, `"way":"passkey","address":"127.0.0.1","ok":false`) {
		t.Errorf("the sign-ins of Olga: %d %s", code, body)
	}
}

// The sign-in page offers passwords below the passkey, while they last,
// and a page with a passkey's question holds the server's script.
func TestTheSignInPageOffersAPasswordBelow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	addr, _, _ := serving(t, "--listen", "127.0.0.1:0", path)
	b := newBrowser(t, "http://"+addr)
	_, page := b.get("/auth/signin?next=/v1/whoami")
	for _, want := range []string{`data-passkey="use"`, "Sign in with a passkey", `href="/auth/signin?with=password&amp;next=%2Fv1%2Fwhoami"`, "No passkey yet?"} {
		if !strings.Contains(page, want) {
			t.Errorf("the sign-in page lacks %s:\n%s", want, page)
		}
	}
	_, page = b.get("/auth/signin?with=password")
	if !strings.Contains(page, `name="password"`) || !strings.Contains(page, "Sign in with a passkey") || strings.Contains(page, "data-passkey") {
		t.Errorf("the page of a password:\n%s", page)
	}
}
