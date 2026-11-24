package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// The address a sign-in is recorded at, behind a proxy (ADR-0088, item
// 8): from a proxy, the last address of X-Forwarded-For that is not a
// proxy; from anybody else, the address of the connection, whatever the
// header says.
func TestTheAddressBehindAProxy(t *testing.T) {
	caddy := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	chain := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8")}
	for name, c := range map[string]struct {
		remote  string
		header  []string
		proxies []netip.Prefix
		want    string
	}{
		"no proxies: the header is not read":          {"127.0.0.1:41000", []string{"203.0.113.7"}, nil, "127.0.0.1"},
		"no proxies, no header":                       {"198.51.100.4:41000", nil, nil, "198.51.100.4"},
		"from the proxy: the caller it saw":           {"127.0.0.1:41000", []string{"203.0.113.7"}, caddy, "203.0.113.7"},
		"from the proxy over IPv6":                    {"[::1]:41000", []string{"203.0.113.7"}, caddy, "203.0.113.7"},
		"what the caller wrote is not believed":       {"127.0.0.1:41000", []string{"192.0.2.1, 203.0.113.7"}, caddy, "203.0.113.7"},
		"two lines of the header are one list":        {"127.0.0.1:41000", []string{"192.0.2.1", "203.0.113.7"}, caddy, "203.0.113.7"},
		"through two proxies":                         {"127.0.0.1:41000", []string{"203.0.113.7, 10.1.2.3"}, chain, "203.0.113.7"},
		"not from a proxy: the header is not read":    {"198.51.100.4:41000", []string{"203.0.113.7"}, caddy, "198.51.100.4"},
		"from the proxy without the header":           {"127.0.0.1:41000", nil, caddy, "127.0.0.1"},
		"an entry with its port":                      {"127.0.0.1:41000", []string{"203.0.113.7:51234"}, caddy, "203.0.113.7"},
		"an IPv6 caller in brackets":                  {"127.0.0.1:41000", []string{"[2001:db8::7]"}, caddy, "2001:db8::7"},
		"an IPv6 caller with its port":                {"127.0.0.1:41000", []string{"[2001:db8::7]:443"}, caddy, "2001:db8::7"},
		"an IPv4 caller written as IPv6":              {"127.0.0.1:41000", []string{"::ffff:203.0.113.7"}, caddy, "203.0.113.7"},
		"the proxy's address written as IPv6":         {"[::ffff:127.0.0.1]:41000", []string{"203.0.113.7"}, caddy, "203.0.113.7"},
		"garbage left of the caller is never reached": {"127.0.0.1:41000", []string{"not an address, 203.0.113.7"}, caddy, "203.0.113.7"},
		"garbage from the proxy stops at the proxy":   {"127.0.0.1:41000", []string{"203.0.113.7, not an address"}, caddy, "127.0.0.1"},
		"garbage behind a second proxy stops there":   {"127.0.0.1:41000", []string{"nonsense, 10.1.2.3"}, chain, "10.1.2.3"},
		"every entry a proxy: the first of them":      {"127.0.0.1:41000", []string{"10.9.9.9, 10.1.2.3"}, chain, "10.9.9.9"},
		"an empty header":                             {"127.0.0.1:41000", []string{""}, caddy, "127.0.0.1"},
		"a connection whose address has no port":      {"127.0.0.1", []string{"203.0.113.7"}, caddy, "203.0.113.7"},
		"a connection whose address is no address":    {"pipe", []string{"203.0.113.7"}, caddy, "pipe"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/auth/signin", nil)
		r.RemoteAddr = c.remote
		for _, line := range c.header {
			r.Header.Add("X-Forwarded-For", line)
		}
		if got := callerAddress(r, c.proxies); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
