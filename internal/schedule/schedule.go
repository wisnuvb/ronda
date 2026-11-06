// Package schedule answers two questions about weekly maintenance windows: is one running now, and
// when does the next one start. A window starts at a wall-clock time ("02:00") in the program's time
// zone (model.Loc), so it stays at 02:00 across a daylight-saving change; its length is elapsed minutes.
package schedule

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

const (
	MaxWindows    = 7
	MaxWindowMins = 24 * 60
)

// ParseStart reads "HH:MM" into minutes after midnight.
func ParseStart(s string) (int, error) {
	var h, m int
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New(i18n.T("the start time must look like HH:MM"))
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, errors.New(i18n.T("the start time must look like HH:MM, between 00:00 and 23:59"))
	}
	return h*60 + m, nil
}

// Validate checks a list of windows.
func Validate(ws []model.Window) error {
	if len(ws) > MaxWindows {
		return fmt.Errorf("%s", i18n.T("at most %d maintenance windows", MaxWindows))
	}
	for i, w := range ws {
		if len(w.Days) == 0 || len(w.Days) > 7 {
			return fmt.Errorf("%s", i18n.T("window %d: choose at least one day", i+1))
		}
		seen := map[int]bool{}
		for _, d := range w.Days {
			if d < 0 || d > 6 || seen[d] {
				return fmt.Errorf("%s", i18n.T("window %d: days must be 0 (Sunday) to 6 (Saturday), without duplicates", i+1))
			}
			seen[d] = true
		}
		if _, err := ParseStart(w.Start); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("window %d", i+1), err)
		}
		if w.Minutes < 1 || w.Minutes > MaxWindowMins {
			return fmt.Errorf("%s", i18n.T("window %d: the duration must be 1..%d minutes", i+1, MaxWindowMins))
		}
	}
	return nil
}

// occurrences lists every start of w from the day before t until `days` days after it, in model.Loc.
// The day before is needed because a window that began yesterday evening may still be running.
func occurrences(w model.Window, t time.Time, days int) []time.Time {
	start, err := ParseStart(w.Start)
	if err != nil {
		return nil
	}
	midnight := model.Midnight(t)
	var out []time.Time
	for d := -1; d <= days; d++ {
		day := midnight.AddDate(0, 0, d)
		if slices.Contains(w.Days, int(day.Weekday())) {
			// By the wall clock, not midnight+start: on a 23 or 25 hour day the latter is an hour off.
			out = append(out, model.ClockOn(day, start))
		}
	}
	return out
}

// Active reports whether a window is running at t and when the last-ending running one ends. A run
// that ends at or before skipUntil (unix ms) was ended early by hand and is ignored.
func Active(ws []model.Window, t time.Time, skipUntil int64) (end time.Time, ok bool) {
	for _, w := range ws {
		for _, s := range occurrences(w, t, 0) {
			e := s.Add(time.Duration(w.Minutes) * time.Minute)
			if !t.Before(s) && t.Before(e) && e.UnixMilli() > skipUntil && e.After(end) {
				end, ok = e, true
			}
		}
	}
	return end, ok
}

// NextStart is the earliest window start strictly after t, skipping runs ended by hand.
func NextStart(ws []model.Window, t time.Time, skipUntil int64) (time.Time, bool) {
	var next time.Time
	found := false
	for _, w := range ws {
		for _, s := range occurrences(w, t, 8) {
			e := s.Add(time.Duration(w.Minutes) * time.Minute)
			if s.After(t) && e.UnixMilli() > skipUntil && (!found || s.Before(next)) {
				next, found = s, true
			}
		}
	}
	return next, found
}
