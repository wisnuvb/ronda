package registry

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

// ExportVersion is the format number written into an export. Import refuses a newer one.
const ExportVersion = 1

// MaxImport bounds one import file; it is far above MaxSites so a too-big file gets a clear message.
const MaxImport = 5000

// Export is the portable form of the whole site list: configuration only, no history and no ids.
type Export struct {
	Version    int              `json:"version"`
	ExportedAt int64            `json:"exported_at"`
	Sites      []map[string]any `json:"sites"`
}

// transient says which fields describe this installation's moment rather than a site's configuration.
var transient = []string{"id", "created", "maintenance_until", "maintenance_skip_until"}

// Export returns every site as a map built from the site's own JSON, so a field added to model.Site
// later is exported without anybody remembering to touch this function. A push site's token is a
// secret: it is left out unless includeTokens, and then jobs must be updated with the new ping address.
func (r *Registry) Export(includeTokens bool) (Export, error) {
	out := Export{Version: ExportVersion, ExportedAt: time.Now().UnixMilli(), Sites: []map[string]any{}}
	for _, s := range r.List() {
		raw, err := json.Marshal(s)
		if err != nil {
			return Export{}, err
		}
		m := map[string]any{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return Export{}, err
		}
		for _, k := range transient {
			delete(m, k)
		}
		if !includeTokens {
			delete(m, "push_token")
		}
		if s.Type == TypePush {
			delete(m, "url") // only a label, rebuilt on import
		}
		out.Sites = append(out.Sites, m)
	}
	return out, nil
}

// Import statuses.
const (
	ImportAdded   = "added"
	ImportSkipped = "skipped" // already there
	ImportInvalid = "invalid"
)

// ImportItem is what happened to one site of the file.
type ImportItem struct {
	Index  int    `json:"index"` // 1-based position in the file
	Name   string `json:"name"`
	URL    string `json:"url,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ImportResult summarises an import. With DryRun nothing was stored; the numbers are what would happen.
type ImportResult struct {
	DryRun  bool         `json:"dry_run"`
	Added   int          `json:"added"`
	Skipped int          `json:"skipped"`
	Invalid int          `json:"invalid"`
	Items   []ImportItem `json:"items"`
}

var tokenShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

// siteKey identifies a site for duplicate detection: the checked address, or for a push site (which
// has no address) its name.
func siteKey(s model.Site) string {
	if s.Type == TypePush {
		return "push\x00" + strings.ToLower(s.Name)
	}
	return s.Type + "\x00" + strings.ToLower(s.URL)
}

// Import adds the sites of an export file. Every site is validated exactly as if it were added by hand;
// the ones that fail are reported and do not stop the rest. Sites already present (same address, or same
// name for a push site) are skipped, so importing the same file twice changes nothing. All accepted sites
// are written in one step. A push site keeps the token in the file when it is well formed and unused,
// which is what lets a migrated server keep receiving the pings of existing jobs.
func (r *Registry) Import(data []byte, dryRun bool) (ImportResult, error) {
	var file struct {
		Version int               `json:"version"`
		Sites   []json.RawMessage `json:"sites"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&file); err != nil {
		return ImportResult{}, invalid(i18n.T("the file is not a Ronda export (JSON): %v", err))
	}
	if file.Version > ExportVersion {
		return ImportResult{}, invalid(i18n.T("the file was made by a newer Ronda (format %d); update Ronda first", file.Version))
	}
	if len(file.Sites) == 0 {
		return ImportResult{}, invalid(i18n.T("the file contains no sites"))
	}
	if len(file.Sites) > MaxImport {
		return ImportResult{}, invalid(i18n.T("the file contains %d sites; the limit is %d per import", len(file.Sites), MaxImport))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	seen := map[string]bool{}
	tokens := map[string]bool{}
	ids := map[string]bool{}
	for _, s := range r.sites {
		seen[siteKey(s)] = true
		ids[s.ID] = true
		if s.PushToken != "" {
			tokens[s.PushToken] = true
		}
	}

	res := ImportResult{DryRun: dryRun, Items: make([]ImportItem, 0, len(file.Sites))}
	next := slices.Clone(r.sites)
	now := time.Now().UnixMilli()
	for i, raw := range file.Sites {
		item := ImportItem{Index: i + 1}
		var s model.Site
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&s); err != nil {
			item.Status, item.Reason = ImportInvalid, i18n.T("unrecognized format: %s", err.Error())
			res.add(item)
			continue
		}
		item.Name, item.URL = s.Name, s.URL
		wantToken := s.PushToken
		s = clone(s)
		s.ID, s.Created, s.MaintenanceUntil, s.MaintenanceSkipUntil = "", 0, 0, 0
		s.PushToken = ""
		if err := Normalize(&s); err != nil {
			item.Status, item.Reason = ImportInvalid, err.Error()
			res.add(item)
			continue
		}
		if s.Type == TypePush {
			item.URL = ""
		} else {
			item.URL = s.URL
		}
		item.Name = s.Name
		if key := siteKey(s); seen[key] {
			item.Status, item.Reason = ImportSkipped, i18n.T("already exists")
			res.add(item)
			continue
		} else {
			seen[key] = true
		}
		if err := r.checkRecipientsLocked(s); err != nil {
			item.Status, item.Reason = ImportInvalid, err.Error()
			res.add(item)
			continue
		}
		if len(next) >= MaxSites {
			item.Status, item.Reason = ImportInvalid, i18n.T("limit of %d sites reached", MaxSites)
			res.add(item)
			continue
		}
		for {
			s.ID = newID()
			if !ids[s.ID] {
				break
			}
		}
		ids[s.ID] = true
		s.Created = now
		if s.Type == TypePush {
			s.URL = "push://" + s.ID
			s.PushToken = newToken()
			if tokenShape.MatchString(wantToken) && !tokens[wantToken] {
				s.PushToken = wantToken
			}
			tokens[s.PushToken] = true
		}
		next = append(next, s)
		item.Status = ImportAdded
		res.add(item)
	}
	if !dryRun && res.Added > 0 {
		if err := r.commitLocked(next); err != nil {
			return ImportResult{}, err
		}
	}
	return res, nil
}

func (res *ImportResult) add(it ImportItem) {
	switch it.Status {
	case ImportAdded:
		res.Added++
	case ImportSkipped:
		res.Skipped++
	default:
		res.Invalid++
	}
	res.Items = append(res.Items, it)
}
