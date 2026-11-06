package report

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/schedule"
)

// Schedule says when reports go out, in the program's time zone (model.Loc). An empty time means that kind is off.
type Schedule struct {
	Daily     string // "HH:MM"
	Weekly    string // "HH:MM"
	WeeklyDay int    // time.Weekday: 0 = Sunday ... 6 = Saturday
}

// Enabled reports whether any report is scheduled.
func (s Schedule) Enabled() bool { return s.Daily != "" || s.Weekly != "" }

// Validate checks the times; the day is only looked at when a weekly report is on.
func (s Schedule) Validate() error {
	for _, t := range []string{s.Daily, s.Weekly} {
		if t == "" {
			continue
		}
		if _, err := schedule.ParseStart(t); err != nil {
			return err
		}
	}
	return nil
}

// catchUp is how late a report may still be sent after its slot, for the case where ronda was off or
// restarting at that moment. Later than this it is skipped: a "daily" report that arrives at midnight is noise.
const catchUp = 3 * time.Hour

// slot is the latest scheduled moment that is not after now.
func slot(now time.Time, kind Kind, hhmm string, weekday int) time.Time {
	mins, _ := schedule.ParseStart(hhmm)
	w := model.In(now)
	t := model.ClockOn(w, mins)
	if t.After(w) {
		t = t.AddDate(0, 0, -1)
	}
	if kind == Weekly {
		for int(t.Weekday()) != weekday {
			t = t.AddDate(0, 0, -1)
		}
	}
	return t
}

// Scheduler sends reports on time. It remembers which slot it last served in dir/reports.json, so a restart
// neither repeats a report nor loses one that fell due while it was down (within catchUp).
type Scheduler struct {
	Schedule Schedule
	Dir      string
	Now      func() time.Time
	Tick     time.Duration
	// Send delivers one report. It is called from the scheduler's goroutine, never concurrently.
	Send func(Kind)

	mu   sync.Mutex
	last map[Kind]int64
}

func (s *Scheduler) path() string { return filepath.Join(s.Dir, "reports.json") }

func (s *Scheduler) load() {
	s.last = map[Kind]int64{}
	if s.Dir == "" {
		return
	}
	if b, err := os.ReadFile(s.path()); err == nil {
		_ = json.Unmarshal(b, &s.last)
		if s.last == nil {
			s.last = map[Kind]int64{}
		}
		if s.migrate() {
			s.save()
		}
	}
}

// legacyKinds are the keys older versions wrote to reports.json, when the kinds were named in Indonesian.
var legacyKinds = map[Kind]Kind{"harian": Daily, "mingguan": Weekly}

// migrate moves the last-served slot of a legacy key to its current key. Dropping it would make the next
// start look like the first time a kind is seen, or resend a report that already went out. If both keys
// are present the later slot wins. It reports whether anything changed.
func (s *Scheduler) migrate() bool {
	changed := false
	for old, cur := range legacyKinds {
		v, ok := s.last[old]
		if !ok {
			continue
		}
		if prev, has := s.last[cur]; !has || v > prev {
			s.last[cur] = v
		}
		delete(s.last, old)
		changed = true
	}
	return changed
}

func (s *Scheduler) save() {
	if s.Dir == "" {
		return
	}
	b, _ := json.Marshal(s.last)
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err == nil {
		if err := os.Rename(tmp, s.path()); err != nil {
			log.Printf("report: could not save %s: %v", s.path(), err)
		}
	}
}

// Due returns the kinds to send at now and records them as served. The first time a kind is seen it is only
// marked, not sent: switching reports on at 08:30 must not fire the 08:00 report at once.
func (s *Scheduler) Due(now time.Time) []Kind {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.load()
	}
	var out []Kind
	changed := false
	for _, k := range []struct {
		kind Kind
		at   string
	}{{Daily, s.Schedule.Daily}, {Weekly, s.Schedule.Weekly}} {
		if k.at == "" {
			continue
		}
		sl := slot(now, k.kind, k.at, s.Schedule.WeeklyDay).UnixMilli()
		prev, seen := s.last[k.kind]
		if seen && sl <= prev {
			continue
		}
		s.last[k.kind] = sl
		changed = true
		if seen && now.UnixMilli()-sl <= catchUp.Milliseconds() {
			out = append(out, k.kind)
		}
	}
	if changed {
		s.save()
	}
	return out
}

// Run checks the clock every Tick (default 20 s) until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	if s.Now == nil {
		s.Now = time.Now
	}
	tick := s.Tick
	if tick <= 0 {
		tick = 20 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		for _, k := range s.Due(s.Now()) {
			if ctx.Err() != nil {
				return
			}
			s.Send(k)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
