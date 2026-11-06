package eventlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/model"
)

func ev(at time.Time, state, name string) model.Event {
	return model.Event{Time: at.UnixMilli(), State: state, SiteName: name, SiteID: name}
}

func TestAppendReadAndPersist(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		l.Append(ev(base.Add(time.Duration(i)*time.Minute), "down", fmt.Sprint("s", i)))
	}
	if got := l.Recent(2); len(got) != 2 || got[0].SiteName != "s4" || got[1].SiteName != "s3" {
		t.Fatalf("Recent is newest first: %+v", got)
	}
	if got := l.All(); len(got) != 5 || got[0].SiteName != "s0" {
		t.Fatalf("All is oldest first: %+v", got)
	}
	l.Close()

	l2, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if l2.Len() != 5 || l2.Recent(1)[0].SiteName != "s4" {
		t.Fatalf("not persisted: %d", l2.Len())
	}
	if fi, _ := os.Stat(filepath.Join(dir, "events.jsonl")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestAppendCostsOneLineNotAFullRewrite(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, 0)
	defer l.Close()
	for i := 0; i < 50; i++ {
		l.Append(ev(time.Now(), "down", "a"))
	}
	path := filepath.Join(dir, "events.jsonl")
	before, _ := os.ReadFile(path)
	l.Append(ev(time.Now(), "up", "a"))
	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), string(before)) || strings.Count(string(after), "\n") != 51 {
		t.Fatal("an append must only add to the end of the file")
	}
}

func TestRetentionDropsOldEventsAndCompactsTheFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var lines []string
	for _, age := range []time.Duration{400 * 24 * time.Hour, 366 * 24 * time.Hour, 10 * 24 * time.Hour, time.Hour} {
		b, _ := json.Marshal(ev(now.Add(-age), "down", fmt.Sprint(age)))
		lines = append(lines, string(b))
	}
	os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o640)

	l, err := Open(dir, 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if l.Len() != 2 {
		t.Fatalf("kept %d, want the two inside the year", l.Len())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if strings.Count(string(b), "\n") != 2 {
		t.Fatalf("the file must be compacted to what is kept:\n%s", b)
	}
}

func TestCorruptLinesAreSkippedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	good, _ := json.Marshal(ev(time.Now(), "down", "ok"))
	os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(string(good)+"\n{rusak\n\n"+string(good)+"\n{\"state\":"), 0o640)

	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.Len() != 2 {
		t.Fatalf("both good lines must survive a bad one and a torn last line, got %d", l.Len())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if strings.Contains(string(b), "rusak") || strings.Count(string(b), "\n") != 2 {
		t.Fatalf("bad lines must be removed from the file:\n%s", b)
	}
}

func TestLegacyEventsJSONIsImportedOnce(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := []model.Event{ // newest first, as the old file kept them
		ev(now.Add(-time.Minute), "up", "a"),
		ev(now.Add(-time.Hour), "down", "a"),
	}
	b, _ := json.Marshal(old)
	os.WriteFile(filepath.Join(dir, "events.json"), b, 0o640)

	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.All(); len(got) != 2 || got[0].State != "down" || got[1].State != "up" {
		t.Fatalf("imported oldest first: %+v", got)
	}
	l.Close()
	if _, err := os.Stat(filepath.Join(dir, "events.json.imported")); err != nil {
		t.Fatal("the old file must be set aside so it is not imported again:", err)
	}
	l2, _ := Open(dir, 0)
	defer l2.Close()
	if l2.Len() != 2 {
		t.Fatalf("a second start must not import twice, got %d", l2.Len())
	}
}

func TestInMemoryWhenNoDirectory(t *testing.T) {
	l, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Append(ev(time.Now(), "down", "a"))
	if l.Len() != 1 || l.Close() != nil {
		t.Fatal("works without a directory")
	}
}

func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				l.Append(ev(time.Now(), "down", "a"))
			}
		}()
	}
	wg.Wait()
	l.Close()
	l2, _ := Open(dir, 0)
	defer l2.Close()
	if l2.Len() != 400 {
		t.Fatalf("lines were lost or torn under concurrency: %d of 400", l2.Len())
	}
}

func TestMemoryIsBounded(t *testing.T) {
	l, _ := Open("", 0)
	for i := 0; i < maxInMemory+500; i++ {
		l.Append(model.Event{Time: int64(i), State: "down"})
	}
	if l.Len() != maxInMemory || l.Recent(1)[0].Time != int64(maxInMemory+499) {
		t.Fatalf("len=%d", l.Len())
	}
}
