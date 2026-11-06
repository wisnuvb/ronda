package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

var t0 = time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)

func newStore(now *time.Time) *Store {
	s := Open("")
	s.now = func() time.Time { return *now }
	return s
}

func ok(at time.Time, ms int) model.Result {
	return model.Result{Time: at.UnixMilli(), OK: true, LatencyMs: ms}
}
func bad(at time.Time) model.Result { return model.Result{Time: at.UnixMilli()} }

func TestUptimeNeverRoundsAFailureAway(t *testing.T) {
	now := t0
	s := newStore(&now)
	for i := 0; i < 9999; i++ {
		s.Record("a", ok(now.Add(-time.Duration(i)*time.Second), 100))
	}
	s.Record("a", bad(now))

	got := *s.Full("a").Uptime24h
	if got != 99.99 {
		t.Fatalf("1 failure in 10000 = %v, want 99.99 (never 100)", got)
	}
}

func TestUptimeExactCases(t *testing.T) {
	now := t0
	s := newStore(&now)
	if st := s.Full("nope"); st.Uptime24h != nil {
		t.Fatal("no data must be nil, not 0 or 100")
	}
	s.Record("a", ok(now, 10))
	if *s.Full("a").Uptime24h != 100 {
		t.Fatal("all ok must be exactly 100")
	}
	s.Record("a", bad(now))
	if got := *s.Full("a").Uptime24h; got != 50 {
		t.Fatalf("1 of 2 = %v", got)
	}
}

func TestWindowsUseHourBuckets(t *testing.T) {
	now := t0
	s := newStore(&now)
	// 3 days ago: all failures. Today: all ok.
	for i := 0; i < 10; i++ {
		s.Record("a", bad(now.Add(-72*time.Hour+time.Duration(i)*time.Minute)))
		s.Record("a", ok(now.Add(-time.Duration(i)*time.Minute), 50))
	}
	st := s.Full("a")
	if *st.Uptime24h != 100 {
		t.Fatalf("24h = %v", *st.Uptime24h)
	}
	if *st.Uptime7d != 50 || *st.Uptime30d != 50 {
		t.Fatalf("7d=%v 30d=%v, want 50", *st.Uptime7d, *st.Uptime30d)
	}
}

func TestRawPrunedByAgeAndCount(t *testing.T) {
	now := t0
	s := newStore(&now)
	s.Record("a", ok(now.Add(-25*time.Hour), 1)) // already too old
	s.Record("a", ok(now, 2))
	d, _ := s.Series("a", "24h")
	if len(d.Raw) != 1 || d.Raw[0][1] != 2 {
		t.Fatalf("old point kept: %v", d.Raw)
	}

	s2 := newStore(&now)
	for i := 0; i < maxRaw+500; i++ {
		s2.Record("a", ok(now.Add(-time.Duration(maxRaw+500-i)*time.Second), i))
	}
	d, _ = s2.Series("a", "24h")
	if len(d.Raw) != maxRaw {
		t.Fatalf("raw len = %d, want cap %d", len(d.Raw), maxRaw)
	}
	if d.Raw[len(d.Raw)-1][1] != int64(maxRaw+499) {
		t.Fatal("the newest point must be kept")
	}
}

func TestOldHourlyBucketsPruned(t *testing.T) {
	now := t0
	s := newStore(&now)
	s.Record("a", ok(now.Add(-100*24*time.Hour), 1))
	s.Record("a", ok(now, 2))
	d, _ := s.Series("a", "90d")
	if len(d.Hourly) != 1 {
		t.Fatalf("hourly = %v", d.Hourly)
	}
}

func TestOutOfOrderResultsLandInTheRightBucket(t *testing.T) {
	now := t0
	s := newStore(&now)
	s.Record("a", ok(now, 10))
	s.Record("a", ok(now.Add(-2*time.Hour), 30))
	s.Record("a", ok(now.Add(-time.Hour), 20))
	d, _ := s.Series("a", "7d")
	if len(d.Hourly) != 3 || d.Hourly[0][1] != 30 || d.Hourly[1][1] != 20 || d.Hourly[2][1] != 10 {
		t.Fatalf("hourly must be ordered oldest first: %v", d.Hourly)
	}
}

