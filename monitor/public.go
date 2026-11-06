package monitor

import (
	"slices"
	"strings"
	"time"

	"github.com/wisnuvb/ronda/model"
)

const (
	publicDays          = 90
	publicIncidentDays  = 14
	publicIncidentLimit = 30
)

// PublicSite is all that the public status page may say about a site. There is deliberately no URL, no
// id and no diagnosis: those describe the inside of someone's infrastructure.
type PublicSite struct {
	Name      string     `json:"name"`
	State     string     `json:"state"` // up, down, maintenance or unknown
	Uptime90d *float64   `json:"uptime_90d,omitempty"`
	Days      [][3]int64 `json:"days"` // [day start ms (00:00 in the configured time zone), checks passed, checks made], 90 entries, today last
}

// PublicIncident names the site and a general kind of trouble, never the technical detail.
type PublicIncident struct {
	Site        string `json:"site"`
	Kind        string `json:"kind"`
	Started     int64  `json:"started"`
	Ended       int64  `json:"ended,omitempty"`
	DurationSec int64  `json:"duration_sec,omitempty"`
	Ongoing     bool   `json:"ongoing,omitempty"`
}

type PublicStatus struct {
	Overall   string           `json:"overall"` // none, operational, maintenance, partial or outage
	Updated   int64            `json:"updated"`
	Sites     []PublicSite     `json:"sites"`
	Incidents []PublicIncident `json:"incidents"`
}

// PublicStatus summarises the sites marked public. Paused sites are left out: an administrator who
// paused a site did not mean to show visitors a frozen status.
func (m *Monitor) PublicStatus() PublicStatus {
	now := m.now()
	ps := PublicStatus{Updated: now.UnixMilli(), Sites: []PublicSite{}, Incidents: []PublicIncident{}}

	public := map[string]string{} // id -> name
	var down, maint, up, other int
	for _, s := range m.opt.Sites.List() {
		if !s.Public || s.Paused {
			continue
		}
		v, ok := m.View(s.ID)
		if !ok || v.State == StPaused {
			continue
		}
		site := PublicSite{Name: s.Name, State: v.State, Uptime90d: nil, Days: [][3]int64{}}
		if m.opt.History != nil {
			site.Uptime90d = m.opt.History.Full(s.ID).Uptime90d
			for _, d := range m.opt.History.Daily(s.ID, publicDays) {
				site.Days = append(site.Days, [3]int64{d.Start, int64(d.Up), int64(d.Total)})
			}
		}
		switch v.State {
		case StDown:
			down++
		case StMaint:
			maint++
		case StUp:
			up++
		default:
			site.State = StUnknown
			other++
		}
		public[s.ID] = s.Name
		ps.Sites = append(ps.Sites, site)
	}
	slices.SortFunc(ps.Sites, func(a, b PublicSite) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })

	switch total := down + maint + up + other; {
	case total == 0:
		ps.Overall = "none"
	case down == 0 && up+other == 0:
		ps.Overall = "maintenance"
	case down == 0:
		ps.Overall = "operational"
	case down == total:
		ps.Overall = "outage"
	default:
		ps.Overall = "partial"
	}

	since := now.Add(-publicIncidentDays * 24 * time.Hour).UnixMilli()
	for _, in := range m.Incidents("") { // newest first
		name, ok := public[in.SiteID]
		if !ok || (!in.Ongoing && in.Started < since) {
			continue
		}
		ps.Incidents = append(ps.Incidents, PublicIncident{
			Site: name, Kind: model.KindLabel(in.Kind), Started: in.Started, Ended: in.Ended,
			DurationSec: in.DurationSec, Ongoing: in.Ongoing,
		})
		if len(ps.Incidents) == publicIncidentLimit {
			break
		}
	}
	return ps
}
