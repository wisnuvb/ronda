// Package registry owns the list of monitored sites: validation, persistence to
// sites.json, and a change signal so the scheduler can pick up edits without a restart.
package registry

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
	"github.com/wisnuvb/ronda/internal/schedule"
)

const MaxSites = 500

// ErrNotFound is returned when no site has the id. It is a comparable value, so errors.Is works, and it
// translates itself when shown.
var ErrNotFound error = notFoundError{}

type notFoundError struct{}

func (notFoundError) Error() string { return i18n.T("site not found") }

// InvalidError means the caller sent something unacceptable (maps to HTTP 400).
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// invalid wraps an already translated message: invalid(i18n.T("limit of %d sites reached", n)).
func invalid(msg string) error { return &InvalidError{Msg: msg} }

type Registry struct {
	mu         sync.Mutex
	recipients map[string]bool // names a site may route its notifications to (config notify.recipients)
	path       string
	sites      []model.Site
	changed    chan struct{}
}

type fileFormat struct {
	Sites []model.Site `json:"sites"`
}

// Open loads dir/sites.json, creating dir if needed. A corrupt file is an error:
// silently starting empty would let the next save wipe the list.
func Open(dir string) (*Registry, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	// Fail now, loudly, rather than start and lose every change at the next restart. A read-only data_dir is the
	// classic way to get there: under systemd the unit only allows writes to /var/lib/ronda (ProtectSystem=strict).
	probe, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return nil, fmt.Errorf("data_dir %s is not writable: %w (under systemd, add ReadWritePaths=%s in a drop-in for ronda.service; install.sh does this when it sees a custom data_dir)", dir, err, dir)
	}
	probe.Close()
	os.Remove(probe.Name())
	r := &Registry{path: filepath.Join(dir, "sites.json"), changed: make(chan struct{}, 1)}
	b, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var f fileFormat
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("%s is unreadable: %w", r.path, err)
		}
		for i := range f.Sites {
			if err := Normalize(&f.Sites[i]); err != nil {
				return nil, fmt.Errorf("%s: site %q: %w", r.path, f.Sites[i].ID, err)
			}
		}
		r.sites = f.Sites
	}
	return r, nil
}

// Changed receives a signal (coalesced) after every successful modification.
func (r *Registry) Changed() <-chan struct{} { return r.changed }

func clone(s model.Site) model.Site {
	s.ExpectStatus = slices.Clone(s.ExpectStatus)
	s.NotifyTo = slices.Clone(s.NotifyTo)
	if s.MaintenanceWeekly != nil {
		ws := make([]model.Window, len(s.MaintenanceWeekly))
		for i, w := range s.MaintenanceWeekly {
			w.Days = slices.Clone(w.Days)
			ws[i] = w
		}
		s.MaintenanceWeekly = ws
	}
	return s
}

func (r *Registry) List() []model.Site {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.Site, len(r.sites))
	for i, s := range r.sites {
		out[i] = clone(s)
	}
	return out
}

func (r *Registry) Get(id string) (model.Site, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := r.indexLocked(id); i >= 0 {
		return clone(r.sites[i]), nil
	}
	return model.Site{}, ErrNotFound
}

func (r *Registry) indexLocked(id string) int {
	return slices.IndexFunc(r.sites, func(s model.Site) bool { return s.ID == id })
}

// SetRecipients tells the registry which recipient names exist. A site may only route to those (and to
// "default"); changing a site's routing to anything else is refused. Call it once, before serving.
func (r *Registry) SetRecipients(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recipients = map[string]bool{}
	for _, n := range names {
		r.recipients[n] = true
	}
}

// checkRecipientsLocked refuses a route to a recipient that is not configured.
func (r *Registry) checkRecipientsLocked(s model.Site) error {
	for _, n := range s.NotifyTo {
		if n != model.DefaultRecipient && !r.recipients[n] {
			if len(r.recipients) == 0 {
				return invalid(i18n.T("notify_to %q: no recipients are defined in the config (notify.recipients)", n))
			}
			return invalid(i18n.T("notify_to %q: unknown recipient; available: %s", n, strings.Join(r.recipientNamesLocked(), ", ")))
		}
	}
	return nil
}

