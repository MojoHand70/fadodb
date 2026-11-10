// Package outbound lets a FadoDB database send the HTTP requests of
// declared actions (ADR-0045, item 7).
//
// A database sends nothing by itself. A class definition is data, and an
// agent may have written it; whether the process may reach the network,
// and where, is its host program's decision. The program states it by
// opening the database with what Allow returns:
//
//	out, err := outbound.Allow(outbound.Options{
//	    Hosts:       []string{"hooks.partner.example"},
//	    Connections: map[string][]string{"slack": {"slack.com"}},
//	})
//	db, err := fado.Open(path, fado.Options{Outbound: out, Secrets: secrets})
//
// The rules are these. A request goes only to a host that is listed. A
// request of a definition that names a connection goes only to the hosts
// of that connection, so the connection's secrets reach nobody else; a
// request without a connection, which reads no secret, goes only to
// Hosts. A host is called over https on port 443, unless its entry says
// http or names a port. A host listed by its own name may be inside the
// program's network; one that only a suffix entry admits is refused when
// it resolves to a loopback, private or link-local address. A redirect is
// not followed: it is the reply. A call has ten seconds and its reply one
// megabyte, unless the options say otherwise. No proxy of the environment
// is used, since a proxy would decide where a request goes.
//
// This is the only package of the module that makes an HTTP client. A
// program that does not import it links none.
package outbound

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The defaults of Options.
const (
	DefaultTimeout        = 10 * time.Second
	DefaultMaxReply int64 = 1 << 20
)

// Options say where a database may send requests.
type Options struct {
	// Hosts are the hosts a request without a connection may go to: one of
	// a definition that names none, and one of http.call. Such a request
	// carries no secret.
	Hosts []string
	// Connections are the connections a definition may name, each with the
	// hosts its requests, and so its secrets, may go to.
	Connections map[string][]string
	// Timeout is how long one call may take, the reply included; 0 is
	// DefaultTimeout.
	Timeout time.Duration
	// MaxReply is how many bytes of a reply are read; a longer reply is
	// refused. 0 is DefaultMaxReply.
	MaxReply int64
}

// An entry of Hosts or of a connection is one of:
//
//	slack.com               that host, over https, on port 443
//	.slack.com              every host under slack.com, not slack.com itself
//	books.internal:8443     that host on that port
//	http://localhost:8080   that host over http instead of https
//
// An entry names no path: a host is allowed or it is not.
type entry struct {
	scheme, host, port string
	suffix             bool
}

func parseEntry(s string) (entry, error) {
	e := entry{scheme: "https"}
	rest := strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(rest, "http://"):
		e.scheme, rest = "http", rest[len("http://"):]
	case strings.HasPrefix(rest, "https://"):
		rest = rest[len("https://"):]
	}
	if rest == "" || strings.ContainsAny(rest, "/?#@* ") {
		return e, fmt.Errorf("outbound: %q is not a host: write a name, .suffix, name:port or http://name:port", s)
	}
	if strings.HasPrefix(rest, ".") {
		e.suffix = true
	}
	host, port := rest, ""
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host, port = h, p
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return e, fmt.Errorf("outbound: %q has no port between 1 and 65535", s)
		}
	} else if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
		host = rest[1 : len(rest)-1]
	} else if strings.Count(rest, ":") == 1 {
		return e, fmt.Errorf("outbound: %q has no port between 1 and 65535", s)
	}
	e.host, e.port = strings.TrimSuffix(strings.ToLower(host), "."), port
	if e.host == "" || e.host == "." || e.suffix && len(e.host) < 2 {
		return e, fmt.Errorf("outbound: %q names no host", s)
	}
	return e, nil
}

