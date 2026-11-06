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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/schedule"
	"github.com/wisnuvb/ronda/sitespec"
)

const MaxSites = 500

// invalid wraps an already translated message: invalid(i18n.T("limit of %d sites reached", n)).
func invalid(msg string) error { return model.Invalid(msg) }

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
			if err := sitespec.Normalize(&f.Sites[i]); err != nil {
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
	return model.Site{}, model.ErrNotFound
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
	if err := sitespec.Normalize(&s); err != nil {
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
	if s.Type == sitespec.TypePush {
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
		return model.Site{}, model.ErrNotFound
	}
	next := clone(r.sites[i])
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		return model.Site{}, invalid(i18n.T("invalid JSON: %v", err))
	}
	next.ID, next.Created = r.sites[i].ID, r.sites[i].Created
	if r.sites[i].Type == sitespec.TypePush {
		next.URL, next.PushToken = r.sites[i].URL, r.sites[i].PushToken // the label and the secret are not editable
	}
	if err := sitespec.Normalize(&next); err != nil {
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
		return model.Site{}, model.ErrNotFound
	}
	all := slices.Clone(r.sites)
	all[i].Paused = paused
	if err := r.commitLocked(all); err != nil {
		return model.Site{}, err
	}
	return clone(all[i]), nil
}

// SetMaintenance silences a site until the given time (unix ms); 0 ends maintenance now.
func (r *Registry) SetMaintenance(id string, until int64) (model.Site, error) {
	now := time.Now()
	if until != 0 && (until <= now.UnixMilli() || until > now.Add(sitespec.MaxMaintenance).UnixMilli()) {
		return model.Site{}, invalid(i18n.T("maintenance must end in the future, at most %d days ahead", int(sitespec.MaxMaintenance.Hours()/24)))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.indexLocked(id)
	if i < 0 {
		return model.Site{}, model.ErrNotFound
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
		return model.Site{}, model.ErrNotFound
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
		return model.ErrNotFound
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
		if s.Type == sitespec.TypePush && len(s.PushToken) == len(token) && subtle.ConstantTimeCompare([]byte(s.PushToken), []byte(token)) == 1 {
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
		return model.Site{}, model.ErrNotFound
	}
	if r.sites[i].Type != sitespec.TypePush {
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
