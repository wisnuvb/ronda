// Package sitespec holds the rules for what a monitored site may look like: its type from the URL, the defaults,
// and every limit. Both the single-tenant registry and a multi-tenant service validate through Normalize, so a
// site means the same thing wherever it is stored.
package sitespec

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/schedule"
)

// invalid wraps an already translated message in the error type that maps to HTTP 400.
func invalid(msg string) error { return model.Invalid(msg) }

// MaxMaintenance is how far ahead a maintenance window may end.
const MaxMaintenance = 30 * 24 * time.Hour

// Site types, from the URL scheme.
const (
	TypeHTTP = "http"
	TypeTCP  = "tcp"
	TypeDNS  = "dns"
	TypePush = "push"
)

// MaxPushInterval is the longest period a push site may expect between pings: 31 days, so a monthly job fits.
const MaxPushInterval = 31 * 24 * 3600

var dnsTypes = []string{"A", "AAAA", "MX", "TXT", "NS"}

func inferType(raw string) string {
	switch l := strings.ToLower(raw); {
	case strings.HasPrefix(l, "tcp://"):
		return TypeTCP
	case strings.HasPrefix(l, "dns://"):
		return TypeDNS
	case strings.HasPrefix(l, "push://"):
		return TypePush
	}
	return TypeHTTP
}

// validHost accepts an IP address or a DNS name (letters, digits, hyphens and underscores per label).
func validHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return validName(h)
}

func validName(h string) bool {
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

// normalizeServer turns "1.1.1.1", "dns.example", "[::1]:5353" into host:port, defaulting to port 53.
func normalizeServer(raw string) (string, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		host, port = strings.Trim(raw, "[]"), "53"
	}
	n, perr := strconv.Atoi(port)
	if !validHost(host) || perr != nil || n < 1 || n > 65535 {
		return "", invalid(i18n.T("dns_server must be a valid host or host:port"))
	}
	return net.JoinHostPort(host, port), nil
}