// matches reports whether the entry admits the address.
func (e entry) matches(u *url.URL) bool {
	port, usual := u.Port(), map[string]string{"https": "443", "http": "80"}[u.Scheme]
	if port == "" {
		port = usual
	}
	want := e.port
	if want == "" {
		want = map[string]string{"https": "443", "http": "80"}[e.scheme]
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if u.Scheme != e.scheme || port != want {
		return false
	}
	if e.suffix {
		return strings.HasSuffix(host, e.host)
	}
	return host == e.host
}

// Client sends requests to the hosts its options list. It is what Allow
// returns, as a fado.Outbound.
type Client struct {
	hosts       []entry
	connections map[string][]entry
	timeout     time.Duration
	maxReply    int64
	// named calls a host listed by its own name, wherever it is; public
	// calls one that only a suffix admits, and reaches public addresses
	// only.
	named, public *http.Client
	// lookup resolves a host; the tests set it.
	lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Allow makes the sender of a database's requests from the hosts a
// program allows. The error is an entry that is no host.
func Allow(o Options) (fado.Outbound, error) {
	c, err := newClient(o)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func newClient(o Options) (*Client, error) {
	c := &Client{connections: map[string][]entry{}, timeout: o.Timeout, maxReply: o.MaxReply, lookup: net.DefaultResolver.LookupIPAddr}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.maxReply <= 0 {
		c.maxReply = DefaultMaxReply
	}
	list := func(hosts []string) ([]entry, error) {
		out := make([]entry, 0, len(hosts))
		for _, h := range hosts {
			e, err := parseEntry(h)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
	var err error
	if c.hosts, err = list(o.Hosts); err != nil {
		return nil, err
	}
	for name, hosts := range o.Connections {
		if name == "" {
			return nil, errors.New("outbound: a connection has a name")
		}
		if c.connections[name], err = list(hosts); err != nil {
			return nil, fmt.Errorf("connection %s: %w", name, err)
		}
	}
	c.named, c.public = c.client(false), c.client(true)
	return c, nil
}

// client makes an HTTP client that follows no redirect and uses no proxy.
// With publicOnly it connects to public addresses only, judged on the
// address it connects to and not on a lookup made before, so that a name
// cannot answer one thing when it is checked and another when it is
// called.
func (c *Client) client(publicOnly bool) *http.Client {
	dialer := &net.Dialer{Timeout: c.timeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := c.lookup(ctx, host)
				if err != nil {
					return nil, err
				}
				last := fmt.Errorf("the host %s has no address", host)
				for _, ip := range ips {
					if publicOnly && !isPublic(ip.IP) {
						last = &store.Error{Code: store.CodeHostNotAllowed, Message: "the host " + host + " resolves to " + ip.IP.String() + ", an address inside the network, and only a suffix admits it",
							Hint: "a host inside the network is listed by its own name"}
						continue
					}
					conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
					if err == nil {
						return conn, nil
					}
					last = err
				}
				return nil, last
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   c.timeout,
			ResponseHeaderTimeout: c.timeout,
		},
	}
}

// isPublic reports whether an address is outside the ranges a program's
// own network uses: loopback, private, link-local, shared, unspecified
// and multicast.
func isPublic(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return false // 100.64.0.0/10, shared address space
	}
	return true
}

// admit finds the entry that lets a request of the connection, "" for
// none, go to the address: an entry by the host's own name before one by
// suffix. An empty address asks about the connection alone.
func (c *Client) admit(connection, raw string) (*url.URL, entry, error) {
	list, where := c.hosts, "this process may call"
	if connection != "" {
		hosts, known := c.connections[connection]
		if !known {
			return nil, entry{}, &store.Error{Code: store.CodeUnknownConnection, Message: "the host program names no connection " + strconv.Quote(connection),
				Hint: "it lists the connections a definition may use, each with its hosts, when it allows outbound calls"}
		}
		list, where = hosts, "of the connection "+connection
	}
	if raw == "" {
		return nil, entry{}, nil
	}
	refuse := func(message, hint string) (*url.URL, entry, error) {
		return nil, entry{}, &store.Error{Code: store.CodeHostNotAllowed, Message: message, Hint: hint}
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Hostname() == "":
		return refuse("the address names no host", "")
	case u.Scheme != "https" && u.Scheme != "http":
		return refuse("a request goes over https, or over http to a host listed with it", "")
	case u.User != nil:
		return refuse("an address carries no user and no password", "a credential belongs in a header, as a secret")
	}
	var found *entry
	for i := range list {
		if e := &list[i]; e.matches(u) && (found == nil || found.suffix && !e.suffix) {
			found = e
		}
	}
	if found == nil {
		host := u.Host
		for i := range list {
			// The host is listed and the scheme or the port is not: say which.
			if e := list[i]; e.matches(&url.URL{Scheme: e.scheme, Host: net.JoinHostPort(u.Hostname(), portOf(e))}) {
				return refuse("the host "+host+" is one "+where+" over "+e.scheme+" on port "+portOf(e)+", not over "+u.Scheme+" on port "+portOf(entry{scheme: u.Scheme, port: u.Port()}), "")
			}
		}
		return refuse("the host "+host+" is not one "+where, "the host program lists the hosts a request may go to when it allows outbound calls")
	}
	return u, *found, nil
}

func portOf(e entry) string {
	if e.port != "" {
		return e.port
	}
	if e.scheme == "http" {
		return "80"
	}
	return "443"
}

// Check says whether a request of a definition with the connection may go
// to the address.
func (c *Client) Check(connection, address string) error {
	_, _, err := c.admit(connection, address)
	return err
}

// Send sends the request, when its host is one that may be called, and
// returns the reply whatever its status. An *fado.Error is a refusal to
// send; any other error is no reply, and names the host and never the
// rest of the address.
func (c *Client) Send(ctx context.Context, req *fado.OutboundRequest) (*fado.OutboundReply, error) {
	u, admitted, err := c.admit(req.Connection, req.URL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), body)
	if err != nil {
		return nil, &store.Error{Code: store.CodeBadValue, Message: "the request does not form: " + unwrap(err).Error()}
	}
	for _, h := range req.Headers {
		// The engine's own headers as it writes them; any other in the
		// transport's capitals, where it looks for the ones it knows.
		if strings.HasPrefix(h.Name, "FadoDB-") {
			hreq.Header[h.Name] = []string{h.Value}
		} else {
			hreq.Header.Set(h.Name, h.Value)
		}
	}
	client := c.public
	if !admitted.suffix {
		client = c.named
	}
	resp, err := client.Do(hreq)
	if err != nil {
		var refused *store.Error
		switch {
		case errors.As(err, &refused):
			return nil, refused
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("no reply within %s", c.timeout)
		}
		return nil, unwrap(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxReply+1))
	switch {
	case err != nil && errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("no whole reply within %s", c.timeout)
	case err != nil:
		return nil, unwrap(err)
	case int64(len(raw)) > c.maxReply:
		return nil, &store.Error{Code: store.CodeReplyTooLarge, Message: fmt.Sprintf("the receiver answered %d with more than the %d bytes this process reads", resp.StatusCode, c.maxReply)}
	}
	return &fado.OutboundReply{Status: resp.StatusCode, Body: raw, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}, nil
}

// unwrap takes the address out of a transport's error: an address may
// carry a secret, and the caller names the host.
func unwrap(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// retryAfter reads a Retry-After header: seconds, or a date.
func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(value, 10, 64); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(min(secs, 366*24*3600)) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}
