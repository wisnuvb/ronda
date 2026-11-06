// Package model holds the types shared by every other package.
package model

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/wisnuvb/ronda/i18n"
	_ "time/tzdata" // embed the zone database: a static binary must not depend on the server having one
)

var location atomic.Pointer[time.Location]

func init() { location.Store(time.UTC) }

// Loc is the time zone of the whole program: the clock times people read in messages and the dashboard,
// the "HH:MM" of schedules and maintenance windows, and where a "day" begins in the uptime history.
// It is an IANA zone (config "timezone"), so daylight saving is followed; UTC until SetTimezone is called.
func Loc() *time.Location { return location.Load() }

// SetTimezone selects the zone by IANA name ("Europe/Berlin", "Asia/Jakarta", "UTC"). Empty means UTC.
// Call it once at startup, before checks and schedulers run.
func SetTimezone(name string) error {
	if name == "" {
		name = "UTC"
	}
	if name == "Local" {
		// time.LoadLocation("Local") is the server's zone, which moves with the server; make it explicit.
		return fmt.Errorf("timezone %q: use an IANA name such as \"Europe/Berlin\" or \"UTC\", not \"Local\"", name)
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("timezone %q: not an IANA zone name (examples: UTC, Europe/Berlin, America/New_York, Asia/Jakarta)", name)
	}
	location.Store(l)
	return nil
}

// In returns t in the program's zone.
func In(t time.Time) time.Time { return t.In(Loc()) }

// Stamp formats unix milliseconds in the program's zone and appends the zone's own abbreviation
// ("WIB", "CEST", "EST", or "+03" where the zone has none), e.g. Stamp(ms, "15:04:05") = "09:30:12 CEST".
// The abbreviation follows daylight saving, so the same zone prints CET in winter and CEST in summer.
func Stamp(ms int64, layout string) string {
	return time.UnixMilli(ms).In(Loc()).Format(layout + " MST")
}

// Midnight is 00:00 of t's calendar day in the program's zone. On the day daylight saving starts or
// ends that day is 23 or 25 hours long, which is why a day must never be computed as 24 h arithmetic.
func Midnight(t time.Time) time.Time {
	l := t.In(Loc())
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, Loc())
}

// ClockOn is hh:mm on the calendar day of t, in the program's zone, by the wall clock: 08:00 stays 08:00
// across a daylight-saving change. (A time that does not exist that day, such as 02:30 when the clocks
// jump from 02:00 to 03:00, lands on the nearest valid moment; one that happens twice uses the first.)
func ClockOn(t time.Time, mins int) time.Time {
	l := t.In(Loc())
	return time.Date(l.Year(), l.Month(), l.Day(), mins/60, mins%60, 0, 0, Loc())
}

// Diagnosis kinds, stored in Result.Kind.
const (
	KindOK              = "ok"
	KindDNS             = "dns"
	KindRefused         = "refused"
	KindUnreachable     = "unreachable"
	KindReset           = "reset"
	KindTimeoutConnect  = "timeout_connect"
	KindTimeoutTLS      = "timeout_tls"
	KindTimeoutResponse = "timeout_response"
	KindTLSExpired      = "tls_expired"
	KindTLSHostname     = "tls_hostname"
	KindTLSUntrusted    = "tls_untrusted"
	KindTLS             = "tls"
	KindRedirectLoop    = "redirect_loop"
	KindBlocked         = "blocked"
	KindCloudflare      = "cloudflare"
	KindHTTP5xx         = "http_5xx"
	KindHTTP4xx         = "http_4xx"
	KindStatus          = "status"
	KindKeyword         = "keyword"
	KindMissed          = "missed"           // push: no ping arrived in time
	KindReported        = "reported_failure" // push: the job reported failure itself
	KindError           = "error"
)

// Site is one monitored URL. It is what gets stored in sites.json and what the API accepts.
type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// URL is the target. Its scheme says what kind of check it is: http(s):// for a web page,
	// tcp://host:port for a port, dns://name for a DNS record.
	URL string `json:"url"`
	// Type is "http" (default), "tcp", "dns" or "push"; it is inferred from the URL when empty.
	// A push site is not checked: a job pings ronda (/ping/<PushToken>) and silence means trouble.
	// Its URL is only a label, push://<id>; the token is the secret and is never shown outside the dashboard.
	Type string `json:"type"`
	// DNSType (A, AAAA, MX, TXT or NS, default A) and DNSServer (host[:port], default: the system
	// resolver) apply to type dns only. For dns, Keyword is the answer that must be present.
	PushToken     string `json:"push_token,omitempty"`
	GraceSec      int    `json:"grace_sec,omitempty"` // push: extra time allowed after interval_sec before a ping counts as missed
	DNSType       string `json:"dns_type,omitempty"`
	DNSServer     string `json:"dns_server,omitempty"`
	Method        string `json:"method,omitempty"` // http only: GET (default) or HEAD
	IntervalSec   int    `json:"interval_sec"`     // 10..86400, default 60
	TimeoutSec    int    `json:"timeout_sec"`      // 1..60, default 10
	FailThreshold int    `json:"fail_threshold"`   // consecutive failures before DOWN, default 3
	ExpectStatus  []int  `json:"expect_status,omitempty"`
	Keyword       string `json:"keyword,omitempty"` // must appear in the body (case-insensitive)
	NoRedirect    bool   `json:"no_redirect,omitempty"`
	UserAgent     string `json:"user_agent,omitempty"`
	AllowPrivate  bool   `json:"allow_private,omitempty"` // permit loopback/LAN targets
	Paused        bool   `json:"paused,omitempty"`
	// Public shows the site (name, status, uptime) on the public status page, if that is turned on.
	Public bool `json:"public,omitempty"`
	// NotifyTo names the recipients (config notify.recipients) that hear about this site; "default" is the
	// main chats. Empty means the main chats only. Several clients, one monitor: each hears about its own sites.
	NotifyTo []string `json:"notify_to,omitempty"`
	// MaintenanceUntil (unix ms) silences the site until then: no checks, no alerts. It ends by itself.
	MaintenanceUntil int64 `json:"maintenance_until,omitempty"`
	// MaintenanceWeekly repeats a silent window every week. Ending maintenance early while one is
	// running sets MaintenanceSkipUntil to that run's end, so that single run is skipped, not the schedule.
	MaintenanceWeekly    []Window `json:"maintenance_weekly,omitempty"`
	MaintenanceSkipUntil int64    `json:"maintenance_skip_until,omitempty"`
	Created              int64    `json:"created"` // unix ms
}

