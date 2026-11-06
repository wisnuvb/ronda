package check

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
)

// errTooManyRedirects is only ever classified (see classifyError), never shown as is.
var errTooManyRedirects = errors.New(i18n.N("too many redirects"))

// classifyError turns a transport error into a kind, an explanation with a next
// step, and a short message for logs. ph says how far the request got. The hint is for people and
// follows the configured language; the short message is for logs and stays English.
func classifyError(err error, ph phases, timeout time.Duration) (kind, hint, short string) {
	var (
		blocked *BlockedError
		inv     x509.CertificateInvalidError
		host    x509.HostnameError
		unk     x509.UnknownAuthorityError
		dnsErr  *net.DNSError
		netErr  net.Error
	)

	switch {
	case errors.As(err, &blocked):
		return model.KindBlocked,
			i18n.T("The destination address (%s) is on a private/internal network, so it was blocked. If that is intentional, enable allow_private for this site.", blocked.IP.String()),
			"private address blocked"

	case errors.Is(err, errTooManyRedirects):
		return model.KindRedirectLoop,
			i18n.T("More than %d consecutive redirects: probably a redirect loop (e.g. http↔https or www↔non-www pointing at each other).", maxRedirects),
			"too many redirects"

	case errors.As(err, &inv):
		if inv.Reason == x509.Expired && inv.Cert != nil {
			if time.Now().Before(inv.Cert.NotBefore) {
				return model.KindTLS, i18n.T("The TLS certificate is not valid yet (starts %s). Check the server clock.", model.Stamp(inv.Cert.NotBefore.UnixMilli(), "2006-01-02 15:04")), "certificate not yet valid"
			}
			return model.KindTLSExpired,
				i18n.T("The TLS certificate expired on %s. Renew it (e.g. certbot renew) and reload the web server.", model.Stamp(inv.Cert.NotAfter.UnixMilli(), "2006-01-02 15:04")),
				"certificate expired"
		}
		return model.KindTLS, i18n.T("The TLS certificate was rejected: %s", inv.Error()), "certificate rejected"

	case errors.As(err, &host):
		return model.KindTLSHostname,
			i18n.T("The TLS certificate does not match the domain name. Check that the vhost uses the right certificate. (%s)", host.Error()),
			"certificate name mismatch"

	case errors.As(err, &unk):
		return model.KindTLSUntrusted,
			i18n.T("The TLS certificate is not trusted (self-signed, unknown issuer, or the intermediate certificate chain is incomplete on the server)."),
			"certificate not trusted"

	case errors.As(err, &dnsErr):
		switch {
		case dnsErr.IsNotFound:
			return model.KindDNS, i18n.T("The domain name was not found (NXDOMAIN). Check the A/AAAA records in DNS, or whether the domain has expired."), "domain not found"
		case dnsErr.IsTimeout:
			return model.KindDNS, i18n.T("The DNS server did not answer in time. Check the domain's nameservers or the monitor's DNS connectivity."), "DNS timeout"
		}
		return model.KindDNS, i18n.T("DNS resolution failed (%s). Check the domain's nameservers.", dnsErr.Err), "DNS failed"

	case errors.Is(err, syscall.ECONNREFUSED):
		return model.KindRefused,
			i18n.T("Connection refused: the server is reachable but nothing is listening on that port (web server down or wrong port)."),
			"connection refused"

	case errors.Is(err, syscall.ECONNRESET):
		return model.KindReset, i18n.T("The connection was forcibly closed by the server (reset). The web server or a firewall closed it."), "connection reset"

	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return model.KindUnreachable, i18n.T("The server address is unreachable from the monitor (routing or firewall problem)."), "unreachable"

	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		secs := int(timeout.Seconds())
		switch {
		case ph.connDone.IsZero():
			return model.KindTimeoutConnect,
				i18n.T("Could not connect within %d s: the server is not answering (down, a firewall dropping packets, or the network is cut).", secs),
				"connect timeout"
		case !ph.tlsStart.IsZero() && ph.tlsDone.IsZero():
			return model.KindTimeoutTLS,
				i18n.T("Connected, but the TLS handshake did not finish within %d s. The web server is stuck or overloaded.", secs),
				"TLS timeout"
		}
		return model.KindTimeoutResponse,
			i18n.T("Connected, but no response came within %d s: the application/backend is stuck or overloaded.", secs),
			"response timeout"

	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return model.KindReset, i18n.T("The server closed the connection without sending a complete response (process crash or misconfiguration)."), "connection closed by server"
	}

	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		msg = ue.Err.Error()
	}
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	if strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") {
		return model.KindTLS, i18n.T("TLS handshake failed: %s", msg), msg
	}
	return model.KindError, i18n.T("Request failed: %s", msg), msg
}

