package check

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

func timeoutOf(s model.Site) time.Duration {
	if s.TimeoutSec > 0 {
		return time.Duration(s.TimeoutSec) * time.Second
	}
	return 10 * time.Second
}

// checkTCP only opens the connection and closes it again: the port accepting is the whole test.
// It goes through the same guard as web checks; otherwise the dashboard could be used to probe
// the ports of whatever network the monitor sits in.
func (c *Checker) checkTCP(ctx context.Context, s model.Site) model.Result {
	start := time.Now()
	res := model.Result{Time: start.UnixMilli()}
	timeout := timeoutOf(s)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" {
		res.Kind, res.Error, res.Hint = model.KindError, "invalid address", i18n.T("A tcp address must look like tcp://host:port.")
		return res
	}
	d := &net.Dialer{Control: guard(s.AllowPrivate)}
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	res.LatencyMs = max(1, int(time.Since(start).Milliseconds()))
	if err != nil {
		res.Kind, res.Hint, res.Error = classifyError(err, phases{}, timeout)
		if res.Kind == model.KindRefused {
			res.Hint = i18n.T("Connection refused: the server is reachable but nothing is listening on that port (service down or wrong port).")
		}
		return res
	}
	_ = conn.Close()
	res.OK, res.Kind, res.ConnectMs = true, model.KindOK, res.LatencyMs
	return res
}

const maxAnswer = 300

func isBlockedMsg(err error) bool {
	// The resolver flattens dial errors into text, so the typed error does not survive.
	return err != nil && strings.Contains(err.Error(), blockedMarker)
}

// checkDNS resolves one record type and, if a keyword is set, requires it among the answers.
func (c *Checker) checkDNS(ctx context.Context, s model.Site) model.Result {
	start := time.Now()
	res := model.Result{Time: start.UnixMilli()}
	timeout := timeoutOf(s)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name := strings.TrimPrefix(s.URL, "dns://")
	fqdn := name + "." // absolute, so the resolver never appends a search domain
	r := &net.Resolver{PreferGo: true}
	if s.DNSServer != "" {
		server := s.DNSServer
		d := &net.Dialer{Control: guard(s.AllowPrivate)}
		r.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, server)
		}
	}

	var answers []string
	var err error
	switch s.DNSType {
	case "AAAA":
		var ips []net.IP
		if ips, err = r.LookupIP(ctx, "ip6", fqdn); err == nil || len(ips) > 0 {
			for _, ip := range ips {
				answers = append(answers, ip.String())
			}
		}
	case "MX":
		var mx []*net.MX
		mx, err = r.LookupMX(ctx, fqdn)
		for _, m := range mx {
			answers = append(answers, fmt.Sprintf("%d %s", m.Pref, strings.TrimSuffix(m.Host, ".")))
		}
	case "TXT":
		answers, err = r.LookupTXT(ctx, fqdn)
	case "NS":
		var ns []*net.NS
		ns, err = r.LookupNS(ctx, fqdn)
		for _, n := range ns {
			answers = append(answers, strings.TrimSuffix(n.Host, "."))
		}
	default: // A
		var ips []net.IP
		if ips, err = r.LookupIP(ctx, "ip4", fqdn); err == nil || len(ips) > 0 {
			for _, ip := range ips {
				answers = append(answers, ip.String())
			}
		}
	}
	res.LatencyMs = max(1, int(time.Since(start).Milliseconds()))
	res.DNSMs = res.LatencyMs

	if len(answers) == 0 {
		if err == nil {
			err = &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
		}
		res.Kind, res.Hint, res.Error = classifyDNSError(err, s, timeout)
		return res
	}
	slices.Sort(answers)
	res.Answer = strings.Join(answers, ", ")
	if len(res.Answer) > maxAnswer {
		res.Answer = res.Answer[:maxAnswer] + "…"
	}
	if s.Keyword != "" && !strings.Contains(strings.ToLower(strings.Join(answers, "\n")), strings.ToLower(s.Keyword)) {
		res.Kind, res.Error = model.KindKeyword, "unexpected answer"
		res.Hint = i18n.T("The %s record of %s answered %q, but %q is not in it: the record changed, points to the wrong place, or has not propagated yet.",
			s.DNSType, name, res.Answer, s.Keyword)
		return res
	}
	res.OK, res.Kind = true, model.KindOK
	return res
}

func classifyDNSError(err error, s model.Site, timeout time.Duration) (kind, hint, short string) {
	name := strings.TrimPrefix(s.URL, "dns://")
	if isBlockedMsg(err) || errors.As(err, new(*BlockedError)) {
		return model.KindBlocked,
			i18n.T("The DNS server (%s) is on a private/internal network, so it was blocked. If that is intentional, enable allow_private for this site.", s.DNSServer),
			"private address blocked"
	}
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return model.KindDNS,
			i18n.T("There is no %s record for %s: the name does not exist (NXDOMAIN) or has no record of that type. Check the domain's DNS settings.", s.DNSType, name),
			"record not found"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &dnsErr) && dnsErr.IsTimeout):
		secs := int(timeout.Seconds())
		if s.DNSServer != "" {
			return model.KindDNS, i18n.T("The DNS server %s did not answer within %d s: the server is down, blocked by a firewall, or the network is cut.", s.DNSServer, secs), "DNS timeout"
		}
		return model.KindDNS, i18n.T("The DNS server did not answer within %d s: the server is down, blocked by a firewall, or the network is cut.", secs), "DNS timeout"
	case errors.As(err, &dnsErr):
		return model.KindDNS, i18n.T("The DNS server failed to answer (%s). Check the domain's nameservers.", dnsErr.Err), "DNS failed"
	}
	return classifyError(err, phases{}, timeout)
}
