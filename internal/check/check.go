// Package check performs one HTTP(S) probe of a site and explains the outcome.
package check

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

const (
	DefaultUserAgent = "ronda/1 (uptime-check)"
	maxBody          = 1 << 20 // keyword search looks at the first MiB only
	maxRedirects     = 5
)

type noRedirectKey struct{}

// blockedMarker ends BlockedError's text. The resolver flattens dial errors into plain text, so
// isBlockedMsg finds a blocked address again by this marker. It is machine-matched, hence never translated.
const blockedMarker = "(private/internal network)"

// BlockedError is returned when a target resolves to an internal address and the
// site has not opted in with allow_private.
type BlockedError struct{ IP netip.Addr }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("address %s is blocked "+blockedMarker, e.IP)
}

// Checker probes sites. It is safe for concurrent use.
type Checker struct{ strict, lax *http.Client }

func New() *Checker { return &Checker{strict: newClient(false), lax: newClient(true)} }

func newClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, Control: guard(allowPrivate)}
	tr := &http.Transport{
		Proxy:                  nil, // never inherit HTTP(S)_PROXY: we measure the direct path
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		DisableKeepAlives:      true, // every check pays for DNS, connect and TLS, like a new visitor
		TLSHandshakeTimeout:    15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.Context().Value(noRedirectKey{}) != nil {
				return http.ErrUseLastResponse
			}
			if len(via) > maxRedirects {
				return errTooManyRedirects
			}
			return nil
		},
	}
}

// guard rejects connections to internal addresses. It runs on the address that is
// about to be dialled (after DNS), so it also covers DNS rebinding and redirects.
func guard(allow bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		if allow {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		if blockedIP(ip) {
			return &BlockedError{IP: ip.Unmap()}
		}
		return nil
	}
}

var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),          // IPv4-compatible (deprecated), includes ::1 and ::
	netip.MustParsePrefix("100::/64"),       // discard-only
	netip.MustParsePrefix("fec0::/10"),      // site-local (deprecated, but still routed inside some networks)
	netip.MustParsePrefix("2001::/32"),      // Teredo: embeds an obfuscated IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
}

var (
	nat64  = netip.MustParsePrefix("64:ff9b::/96") // well-known NAT64: the last 32 bits are an IPv4 address
	sixto4 = netip.MustParsePrefix("2002::/16")    // 6to4: bits 16..48 are an IPv4 address
)