// DefaultRecipient is the name that stands for the main chats (notify.telegram_chat_id).
const DefaultRecipient = "default"

// ValidRecipient says whether name is acceptable as a recipient name: short, lower case, no spaces.
func ValidRecipient(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '-' || r == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}

// Window is a weekly maintenance window, in the program's time zone (Loc): it starts at Start on each of Days and lasts Minutes
// (it may run past midnight).
type Window struct {
	Days    []int  `json:"days"`    // time.Weekday numbers: 0 = Sunday ... 6 = Saturday
	Start   string `json:"start"`   // "HH:MM"
	Minutes int    `json:"minutes"` // 1..1440
}

// Result is the outcome of one check.
type Result struct {
	Time         int64  `json:"time"` // unix ms, start of the check
	OK           bool   `json:"ok"`
	Status       int    `json:"status,omitempty"`
	LatencyMs    int    `json:"latency_ms"`
	DNSMs        int    `json:"dns_ms,omitempty"`
	ConnectMs    int    `json:"connect_ms,omitempty"`
	TLSMs        int    `json:"tls_ms,omitempty"`
	TTFBMs       int    `json:"ttfb_ms,omitempty"`
	Kind         string `json:"kind"`
	Error        string `json:"error,omitempty"`  // short, for logs and tables
	Hint         string `json:"hint,omitempty"`   // human explanation and next step
	Answer       string `json:"answer,omitempty"` // dns: what the lookup returned
	CertNotAfter int64  `json:"cert_not_after,omitempty"`
}

// Event is a state change worth telling a human about.
type Event struct {
	Time        int64    `json:"time"`
	State       string   `json:"state"` // "down", "up", "reminder" (still down), "cert" (certificate about to expire) or "test"
	SiteID      string   `json:"site_id,omitempty"`
	SiteName    string   `json:"site_name"`
	URL         string   `json:"url,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Hint        string   `json:"hint,omitempty"`
	Error       string   `json:"error,omitempty"`
	Status      int      `json:"status,omitempty"`
	LatencyMs   int      `json:"latency_ms,omitempty"`
	Fails       int      `json:"fails,omitempty"`
	To          []string `json:"to,omitempty"`           // recipients, copied from the site (see Site.NotifyTo)
	Since       int64    `json:"since,omitempty"`        // down: when the failures began
	DowntimeSec int64    `json:"downtime_sec,omitempty"` // up: how long it was down; reminder: how long it has been down so far

	CertNotAfter int64 `json:"cert_not_after,omitempty"` // cert: when the certificate expires
	DaysLeft     int   `json:"days_left,omitempty"`      // cert: whole days until then
}

// SiteState is the part of a site's state that survives a restart.
type SiteState struct {
	Status   string `json:"status,omitempty"`
	Since    int64  `json:"since,omitempty"`
	CertTier int    `json:"cert_tier,omitempty"` // the smallest expiry threshold (days) already announced
	Reminded int    `json:"reminded,omitempty"`  // how many "still down" reminders were already sent for the current outage
}

var kindLabels = map[string]string{
	KindOK: i18n.N("Healthy"), KindDNS: i18n.N("DNS failed"), KindRefused: i18n.N("Connection refused"), KindUnreachable: i18n.N("Unreachable"),
	KindReset: i18n.N("Connection reset"), KindTimeoutConnect: i18n.N("Connect timeout"), KindTimeoutTLS: i18n.N("TLS timeout"),
	KindTimeoutResponse: i18n.N("Response timeout"), KindTLSExpired: i18n.N("Certificate expired"),
	KindTLSHostname: i18n.N("Certificate name mismatch"), KindTLSUntrusted: i18n.N("Certificate not trusted"), KindTLS: i18n.N("TLS error"),
	KindRedirectLoop: i18n.N("Redirect loop"), KindBlocked: i18n.N("Address blocked"), KindCloudflare: i18n.N("Cloudflare error"),
	KindHTTP5xx: i18n.N("Server error (5xx)"), KindHTTP4xx: i18n.N("Access error (4xx)"), KindStatus: i18n.N("Unexpected status"),
	KindKeyword: i18n.N("Text not found"), KindError: i18n.N("Error"),
	KindMissed: i18n.N("Ping not received"), KindReported: i18n.N("Job reported failure"),
}

// KindLabel is the short name of a diagnosis kind in the configured language (the dashboard keeps its own copy).
func KindLabel(kind string) string {
	if l, ok := kindLabels[kind]; ok {
		return i18n.T(l)
	}
	if kind == "" {
		return "–"
	}
	return kind
}