// Normalize applies defaults and validates s in place.
func Normalize(s *model.Site) error {
	s.Name = strings.TrimSpace(s.Name)
	s.URL = strings.TrimSpace(s.URL)
	s.Type = strings.ToLower(strings.TrimSpace(s.Type))
	if s.Type == TypePush && s.URL == "" {
		s.URL = "push://new" // a push site's address is only a label; Add replaces it with push://<id>
	}
	if s.URL == "" {
		return invalid(i18n.T("invalid url"))
	}
	if len(s.URL) > 2048 {
		return invalid(i18n.T("url is too long"))
	}

	inferred := inferType(s.URL)
	if s.Type == "" {
		s.Type = inferred
	}
	switch s.Type {
	case TypeHTTP, TypeTCP, TypeDNS, TypePush:
	default:
		return invalid(i18n.T("type must be http, tcp, dns or push"))
	}
	if s.Type != inferred {
		return invalid(i18n.T("url does not match type %s", s.Type))
	}

	u, err := url.Parse(s.URL)
	if err != nil {
		return invalid(i18n.T("invalid url"))
	}
	if u.User != nil {
		return invalid(i18n.T("url must not contain user:password"))
	}

	var defaultName string
	switch s.Type {
	case TypeHTTP:
		if u.Scheme != "http" && u.Scheme != "https" {
			return invalid(i18n.T("url must start with http:// or https://"))
		}
		if u.Hostname() == "" {
			return invalid(i18n.T("url has no host name"))
		}
		defaultName = u.Hostname()

	case TypeTCP:
		port, perr := strconv.Atoi(u.Port())
		if u.Hostname() == "" || u.Port() == "" || perr != nil || port < 1 || port > 65535 {
			return invalid(i18n.T("a tcp url must look like tcp://host:port, e.g. tcp://db.example.com:5432"))
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || !validHost(u.Hostname()) {
			return invalid(i18n.T("a tcp url may contain only a valid host and a port"))
		}
		s.URL = "tcp://" + u.Host
		defaultName = u.Host
		if s.Keyword != "" {
			return invalid(i18n.T("keyword does not apply to type tcp"))
		}

	case TypePush:
		if !validName(u.Hostname()) || u.Port() != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return invalid(i18n.T("a push url must look like push://label"))
		}
		s.URL = "push://" + u.Hostname()
		defaultName = i18n.T("Scheduled job")
		if s.Keyword != "" {
			return invalid(i18n.T("keyword does not apply to type push"))
		}

	case TypeDNS:
		name := strings.TrimSuffix(u.Hostname(), ".")
		if name == "" || u.Port() != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return invalid(i18n.T("a dns url must look like dns://name, e.g. dns://example.com"))
		}
		if _, ipErr := netip.ParseAddr(name); ipErr == nil || !validName(name) {
			return invalid(i18n.T("a dns url must contain a valid domain name, not an IP address"))
		}
		s.URL = "dns://" + name
		s.DNSType = strings.ToUpper(strings.TrimSpace(s.DNSType))
		if s.DNSType == "" {
			s.DNSType = "A"
		}
		if !slices.Contains(dnsTypes, s.DNSType) {
			return invalid(i18n.T("dns_type must be one of %s", strings.Join(dnsTypes, ", ")))
		}
		if srv := strings.TrimSpace(s.DNSServer); srv != "" {
			if s.DNSServer, err = normalizeServer(srv); err != nil {
				return err
			}
		}
		defaultName = fmt.Sprintf("%s (DNS %s)", name, s.DNSType)
	}
	if s.Type != TypeDNS && (s.DNSType != "" || s.DNSServer != "") {
		return invalid(i18n.T("dns_type and dns_server are only for type dns"))
	}

	if s.Name == "" {
		s.Name = defaultName
	}
	if len([]rune(s.Name)) > 100 {
		return invalid(i18n.T("name must be at most 100 characters"))
	}
	if hasControl(s.Name) {
		return invalid(i18n.T("name must not contain control characters"))
	}

	if s.Type == TypeHTTP {
		s.Method = strings.ToUpper(strings.TrimSpace(s.Method))
		switch s.Method {
		case "":
			s.Method = "GET"
		case "GET", "HEAD":
		default:
			return invalid(i18n.T("method must be GET or HEAD"))
		}
	} else {
		// Web-only settings mean nothing here; clear them rather than make every client omit them.
		s.Method, s.ExpectStatus, s.NoRedirect, s.UserAgent = "", nil, false, ""
	}

	push := s.Type == TypePush
	if s.IntervalSec == 0 {
		s.IntervalSec = 60
		if push {
			s.IntervalSec = 86400 // a daily job is the usual case
		}
	}
	minInterval, maxInterval := 10, 86400
	if push {
		minInterval, maxInterval = 60, MaxPushInterval
	}
	if s.IntervalSec < minInterval || s.IntervalSec > maxInterval {
		return invalid(i18n.T("interval_sec must be %d..%d", minInterval, maxInterval))
	}
	if push {
		s.TimeoutSec = 0 // nothing is fetched, so there is nothing to time out
		if s.GraceSec == 0 {
			s.GraceSec = 300
		}
		if s.GraceSec < 0 || s.GraceSec > 86400 {
			return invalid(i18n.T("grace_sec must be 0..86400"))
		}
	} else {
		s.PushToken, s.GraceSec = "", 0
		if s.TimeoutSec == 0 {
			s.TimeoutSec = 10
		}
		if s.TimeoutSec < 1 || s.TimeoutSec > 60 {
			return invalid(i18n.T("timeout_sec must be 1..60"))
		}
		if s.TimeoutSec > s.IntervalSec {
			return invalid(i18n.T("timeout_sec must not exceed interval_sec"))
		}
	}
	if s.FailThreshold == 0 {
		s.FailThreshold = 3
		if push {
			s.FailThreshold = 1 // a backup that did not report should be heard about at once
		}
	}
	if s.FailThreshold < 1 || s.FailThreshold > 10 {
		return invalid(i18n.T("fail_threshold must be 1..10"))
	}

	if len(s.ExpectStatus) > 20 {
		return invalid(i18n.T("expect_status accepts at most 20 values"))
	}
	for _, c := range s.ExpectStatus {
		if c < 100 || c > 599 {
			return invalid(i18n.T("expect_status must hold HTTP codes 100..599"))
		}
	}
	if len([]rune(s.Keyword)) > 200 {
		return invalid(i18n.T("keyword must be at most 200 characters"))
	}
	if len(s.NotifyTo) > 0 {
		var to []string
		for _, n := range s.NotifyTo {
			n = strings.ToLower(strings.TrimSpace(n))
			if n != model.DefaultRecipient && !model.ValidRecipient(n) {
				return invalid(i18n.T("notify_to %q is invalid: use lowercase letters, digits, - or _ (max 32)", n))
			}
			if !slices.Contains(to, n) {
				to = append(to, n)
			}
		}
		if len(to) > 10 {
			return invalid(i18n.T("notify_to accepts at most 10 recipients"))
		}
		s.NotifyTo = to
	}
	if s.Keyword != "" && s.Method == "HEAD" {
		return invalid(i18n.T("keyword cannot be used with method HEAD"))
	}
	if len(s.UserAgent) > 200 || hasControl(s.UserAgent) {
		return invalid(i18n.T("user_agent must be at most 200 characters with no control characters"))
	}
	if s.MaintenanceUntil < 0 || s.MaintenanceUntil > time.Now().Add(MaxMaintenance+time.Hour).UnixMilli() {
		return invalid(i18n.T("invalid maintenance_until"))
	}
	if s.MaintenanceSkipUntil < 0 {
		return invalid(i18n.T("invalid maintenance_skip_until"))
	}
	if err := schedule.Validate(s.MaintenanceWeekly); err != nil {
		return invalid(i18n.T("maintenance_weekly: %v", err))
	}
	if len(s.MaintenanceWeekly) == 0 {
		s.MaintenanceWeekly, s.MaintenanceSkipUntil = nil, 0 // nothing scheduled, nothing to skip
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}
