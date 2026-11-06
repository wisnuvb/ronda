package monitor

import (
	"testing"

	"github.com/wisnuvb/ronda/eventlog"
	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
)

// The file-based implementations are what the single-tenant program passes in. If one stops satisfying its
// interface, this stops compiling, which is the point: the interfaces are the seam another program builds on.
var (
	_ SiteSource = (*registry.Registry)(nil)
	_ History    = (*history.Store)(nil)
	_ EventLog   = (*eventlog.Log)(nil)
	_ StateStore = fileState{}
)

type memEvents struct{ evs []model.Event }

func (m *memEvents) Append(ev model.Event) { m.evs = append(m.evs, ev) }
func (m *memEvents) Recent(n int) []model.Event {
	out := []model.Event{}
	for i := len(m.evs) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, m.evs[i])
	}
	return out
}
func (m *memEvents) All() []model.Event { return m.evs }
func (m *memEvents) Close() error       { panic("the monitor must not close an event log it was given") }

type memState struct{ saved map[string]model.SiteState }

func (s *memState) Load() map[string]model.SiteState {
	return map[string]model.SiteState{"a": {Status: StDown}}
}
func (s *memState) Save(st map[string]model.SiteState) { s.saved = st }

// Storage that is not a file can be plugged in: injected stores are used, and the monitor leaves their lifecycle
// to the caller.
func TestInjectedEventLogAndStateAreUsedAndNotClosed(t *testing.T) {
	ev, st := &memEvents{}, &memState{}
	m := New(Options{Sites: nil, Checker: nil, Events: ev, State: st})
	if got := m.saved["a"].Status; got != StDown {
		t.Fatalf("state from the injected store was not loaded: %+v", m.saved)
	}
	m.emit(model.Event{State: "down", SiteName: "x"})
	if len(ev.evs) != 1 || len(m.Events()) != 1 {
		t.Fatalf("event did not go to the injected log: %+v", ev.evs)
	}
	if err := m.Close(); err != nil { // would panic if it closed the injected log
		t.Fatal(err)
	}
}
