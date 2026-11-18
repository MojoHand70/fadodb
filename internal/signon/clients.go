package signon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/MojoHand70/fadodb/internal/store"
)

// A client known by the document it publishes (ADR-0071, item 6): its id
// is the address of a document, a Client ID Metadata Document
// (draft-ietf-oauth-client-id-metadata-document), that says its name and
// where its codes are sent. Claude names itself so. The server reads the
// document when a person is asked, over HTTPS alone, never from an
// address of the network it stands in, never past a redirect, at most
// 5 KB of it and within five seconds, and keeps nothing of it beyond the
// request.

// The limits of reading a document.
const (
	documentBytes = 5 << 10
	documentTime  = 5 * time.Second
	// documentsAtOnce bounds the documents read at once, so that a page
	// sent to many browsers does not make the server read without end.
	documentsAtOnce = 8
)

// DocumentOptions are what the reader of documents is made with. Both
// are for tests: Loopback lets it read from the machine itself, which
// the draft allows only to a server that is developed there, and Roots
// are the certificates it trusts in place of the system's.
type DocumentOptions struct {
	Loopback bool
	Roots    *x509.CertPool
}

// Documents reads the documents clients publish.
type Documents struct {
	client *http.Client
	slots  chan struct{}
}

// NewDocuments makes a reader of documents.
func NewDocuments(o DocumentOptions) *Documents {
	dialer := &net.Dialer{Timeout: documentTime, Control: func(_, address string, _ syscall.RawConn) error {
		// Asked of the address the connection is made to, after the name
		// was looked up, so that a name that points inside the network is
		// refused however it was answered.
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("the address %s is not one", address)
		}
		ip := ap.Addr().Unmap()
		if o.Loopback && ip.IsLoopback() {
			return nil
		}
		if special(ip) {
			return fmt.Errorf("the address %s is of the network itself, and no client's document is read from it", ip)
		}
		return nil
	}}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{RootCAs: o.Roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   documentTime,
		ResponseHeaderTimeout: documentTime,
		MaxIdleConns:          4,
		IdleConnTimeout:       time.Minute,
	}
	return &Documents{
		client: &http.Client{Transport: transport, Timeout: documentTime, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		slots: make(chan struct{}, documentsAtOnce),
	}
}

// special reports whether an address is one of the special-use ones of
// RFC 6890 and the registries after it: the machine, the private
// networks, the link, the shared and the documentation ones, multicast,
// and the translations that reach any of them.
func special(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, p := range specialPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

var specialPrefixes = func() []netip.Prefix {
	var list []netip.Prefix
	for _, text := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fec0::/10",
	} {
		list = append(list, netip.MustParsePrefix(text))
	}
	return list
}()

// CheckClientID refuses an id that is no address of a document: one
// over HTTPS, with a path other than /, no user, no query, no fragment
// and no . or .. in its path.
func CheckClientID(id string) error {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		strings.ContainsAny(id, "#?") || u.Path == "" || u.Path == "/" || len(id) > 2000 {
		return fmt.Errorf("%q is not the address of a client's document: an address over https with a path, no query and no fragment", id)
	}
	for _, part := range strings.Split(u.EscapedPath(), "/") {
		if part == "." || part == ".." || strings.EqualFold(part, "%2e") || strings.EqualFold(part, "%2e%2e") {
			return fmt.Errorf("%q is not the address of a client's document: a part of its path is one dot or two dots", id)
		}
	}
	return nil
}

// document is what the server reads of a client's document.
type document struct {
	ClientID             string           `json:"client_id"`
	ClientName           string           `json:"client_name"`
	RedirectURIs         []string         `json:"redirect_uris"`
	GrantTypes           []string         `json:"grant_types"`
	ResponseTypes        []string         `json:"response_types"`
	AuthMethod           *string          `json:"token_endpoint_auth_method"`
	AuthMethods          []string         `json:"token_endpoint_auth_methods_supported"`
	ClientSecret         *json.RawMessage `json:"client_secret"`
	ClientSecretExpiries *json.RawMessage `json:"client_secret_expires_at"`
}

// Client reads the document of a client and answers the client it says
// it is, for the door for agents: its name, and the addresses its codes
// are sent to that the server sends codes to. A document that is not
// read, or does not hold, answers an error that says why.
func (d *Documents) Client(ctx context.Context, id string) (*store.Client, error) {
	if err := CheckClientID(id); err != nil {
		return nil, err
	}
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, documentTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "FadoDB")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the client's document was not read: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the client's document answered %s; the server follows no redirect", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, documentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("the client's document was not read: %w", err)
	}
	if len(body) > documentBytes {
		return nil, errors.New("the client's document is longer than 5 KB, the most the server reads")
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("the client's document is not JSON: %w", err)
	}
	return doc.client(id)
}

// client checks a document and answers the client it names.
func (doc *document) client(id string) (*store.Client, error) {
	switch {
	case doc.ClientID != id:
		return nil, fmt.Errorf("the document names the client %q, not the address it is read from", doc.ClientID)
	case doc.ClientSecret != nil || doc.ClientSecretExpiries != nil:
		return nil, errors.New("the document holds a client secret, which a published document never does")
	case doc.AuthMethods != nil && !contains(doc.AuthMethods, "none"),
		doc.AuthMethods == nil && doc.AuthMethod != nil && *doc.AuthMethod != "none":
		return nil, errors.New("the client proves itself with a key or a secret at the token address; the server takes clients that prove each code with PKCE alone")
	case doc.GrantTypes != nil && !contains(doc.GrantTypes, "authorization_code"):
		return nil, errors.New("the client asks for no code, the one way the server gives passes")
	case doc.ResponseTypes != nil && !contains(doc.ResponseTypes, "code"):
		return nil, errors.New("the client takes no code, the one answer the server gives")
	}
	c := &store.Client{ID: id, Address: store.AddressAgents}
	c.Name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(doc.ClientName))
	if c.Name == "" {
		c.Name = c.Host()
	}
	if len([]rune(c.Name)) > 100 {
		c.Name = string([]rune(c.Name)[:100])
	}
	for _, uri := range doc.RedirectURIs {
		u, err := url.Parse(uri)
		if err == nil && u.Host != "" && u.User == nil && u.Fragment == "" && !strings.Contains(uri, "#") && len(uri) <= 2000 &&
			(u.Scheme == "https" || u.Scheme == "http" && loopbackHost(u.Hostname())) {
			c.RedirectURIs = append(c.RedirectURIs, uri)
		}
	}
	if len(c.RedirectURIs) == 0 {
		return nil, errors.New("the document names no address over https, nor over http on the client's machine, to send its codes to")
	}
	return c, nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
