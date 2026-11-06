// Package eventlog is the permanent record of down/up/certificate events. It is an append-only JSON
// Lines file: a new event costs one small write instead of rewriting everything, and a crash can
// only ever damage the last line. Incidents are built from it, so its length is the history's length.
package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/wisnuvb/ronda/model"
)

const (
	fileName   = "events.jsonl"
	legacyName = "events.json"
	// maxInMemory bounds memory on a monitor that has been running for years. The file is trimmed to the
	// retention period at the next start.
	maxInMemory = 100_000
)

type Log struct {
	dir string
	now func() time.Time

	mu     sync.Mutex
	events []model.Event // oldest first
	f      *os.File
}

// Open reads dir/events.jsonl (dir "" keeps everything in memory), drops events older than
// retention, and rewrites the file if that removed anything. A first start after an upgrade imports the
// old events.json. Unreadable lines are skipped and counted, never fatal: history is worth less than uptime.
func Open(dir string, retention time.Duration) (*Log, error) {
	l := &Log{dir: dir, now: time.Now}
	if dir == "" {
		return l, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	rewrite := l.load(retention)
	if rewrite {
		if err := l.rewriteLocked(); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, fileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	l.f = f
	return l, nil
}

// load fills l.events and reports whether the file on disk should be rewritten.
func (l *Log) load(retention time.Duration) (rewrite bool) {
	cutoff := int64(0)
	if retention > 0 {
		cutoff = l.now().Add(-retention).UnixMilli()
	}
	path := filepath.Join(l.dir, fileName)

	f, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		rewrite = l.importLegacy()
	case err != nil:
		log.Printf("eventlog: cannot read %s, starting empty: %v", fileName, err)
	default:
		defer f.Close()
		bad := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			var ev model.Event
			if len(sc.Bytes()) == 0 {
				continue
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				bad++
				continue
			}
			l.events = append(l.events, ev)
		}
		if bad > 0 {
			log.Printf("eventlog: skipped %d corrupt lines in %s; they will be dropped", bad, fileName)
			rewrite = true
		}
	}

	if cutoff > 0 {
		before := len(l.events)
		l.events = slices.DeleteFunc(l.events, func(e model.Event) bool { return e.Time < cutoff })
		rewrite = rewrite || len(l.events) != before
	}
	slices.SortStableFunc(l.events, func(a, b model.Event) int { return int(a.Time - b.Time) })
	return rewrite
}

// importLegacy reads the pre-archive events.json (newest first, capped at 300) once.
func (l *Log) importLegacy() bool {
	path := filepath.Join(l.dir, legacyName)
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var old []model.Event
	if err := json.Unmarshal(b, &old); err != nil {
		log.Printf("eventlog: old %s is corrupt, ignored: %v", legacyName, err)
		return false
	}
	slices.Reverse(old)
	l.events = old
	_ = os.Rename(path, path+".imported")
	log.Printf("eventlog: imported %d events from %s into %s", len(old), legacyName, fileName)
	return true
}

// rewriteLocked replaces the file with exactly l.events, atomically.
func (l *Log) rewriteLocked() error {
	path := filepath.Join(l.dir, fileName)
	tmp, err := os.CreateTemp(l.dir, fileName+".tmp-*")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	for _, ev := range l.events {
		if err := enc.Encode(ev); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Append records one event.
func (l *Log) Append(ev model.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
	if len(l.events) > maxInMemory {
		l.events = slices.Delete(l.events, 0, len(l.events)-maxInMemory)
	}
	if l.f == nil {
		return
	}
	b, err := json.Marshal(ev)
	if err == nil {
		_, err = l.f.Write(append(b, '\n'))
	}
	if err != nil {
		log.Printf("eventlog: failed to write %s: %v", fileName, err)
	}
}

// Recent returns up to n events, newest first.
func (l *Log) Recent(n int) []model.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.events) {
		n = len(l.events)
	}
	out := make([]model.Event, 0, n)
	for i := len(l.events) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.events[i])
	}
	return out
}

// All returns every event, oldest first.
func (l *Log) All() []model.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// Len is the number of events kept.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// Close flushes and closes the file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