func (r *Registry) recipientNamesLocked() []string {
	out := make([]string, 0, len(r.recipients))
	for n := range r.recipients {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Add validates s, assigns an ID and stores it. Any ID or Created in s is ignored.
func (r *Registry) Add(s model.Site) (model.Site, error) {
	s = clone(s)
	if err := Normalize(&s); err != nil {
		return model.Site{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sites) >= MaxSites {
		return model.Site{}, invalid(i18n.T("limit of %d sites reached", MaxSites))
	}
	if err := r.checkRecipientsLocked(s); err != nil {
		return model.Site{}, err
	}
	for {
		s.ID = newID()
		if r.indexLocked(s.ID) < 0 {
			break
		}
	}
	s.Created = time.Now().UnixMilli()
	if s.Type == TypePush {
		s.URL = "push://" + s.ID
		s.PushToken = newToken() // whatever the client sent is ignored: only the server mints secrets
	}
	if err := r.commitLocked(append(slices.Clone(r.sites), s)); err != nil {
		return model.Site{}, err
	}
	return clone(s), nil
}

// Patch overlays the JSON fields in raw onto the stored site. ID and Created cannot change.
func (r *Registry) Patch(id string, raw []byte) (model.Site, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return model.Site{}, ErrNotFound
	}
	next := clone(r.sites[i])
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		return model.Site{}, invalid(i18n.T("invalid JSON: %v", err))
	}
	next.ID, next.Created = r.sites[i].ID, r.sites[i].Created
	if r.sites[i].Type == TypePush {
		next.URL, next.PushToken = r.sites[i].URL, r.sites[i].PushToken // the label and the secret are not editable
	}
	if err := Normalize(&next); err != nil {
		return model.Site{}, err
	}
	if next.Type != r.sites[i].Type {
		return model.Site{}, invalid(i18n.T("the check type cannot be changed; create a new site"))
	}
	// Only a changed route is checked: a recipient removed from the config later must not make every other
	// edit of the site fail.
	if !slices.Equal(next.NotifyTo, r.sites[i].NotifyTo) {
		if err := r.checkRecipientsLocked(next); err != nil {
			return model.Site{}, err
		}
	}
	all := slices.Clone(r.sites)
	all[i] = next
	if err := r.commitLocked(all); err != nil {
		return model.Site{}, err
	}
	return clone(next), nil
}

func (r *Registry) SetPaused(id string, paused bool) (model.Site, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return model.Site{}, ErrNotFound
	}
	all := slices.Clone(r.sites)
	all[i].Paused = paused
	if err := r.commitLocked(all); err != nil {
		return model.Site{}, err
	}
	return clone(all[i]), nil
}

// MaxMaintenance is how far ahead a maintenance window may end.
const MaxMaintenance = 30 * 24 * time.Hour

// SetMaintenance silences a site until the given time (unix ms); 0 ends maintenance now.
func (r *Registry) SetMaintenance(id string, until int64) (model.Site, error) {
	now := time.Now()
	if until != 0 && (until <= now.UnixMilli() || until > now.Add(MaxMaintenance).UnixMilli()) {
		return model.Site{}, invalid(i18n.T("maintenance must end in the future, at most %d days ahead", int(MaxMaintenance.Hours()/24)))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return model.Site{}, ErrNotFound
	}
	all := slices.Clone(r.sites)
	all[i].MaintenanceUntil = until
	if err := r.commitLocked(all); err != nil {
		return model.Site{}, err
	}
	return clone(all[i]), nil
}

// EndMaintenance stops maintenance now. A manual window simply ends. A scheduled window that is running
// is skipped for this one run only, so next week's still happens.
func (r *Registry) EndMaintenance(id string) (model.Site, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return model.Site{}, ErrNotFound
	}
	all := slices.Clone(r.sites)
	all[i] = clone(all[i])
	all[i].MaintenanceUntil = 0
	if end, ok := schedule.Active(all[i].MaintenanceWeekly, time.Now(), all[i].MaintenanceSkipUntil); ok {
		all[i].MaintenanceSkipUntil = end.UnixMilli()
	}
	if err := r.commitLocked(all); err != nil {
		return model.Site{}, err
	}
	return clone(all[i]), nil
}

func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return ErrNotFound
	}
	return r.commitLocked(slices.Delete(slices.Clone(r.sites), i, i+1))
}

// commitLocked writes next to disk and only then adopts it, so a failed write
// leaves memory and disk consistent.
func (r *Registry) commitLocked(next []model.Site) error {
	b, err := json.MarshalIndent(fileFormat{Sites: next}, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return err
	}
	r.sites = next
	select {
	case r.changed <- struct{}{}:
	default:
	}
	return nil
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ByToken finds the push site that owns token. It compares against every push site without stopping at
// the first match, so how long it takes does not say how much of a guess was right.
func (r *Registry) ByToken(token string) (model.Site, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found *model.Site
	for i := range r.sites {
		s := &r.sites[i]
		if s.Type == TypePush && len(s.PushToken) == len(token) && subtle.ConstantTimeCompare([]byte(s.PushToken), []byte(token)) == 1 {
			found = s
		}
	}
	if found == nil {
		return model.Site{}, false
	}
	return clone(*found), true
}

// RotateToken replaces a push site's secret, for when the old one has leaked. The old ping URL stops working at once.
func (r *Registry) RotateToken(id string) (model.Site, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return model.Site{}, ErrNotFound
	}
	if r.sites[i].Type != TypePush {
		return model.Site{}, invalid(i18n.T("only push sites have a ping token"))
	}
	all := slices.Clone(r.sites)
	all[i].PushToken = newToken()
	if err := r.commitLocked(all); err != nil {
		return model.Site{}, err
	}
	return clone(all[i]), nil
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

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