func TestLatencyStats(t *testing.T) {
	now := t0
	s := newStore(&now)
	for i := 1; i <= 100; i++ {
		s.Record("a", ok(now.Add(-time.Duration(i)*time.Second), i)) // 1..100 ms
	}
	s.Record("a", bad(now))
	st := s.Full("a")
	if st.P95Ms != 95 || st.AvgMs != 50 || st.Checks24h != 101 {
		t.Fatalf("p95=%d avg=%d checks=%d", st.P95Ms, st.AvgMs, st.Checks24h)
	}
}

func TestBriefStrip(t *testing.T) {
	now := t0
	s := newStore(&now)
	for i := 0; i < 40; i++ {
		s.Record("a", ok(now.Add(time.Duration(i)*time.Second-time.Minute), i))
	}
	s.Record("a", bad(now))
	b := s.Brief("a")
	if len(b.Strip) != stripLen || b.Strip[stripLen-1] != -1 || b.Strip[stripLen-2] != 39 {
		t.Fatalf("strip = %v", b.Strip)
	}
}

func TestSeriesRanges(t *testing.T) {
	now := t0
	s := newStore(&now)
	s.Record("a", ok(now.Add(-30*time.Minute), 5))
	s.Record("a", ok(now.Add(-3*time.Hour), 6))
	s.Record("a", bad(now))

	if d, _ := s.Series("a", "1h"); len(d.Raw) != 2 {
		t.Fatalf("1h = %v", d.Raw)
	}
	if d, _ := s.Series("a", "6h"); len(d.Raw) != 3 {
		t.Fatalf("6h = %v", d.Raw)
	}
	d, _ := s.Series("a", "7d")
	if len(d.Raw) != 0 || len(d.Hourly) == 0 {
		t.Fatalf("7d must be hourly: %+v", d)
	}
	if _, err := s.Series("a", "5y"); err == nil {
		t.Fatal("unknown range must error")
	}
	if d, err := s.Series("tidak-ada", "24h"); err != nil || len(d.Raw) != 0 {
		t.Fatalf("unknown site: %+v %v", d, err)
	}
}

func TestDelete(t *testing.T) {
	now := t0
	s := newStore(&now)
	s.Record("a", ok(now, 1))
	s.Delete("a")
	if s.Full("a").Uptime24h != nil {
		t.Fatal("history must be gone")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s := Open(dir)
	for i := 0; i < 20; i++ {
		s.Record("a", ok(now.Add(-time.Duration(i)*time.Minute), 100+i))
	}
	s.Record("a", bad(now))
	want := s.Full("a")
	s.Save()

	s2 := Open(dir)
	got := s2.Full("a")
	if *got.Uptime24h != *want.Uptime24h || got.AvgMs != want.AvgMs || got.Checks24h != 21 {
		t.Fatalf("reloaded %+v, want %+v", got, want)
	}
	if d, _ := s2.Series("a", "24h"); len(d.Raw) != 21 {
		t.Fatalf("raw after reload: %d", len(d.Raw))
	}
}

func TestCorruptFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "history.json"), []byte("{rusak"), 0o640)
	s := Open(dir)
	s.Record("a", ok(time.Now(), 1)) // must still work
	if s.Full("a").Uptime24h == nil {
		t.Fatal("store unusable after a corrupt file")
	}
}

func TestAutoSaveFlushesOnCancel(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	s.Record("a", ok(time.Now(), 1))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.AutoSave(ctx, time.Hour); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoSave did not return")
	}
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err != nil {
		t.Fatal("final save missing:", err)
	}
}

// ---- daily rollups for the public page ----

func TestDailyGroupsByWIBDayAndMarksEmptyDays(t *testing.T) {
	// 11:00 WIB on Thursday 5 Nov 2026 = 04:00 UTC.
	now := time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)
	s := newStore(&now)
	wib := func(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 30, 0, 0, jakarta) }

	s.Record("a", ok(wib(5, 9), 10))
	s.Record("a", ok(wib(5, 10), 10))
	s.Record("a", bad(wib(5, 10)))    // today: 2 of 3
	s.Record("a", ok(wib(3, 23), 10)) // 23:30 WIB on the 3rd...
	s.Record("a", ok(wib(4, 0), 10))  // ...and 00:30 WIB on the 4th are different days, an hour apart
	s.Record("a", bad(wib(4, 0)))

	d := s.Daily("a", 4) // 2, 3, 4, 5 Nov
	if len(d) != 4 {
		t.Fatalf("len = %d", len(d))
	}
	if d[0].Total != 0 || d[1].Up != 1 || d[1].Total != 1 {
		t.Fatalf("the 2nd must be empty and the 3rd hold one check: %+v", d)
	}
	if d[2].Up != 1 || d[2].Total != 2 {
		t.Fatalf("00:30 WIB belongs to the 4th, not the 3rd: %+v", d[2])
	}
	if d[3].Up != 2 || d[3].Total != 3 {
		t.Fatalf("today: %+v", d[3])
	}
	midnight := time.Date(2026, 11, 5, 0, 0, 0, 0, jakarta).UnixMilli()
	if d[3].Start != midnight || d[2].Start != midnight-24*3600*1000 {
		t.Fatalf("days start at 00:00 WIB: %v", d[3].Start)
	}
}

