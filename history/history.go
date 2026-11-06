// Package history keeps what the dashboard charts: recent raw results per site
// (about 24 hours) and hourly rollups for 90 days, persisted to history.json.
package history

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
)

const (
	maxRaw     = 2880 // ~24h at 30s, ~8h at 10s
	rawAge     = 24 * time.Hour
	maxHours   = 24 * 90
	stripLen   = 30
	hourMillis = int64(time.Hour / time.Millisecond)

	tempPrefix = "history.json.tmp-"
)

type point struct {
	T  int64 // unix ms
	Ms int32 // latency; -1 means the check failed
}

type bucket struct {
	H     int64 // hour index: unix ms / 3600000
	Total int32
	Up    int32
	SumMs int64 // total latency of the successful checks
}

type series struct {
	raw   []point
	hours []bucket
}

// Stats is what the API shows next to a site. Uptime fields are nil when there is no data yet.
type Stats struct {
	Uptime24h *float64 `json:"uptime_24h,omitempty"`
	Uptime7d  *float64 `json:"uptime_7d,omitempty"`
	Uptime30d *float64 `json:"uptime_30d,omitempty"`
	Uptime90d *float64 `json:"uptime_90d,omitempty"`
	AvgMs     int      `json:"avg_ms,omitempty"`     // mean latency of successful checks, last 24h
	P95Ms     int      `json:"p95_ms,omitempty"`     // 95th percentile, last 24h (detail only)
	Checks24h int      `json:"checks_24h,omitempty"` // number of checks in the last 24h
	Strip     []int    `json:"strip,omitempty"`      // last results: latency in ms, -1 for a failure (list only)
}

// SeriesData is a chart's worth of data. Raw ranges carry every check; longer
// ranges carry hourly rollups.
type SeriesData struct {
	Range  string     `json:"range"`
	Raw    [][2]int64 `json:"raw,omitempty"`    // [time ms, latency ms or -1]
	Hourly [][4]int64 `json:"hourly,omitempty"` // [hour start ms, avg latency ms, ok checks, total checks]
}

var rangeSpec = map[string]struct {
	d   time.Duration
	raw bool
}{
	"1h": {time.Hour, true}, "6h": {6 * time.Hour, true}, "24h": {24 * time.Hour, true},
	"7d": {7 * 24 * time.Hour, false}, "30d": {30 * 24 * time.Hour, false}, "90d": {90 * 24 * time.Hour, false},
}

type Store struct {
	dir string
	now func() time.Time

	mu     sync.Mutex
	sites  map[string]*series
	saveMu sync.Mutex // two saves at once would write the same temp file
}

