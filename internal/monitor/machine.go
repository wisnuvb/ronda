// Package monitor schedules checks for every site and turns results into state
// changes: a site goes DOWN only after several consecutive failures, and comes
// back UP only after consecutive successes, so a single blip never pages anyone.
package monitor

const (
	StUnknown = "unknown" // no verdict yet
	StUp      = "up"
	StDown    = "down"
	StPaused  = "paused"
	StMaint   = "maintenance" // silenced until a set time

	recoverThreshold = 2 // consecutive successes needed to leave DOWN (checks)
)

type Transition int

const (
	NoChange  Transition = iota
	WentDown             // alert: site is down
	Recovered            // alert: site is back
	Held                 // failure threshold reached, but the monitor's own network looks dead
)

// Machine is the per-site state. It is not safe for concurrent use.
type Machine struct {
	Status    string
	Since     int64 // when the current Status began (DOWN: when the failures began)
	Fails     int   // consecutive failures while not DOWN
	Oks       int   // consecutive successes while DOWN
	FailStart int64 // time of the first failure of the current streak

	// RecoverAfter overrides recoverThreshold. A push site recovers on its first ping: a job that runs
	// once a day cannot be asked to prove itself twice.
	RecoverAfter int
}

func (m *Machine) recoverAfter() int {
	if m.RecoverAfter > 0 {
		return m.RecoverAfter
	}
	return recoverThreshold
}

// Observe feeds one check result in. now is the check's time in unix ms. netOK
// reports whether the monitor itself has internet access; when it does not, a
// failure streak is held back instead of declaring the site down, because the
// fault is most likely ours, not the site's.
func (m *Machine) Observe(ok bool, now int64, threshold int, netOK bool) Transition {
	if ok {
		m.Fails = 0
		switch m.Status {
		case StDown:
			m.Oks++
			if m.Oks >= m.recoverAfter() {
				m.Status, m.Since, m.Oks = StUp, now, 0
				return Recovered
			}
		case StUp:
		default:
			m.Status, m.Since = StUp, now
		}
		return NoChange
	}

	m.Oks = 0
	if m.Status == StDown {
		return NoChange
	}
	if m.Fails == 0 {
		m.FailStart = now
	}
	m.Fails++
	if m.Fails < threshold {
		return NoChange
	}
	if !netOK {
		m.Fails = threshold - 1
		m.FailStart = now
		return Held
	}
	m.Status, m.Since = StDown, m.FailStart
	return WentDown
}

// retrying reports whether the next check should come sooner than the normal
// interval: a failure streak is being confirmed, or a recovery is.
func (m *Machine) retrying() bool {
	return (m.Fails > 0 && m.Status != StDown) || (m.Status == StDown && m.Oks > 0)
}