// embeddedV4 extracts the IPv4 address a translation prefix carries. A NAT64 gateway turns
// 64:ff9b::7f00:1 into a connection to 127.0.0.1, so judging only the IPv6 form would let it through.
// Blocking the whole prefix would instead break IPv6-only hosts behind DNS64, where every public
// IPv4 site resolves into it; so the embedded address is judged by the IPv4 rules.
func embeddedV4(ip netip.Addr) (netip.Addr, bool) {
	b := ip.As16()
	switch {
	case nat64.Contains(ip):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case sixto4.Contains(ip):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if v4, ok := embeddedV4(ip); ok {
		return blockedIP(v4)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range extraBlocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Check probes s once. It never panics and always returns a Result; ctx cancels it.
func (c *Checker) Check(ctx context.Context, s model.Site) model.Result {
	switch s.Type {
	case "tcp":
		return c.checkTCP(ctx, s)
	case "dns":
		return c.checkDNS(ctx, s)
	case "push":
		// Nothing is fetched for a push site; the monitor waits for its pings. Never try push:// over HTTP.
		return model.Result{Time: time.Now().UnixMilli(), Kind: model.KindError, Error: "push site is not checked",
			Hint: i18n.T("A push site waits for pings from its job and is not checked from here.")}
	}
	start := time.Now()
	res := model.Result{Time: start.UnixMilli()}

	timeout := time.Duration(s.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if s.NoRedirect {
		ctx = context.WithValue(ctx, noRedirectKey{}, true)
	}
	tr := &tracer{}
	ctx = httptrace.WithClientTrace(ctx, tr.hooks())

	method := s.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL, nil)
	if err != nil {
		res.Kind, res.Error, res.Hint = model.KindError, "invalid url", i18n.T("The URL cannot be used to make a request.")
		return res
	}
	ua := s.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

	client := c.strict
	if s.AllowPrivate {
		client = c.lax
	}

	finish := func() phases {
		res.LatencyMs = max(1, int(time.Since(start).Milliseconds()))
		ph := tr.snapshot()
		ph.fill(&res)
		return ph
	}

	resp, err := client.Do(req)
	if err != nil {
		ph := finish()
		res.Kind, res.Hint, res.Error = classifyError(err, ph, timeout)
		return res
	}
	defer resp.Body.Close()

	res.Status = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		res.CertNotAfter = resp.TLS.PeerCertificates[0].NotAfter.UnixMilli()
	}

	accepted := statusAccepted(s.ExpectStatus, resp.StatusCode)
	var body []byte
	if accepted && s.Keyword != "" && method == http.MethodGet {
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil {
			ph := finish()
			res.Kind, res.Hint, res.Error = classifyError(err, ph, timeout)
			return res
		}
	}
	finish()

	switch {
	case !accepted:
		res.Kind, res.Hint = classifyStatus(resp.StatusCode)
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	case s.Keyword != "" && method == http.MethodGet &&
		!bytes.Contains(bytes.ToLower(body), []byte(strings.ToLower(s.Keyword))):
		res.Kind = model.KindKeyword
		res.Error = "keyword not found"
		res.Hint = i18n.T("The page opened (HTTP %d) but the text %q is not in the first 1 MB: the application may be failing or the page changed.",
			resp.StatusCode, s.Keyword)
	default:
		res.OK, res.Kind = true, model.KindOK
	}
	return res
}

func statusAccepted(expect []int, code int) bool {
	if len(expect) == 0 {
		return code >= 200 && code < 400
	}
	for _, e := range expect {
		if e == code {
			return true
		}
	}
	return false
}

// phases records when each stage of the last request hop happened.
type phases struct {
	hopStart, dnsStart, dnsDone, connStart, connDone, tlsStart, tlsDone, firstByte time.Time
}

func dur(a, b time.Time) int {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return 0
	}
	return int(b.Sub(a).Milliseconds())
}

func (p phases) fill(r *model.Result) {
	r.DNSMs = dur(p.dnsStart, p.dnsDone)
	r.ConnectMs = dur(p.connStart, p.connDone)
	r.TLSMs = dur(p.tlsStart, p.tlsDone)
	base := p.hopStart
	for _, t := range []time.Time{p.connDone, p.tlsDone} {
		if t.After(base) {
			base = t
		}
	}
	r.TTFBMs = dur(base, p.firstByte)
}

type tracer struct {
	mu sync.Mutex
	p  phases
}

func (t *tracer) set(f func(p *phases)) {
	t.mu.Lock()
	f(&t.p)
	t.mu.Unlock()
}

func (t *tracer) snapshot() phases {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.p
}

// hooks restarts the record for every connection, so after redirects the numbers
// describe the final hop.
func (t *tracer) hooks() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn:  func(string) { t.set(func(p *phases) { *p = phases{hopStart: time.Now()} }) },
		DNSStart: func(httptrace.DNSStartInfo) { t.set(func(p *phases) { p.dnsStart = time.Now() }) },
		DNSDone:  func(httptrace.DNSDoneInfo) { t.set(func(p *phases) { p.dnsDone = time.Now() }) },
		ConnectStart: func(_, _ string) {
			t.set(func(p *phases) {
				if p.connStart.IsZero() {
					p.connStart = time.Now()
				}
			})
		},
		ConnectDone: func(_, _ string, err error) {
			if err == nil {
				t.set(func(p *phases) { p.connDone = time.Now() })
			}
		},
		TLSHandshakeStart: func() { t.set(func(p *phases) { p.tlsStart = time.Now() }) },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				t.set(func(p *phases) { p.tlsDone = time.Now() })
			}
		},
		GotFirstResponseByte: func() { t.set(func(p *phases) { p.firstByte = time.Now() }) },
	}
}
