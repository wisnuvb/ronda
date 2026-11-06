package monitor

import (
	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/model"
)

// The monitor reaches the outside world only through the interfaces below, so the same checking engine can run
// on one machine with files (the single-tenant program) or behind a database for many accounts. Each interface is
// small on purpose; the file-based implementations in this module satisfy all of them.

// SiteSource is where the monitor reads the sites it must check.
type SiteSource interface {
	// List returns every site the monitor is responsible for. The monitor treats the result as a snapshot.
	List() []model.Site
	// Get returns one site, or model.ErrNotFound.
	Get(id string) (model.Site, error)
	// ByToken finds the push site that owns token.
	ByToken(token string) (model.Site, bool)
	// Changed receives a (coalesced) signal after every change to the list, so edits apply without a restart.
	Changed() <-chan struct{}
}

// History records check results and answers the questions the dashboard and the status page ask about them.
// Options.History is optional; leave it nil to keep no history.
type History interface {
	Record(id string, r model.Result)
	Delete(id string)
	Brief(id string) history.Stats
	Full(id string) history.Stats
	Daily(id string, n int) []history.Day
}

// EventLog is the archive of state changes (down, up, certificate) that incidents are built from.
type EventLog interface {
	Append(ev model.Event)
	// Recent returns up to n events, newest first.
	Recent(n int) []model.Event
	// All returns every event, oldest first.
	All() []model.Event
	Close() error
}

// StateStore keeps the part of each site's state that must survive a restart.
type StateStore interface {
	Load() map[string]model.SiteState
	Save(map[string]model.SiteState)
}

// fileState keeps the state in state.json in a directory; an empty directory keeps nothing.
type fileState struct{ dir string }

func (f fileState) Load() map[string]model.SiteState   { return loadState(f.dir) }
func (f fileState) Save(st map[string]model.SiteState) { writeJSON(f.dir, "state.json", st) }