// classifyStatus explains an unexpected HTTP status.
func classifyStatus(code int) (kind, hint string) {
	switch code {
	case 520:
		return model.KindCloudflare, i18n.T("Cloudflare 520: the origin sent an invalid response or dropped the connection. Check the web server/application logs.")
	case 521:
		return model.KindCloudflare, i18n.T("Cloudflare 521: the origin refused the connection from Cloudflare (web server down, or port 80/443 closed or blocked by a firewall).")
	case 522:
		return model.KindCloudflare, i18n.T("Cloudflare 522: Cloudflare's connection to the origin timed out (server down, overloaded, or a firewall blocking Cloudflare's IPs).")
	case 523:
		return model.KindCloudflare, i18n.T("Cloudflare 523: the origin is unreachable (wrong origin IP in DNS or a broken route).")
	case 524:
		return model.KindCloudflare, i18n.T("Cloudflare 524: the origin accepted the connection but did not reply in time (application stuck or too slow).")
	case 525:
		return model.KindCloudflare, i18n.T("Cloudflare 525: the SSL handshake between Cloudflare and the origin failed. Check the origin's certificate and TLS configuration.")
	case 526:
		return model.KindCloudflare, i18n.T("Cloudflare 526: the origin's SSL certificate is invalid (Full strict mode). Renew or fix the origin certificate.")
	case 530:
		return model.KindCloudflare, i18n.T("Cloudflare 530 (error 1xxx): usually a DNS/zone problem or an unreachable origin. See the 1xxx error code on the Cloudflare page.")
	case 502:
		return model.KindHTTP5xx, i18n.T("502 Bad Gateway: the reverse proxy (e.g. nginx) is up but the backend is not answering or has crashed.")
	case 503:
		return model.KindHTTP5xx, i18n.T("503 Service Unavailable: the service is overloaded, under maintenance, or the backend is down.")
	case 504:
		return model.KindHTTP5xx, i18n.T("504 Gateway Timeout: the backend took too long to answer the reverse proxy.")
	case 401:
		return model.KindHTTP4xx, i18n.T("401: the page requires a login. Add expect_status 401 if that is what you expect, or monitor a public path.")
	case 403:
		return model.KindHTTP4xx, i18n.T("403: access denied. A WAF/Cloudflare may be blocking the monitor; allow the monitor's IP or set a custom user_agent.")
	case 404:
		return model.KindHTTP4xx, i18n.T("404: page not found. Check the path in the URL.")
	case 429:
		return model.KindHTTP4xx, i18n.T("429: the monitor is being rate limited. Increase the check interval or allow the monitor's IP.")
	}
	switch {
	case code >= 500:
		return model.KindHTTP5xx, i18n.T("The server returned HTTP %d: an error on the application side. Check the application logs.", code)
	case code >= 400:
		return model.KindHTTP4xx, i18n.T("The server returned HTTP %d. Check the URL and access settings.", code)
	}
	return model.KindStatus, i18n.T("HTTP %d is not what was expected. Adjust expect_status if this is normal.", code)
}