// Open loads dir/history.json. An unreadable file is logged and ignored: history
// is a convenience, and refusing to start over it would be worse.
func Open(dir string) *Store {
	s := &Store{dir: dir, now: time.Now, sites: map[string]*series{}}
	if dir == "" {
		return s
	}
	// A crash mid-save leaves a half-written temp file behind; the real file is untouched, so just sweep it.
	if stale, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*")); len(stale) > 0 {
		for _, f := range stale {
			_ = os.Remove(f)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if err != nil {
		return s
	}
	var f struct {
		Sites map[string]*series `json:"sites"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		log.Printf("history: history.json is corrupt, starting empty: %v", err)
		return s
	}
	if f.Sites != nil {
		s.sites = f.Sites
	}
	return s
}

func (se *series) MarshalJSON() ([]byte, error) {
	aux := struct {
		Raw   [][2]int64 `json:"raw"`
		Hours [][4]int64 `json:"hours"`
	}{make([][2]int64, len(se.raw)), make([][4]int64, len(se.hours))}
	for i, p := range se.raw {
		aux.Raw[i] = [2]int64{p.T, int64(p.Ms)}
	}
	for i, b := range se.hours {
		aux.Hours[i] = [4]int64{b.H, int64(b.Total), int64(b.Up), b.SumMs}
	}
	return json.Marshal(aux)
}

func (se *series) UnmarshalJSON(b []byte) error {
	var aux struct {
		Raw   [][2]int64 `json:"raw"`
		Hours [][4]int64 `json:"hours"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	for _, p := range aux.Raw {
		se.raw = append(se.raw, point{T: p[0], Ms: int32(p[1])})
	}
	for _, h := range aux.Hours {
		se.hours = append(se.hours, bucket{H: h[0], Total: int32(h[1]), Up: int32(h[2]), SumMs: h[3]})
	}
	return nil
}

// Record adds one check result for site id.
func (s *Store) Record(id string, r model.Result) {
	ms := int32(-1)
	if r.OK {
		ms = int32(min(max(r.LatencyMs, 0), math.MaxInt32))
	}
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.sites[id]
	if se == nil {
		se = &series{}
		s.sites[id] = se
	}

	se.raw = append(se.raw, point{T: r.Time, Ms: ms})
	cut := now.Add(-rawAge).UnixMilli()
	drop := 0
	for drop < len(se.raw) && se.raw[drop].T < cut {
		drop++
	}
	drop = max(drop, len(se.raw)-maxRaw)
	if drop > 0 {
		se.raw = slices.Delete(se.raw, 0, drop)
	}

	h := r.Time / hourMillis
	i := len(se.hours) - 1
	for i >= 0 && se.hours[i].H > h {
		i--
	}
	if i < 0 || se.hours[i].H != h {
		se.hours = slices.Insert(se.hours, i+1, bucket{H: h})
		i++
	}
	b := &se.hours[i]
	b.Total++
	if ms >= 0 {
		b.Up++
		b.SumMs += int64(ms)
	}
	if old := now.UnixMilli()/hourMillis - maxHours; len(se.hours) > 0 && se.hours[0].H < old {
		k := 0
		for k < len(se.hours) && se.hours[k].H < old {
			k++
		}
		se.hours = slices.Delete(se.hours, 0, k)
	}
}

// Delete forgets a site's history.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	delete(s.sites, id)
	s.mu.Unlock()
}

// pct is the uptime percentage. It never shows 100 while a check has failed, and
// never rounds a failure away: 99.996 becomes 99.99.
func pct(up, total int64) *float64 {
	if total == 0 {
		return nil
	}
	v := 100.0
	if up < total {
		v = math.Min(math.Floor(float64(up)/float64(total)*10000)/100, 99.99)
	}
	return &v
}

// uptime counts the last `hours` hours. The buckets are sorted oldest first, so walk back from the end
// and stop at the first one outside the window: the 24-hour figure shown in every list row then looks at
// ~25 buckets instead of all 2,160.
func (se *series) uptime(nowHour int64, hours int64) *float64 {
	var up, total int64
	for i := len(se.hours) - 1; i >= 0; i-- {
		b := se.hours[i]
		if b.H <= nowHour-hours {
			break
		}
		up += int64(b.Up)
		total += int64(b.Total)
	}
	return pct(up, total)
}

// latency24h summarises the last 24 hours. The 95th percentile needs a sorted copy of up to ~2900
// values, so it is computed only when asked: the list view runs this for every site on every refresh
// and does not show it.
func (se *series) latency24h(cut int64, wantP95 bool) (avg, p95, checks int) {
	var sum, n int64
	var sorted []int
	for _, p := range se.raw {
		if p.T < cut {
			continue
		}
		checks++
		if p.Ms >= 0 {
			sum += int64(p.Ms)
			n++
			if wantP95 {
				sorted = append(sorted, int(p.Ms))
			}
		}
	}
	if n == 0 {
		return 0, 0, checks
	}
	if wantP95 {
		slices.Sort(sorted)
		idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
		p95 = sorted[max(idx, 0)]
	}
	return int(sum / n), p95, checks
}

// Brief is the cheap form for list rows: 24h uptime, average latency, and a strip of recent results.
func (s *Store) Brief(id string) Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.sites[id]
	if se == nil {
		return Stats{}
	}
	now := s.now()
	st := Stats{Uptime24h: se.uptime(now.UnixMilli()/hourMillis, 24)}
	st.AvgMs, _, st.Checks24h = se.latency24h(now.Add(-rawAge).UnixMilli(), false)
	from := max(0, len(se.raw)-stripLen)
	for _, p := range se.raw[from:] {
		st.Strip = append(st.Strip, int(p.Ms))
	}
	return st
}

// Full is the detail form: every uptime window plus the 95th percentile.
func (s *Store) Full(id string) Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.sites[id]
	if se == nil {
		return Stats{}
	}
	now := s.now()
	nh := now.UnixMilli() / hourMillis
	st := Stats{
		Uptime24h: se.uptime(nh, 24), Uptime7d: se.uptime(nh, 7*24),
		Uptime30d: se.uptime(nh, 30*24), Uptime90d: se.uptime(nh, 90*24),
	}
	st.AvgMs, st.P95Ms, st.Checks24h = se.latency24h(now.Add(-rawAge).UnixMilli(), true)
	return st
}