func TestDailyForUnknownSiteIsAllEmptyAndIgnoresOldData(t *testing.T) {
	now := time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)
	s := newStore(&now)
	for _, d := range s.Daily("tidak-ada", 90) {
		if d.Total != 0 {
			t.Fatal("no data must read as empty days")
		}
	}
	s.Record("a", ok(now.Add(-100*24*time.Hour), 1)) // older than the window
	total := 0
	for _, d := range s.Daily("a", 90) {
		total += d.Total
	}
	if total != 0 {
		t.Fatalf("a result older than the window must not leak into it: %d", total)
	}
}

// ---- saving a big history ----

func TestSaveWhileRecordingAlwaysWritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	for i := 0; i < 30; i++ {
		for k := 0; k < 200; k++ {
			s.Record(fmt.Sprint("s", i), ok(time.Now().Add(-time.Duration(k)*time.Minute), 10+k))
		}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { // a monitor busy recording while the periodic save runs
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				s.Record(fmt.Sprint("s", i%35), ok(time.Now(), 5)) // sites 30-34 appear mid-save
			}
		}
	}()
	for i := 0; i < 25; i++ {
		s.Save()
		b, err := os.ReadFile(filepath.Join(dir, "history.json"))
		if err != nil || !json.Valid(b) {
			t.Fatalf("save %d produced an unreadable file (err=%v)", i, err)
		}
	}
	close(stop)
	<-done
	s.Save()

	reopened := Open(dir)
	if reopened.Full("s0").Uptime24h == nil || reopened.Full("s29").Uptime24h == nil {
		t.Fatal("the saved history must reload")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestStaleTempFilesAreSweptOnOpen(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	s.Record("a", ok(time.Now(), 1))
	s.Save()
	stale := filepath.Join(dir, tempPrefix+"123456")
	os.WriteFile(stale, []byte(`{"sites":{"a":{"raw":[[1`), 0o640) // a crash halfway through a save

	got := Open(dir)
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("the half-written temp file must be removed")
	}
	if got.Full("a").Uptime24h == nil {
		t.Fatal("and the good file must be untouched")
	}
}

func TestListStatsSkipThePercentileOnlyDetailComputesIt(t *testing.T) {
	now := t0
	s := newStore(&now)
	for i := 1; i <= 50; i++ {
		s.Record("a", ok(now.Add(-time.Duration(i)*time.Second), i*10))
	}
	if b := s.Brief("a"); b.P95Ms != 0 || b.AvgMs == 0 {
		t.Fatalf("Brief runs for every site on every refresh: no sorting, but the average stays: %+v", b)
	}
	if f := s.Full("a"); f.P95Ms == 0 || f.AvgMs == 0 {
		t.Fatalf("Full carries the percentile: %+v", f)
	}
}

func BenchmarkBriefFullHistory(b *testing.B) {
	now := time.Now()
	s := Open("")
	for k := 0; k < 2880; k++ {
		s.Record("a", ok(now.Add(-time.Duration(k)*30*time.Second), 50+k%200))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Brief("a")
	}
}

func TestSeriesRangeErrorIsTranslated(t *testing.T) {
	now := time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)
	s := newStore(&now)
	if _, err := s.Series("a", "2h"); err == nil || err.Error() != "range must be one of 1h, 6h, 24h, 7d, 30d, 90d" {
		t.Fatalf("english: %v", err)
	}
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	if _, err := s.Series("a", "2h"); err == nil || err.Error() != "range harus salah satu dari 1h, 6h, 24h, 7d, 30d, 90d" {
		t.Fatalf("indonesian: %v", err)
	}
}