// Series returns chart data for one of 1h, 6h, 24h, 7d, 30d, 90d.
func (s *Store) Series(id, rng string) (SeriesData, error) {
	spec, ok := rangeSpec[rng]
	if !ok {
		return SeriesData{}, errors.New(i18n.T("range must be one of 1h, 6h, 24h, 7d, 30d, 90d"))
	}
	out := SeriesData{Range: rng}

	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.sites[id]
	if se == nil {
		return out, nil
	}
	now := s.now()
	if spec.raw {
		cut := now.Add(-spec.d).UnixMilli()
		for _, p := range se.raw {
			if p.T >= cut {
				out.Raw = append(out.Raw, [2]int64{p.T, int64(p.Ms)})
			}
		}
		return out, nil
	}
	first := now.UnixMilli()/hourMillis - int64(spec.d/time.Hour) + 1
	for _, b := range se.hours {
		if b.H < first {
			continue
		}
		var avg int64
		if b.Up > 0 {
			avg = b.SumMs / int64(b.Up)
		}
		out.Hourly = append(out.Hourly, [4]int64{b.H * hourMillis, avg, int64(b.Up), int64(b.Total)})
	}
	return out, nil
}

// Day is one calendar day (in model.Loc) of results: how many checks passed out of how many were made.
type Day struct {
	Start int64 `json:"start"` // unix ms of 00:00 that day; a day is 23 or 25 hours long when daylight saving changes
	Up    int   `json:"up"`
	Total int   `json:"total"`
}

// Daily returns the last n calendar days (in model.Loc, oldest first, today last) from the hourly rollups.
// A day with no checks (a paused site, or before it existed) has Total 0 so a chart can tell "no data" from
// "all well". Days are real calendar days, so one of them is 23 or 25 hours when daylight saving changes.
func (s *Store) Daily(id string, n int) []Day {
	today := model.Midnight(s.now())
	days := make([]Day, n)
	for i := range days {
		days[i].Start = today.AddDate(0, 0, -(n - 1 - i)).UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.sites[id]
	if se == nil {
		return days
	}
	for _, b := range se.hours {
		at := b.H * hourMillis
		if at < days[0].Start {
			continue
		}
		// The day an hour belongs to is the last one that started at or before it.
		i := sort.Search(n, func(k int) bool { return days[k].Start > at }) - 1
		if i < 0 || i >= n {
			continue
		}
		days[i].Up += int(b.Up)
		days[i].Total += int(b.Total)
	}
	return days
}

// Save writes history.json atomically, one site at a time. The file for a year-old install with hundreds
// of sites is tens of megabytes; building it in memory first (and holding the lock while doing so)
// doubled the process's memory and froze every reader for the duration. Here only one site is copied
// and encoded at once, and the lock is held just long enough to copy it.
func (s *Store) Save() {
	if s.dir == "" {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	start := time.Now()

	tmp, err := os.CreateTemp(s.dir, tempPrefix+"*")
	if err != nil {
		log.Printf("history: save: %v", err)
		return
	}
	fail := func(err error) {
		log.Printf("history: save: %v", err)
		tmp.Close()
		os.Remove(tmp.Name())
	}
	w := bufio.NewWriterSize(tmp, 256<<10)
	_, _ = w.WriteString(`{"sites":{`)

	s.mu.Lock()
	ids := make([]string, 0, len(s.sites))
	for id := range s.sites {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	first := true
	for _, id := range ids {
		s.mu.Lock()
		se := s.sites[id]
		var snap series
		if se != nil {
			snap = series{raw: slices.Clone(se.raw), hours: slices.Clone(se.hours)}
		}
		s.mu.Unlock()
		if se == nil { // removed while we were writing
			continue
		}
		body, err := json.Marshal(&snap)
		if err != nil {
			fail(err)
			return
		}
		key, _ := json.Marshal(id)
		if !first {
			_ = w.WriteByte(',')
		}
		first = false
		_, _ = w.Write(key)
		_ = w.WriteByte(':')
		if _, err := w.Write(body); err != nil {
			fail(err)
			return
		}
	}
	_, _ = w.WriteString(`}}`)
	if err := w.Flush(); err != nil {
		fail(err)
		return
	}
	if err := tmp.Chmod(0o640); err != nil {
		fail(err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		log.Printf("history: save: %v", err)
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, "history.json")); err != nil {
		os.Remove(tmp.Name())
		log.Printf("history: save: %v", err)
		return
	}
	if d := time.Since(start); d > time.Second {
		log.Printf("history: slow save: %s for %d sites", d.Round(time.Millisecond), len(ids))
	}
}

// AutoSave saves every interval until ctx ends, then saves once more and returns.
func (s *Store) AutoSave(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Save()
			return
		case <-t.C:
			s.Save()
		}
	}
}
