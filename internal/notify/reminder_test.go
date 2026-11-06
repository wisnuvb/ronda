package notify

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

func reminderEvent(name string, downSec int64) model.Event {
	return model.Event{
		State: "reminder", SiteName: name, URL: "https://" + name + ".example", Kind: model.KindRefused,
		Hint: "port 443 <ditolak>", Since: time.Date(2026, 11, 3, 9, 30, 0, 0, jakarta).UnixMilli(), DowntimeSec: downSec,
	}
}

func TestReminderMessageSaysHowLongSinceWhenAndWhyAndEscapes(t *testing.T) {
	html := FormatHTML(reminderEvent("Toko & Co", 26*3600+15*60))
	for _, want := range []string{"STILL DOWN", "Toko &amp; Co", "1 d 2 h", "2026-11-03 09:30:00 WIB", "port 443 &lt;ditolak&gt;", "Cause now"} {
		if !strings.Contains(html, want) {
			t.Errorf("reminder HTML lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "test notification") {
		t.Fatalf("an unknown state must never fall through to the test message:\n%s", html)
	}
	text := FormatText(reminderEvent("Toko", 3600))
	if !strings.HasPrefix(text, "[STILL DOWN] Toko") || strings.Contains(text, "[TEST]") || !strings.Contains(text, "1 h 0 min") {
		t.Fatalf("plain text: %q", text)
	}
}

func TestReminderForAPushSiteHasNoMeaninglessAddress(t *testing.T) {
	e := reminderEvent("Backup", 7200)
	e.URL = "push://abc123"
	if html := FormatHTML(e); strings.Contains(html, "push://") {
		t.Fatalf("a push site's internal label is not an address worth showing:\n%s", html)
	}
}

func TestSeveralRemindersBecomeOneDigest(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 80*time.Millisecond, 2*time.Second, 3)
	defer q.Close(2 * time.Second)
	for i := 0; i < 5; i++ {
		q.Notify(ev("reminder", fmt.Sprint("s", i)))
	}
	settle(t, d, 1)
	time.Sleep(150 * time.Millisecond)
	if got := d.snapshot(); len(got) != 1 || got[0] != "N5:reminder" {
		t.Fatalf("a server down for hours must not send one reminder per site: %v", got)
	}
}

func TestReminderDigestDoesNotSwallowADownOrReorderIt(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 80*time.Millisecond, 2*time.Second, 3)
	defer q.Close(2 * time.Second)
	q.Notify(ev("reminder", "a"))
	q.Notify(ev("reminder", "b"))
	q.Notify(ev("down", "c"))
	q.Notify(ev("reminder", "d"))
	got := settle(t, d, 4)
	want := []string{"1:reminder:a", "1:reminder:b", "1:down:c", "1:reminder:d"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("only consecutive runs merge and order is kept: %v, want %v", got, want)
	}
}

func TestReminderDigestText(t *testing.T) {
	evs := []model.Event{reminderEvent("A", 3600), reminderEvent("B", 7200), reminderEvent("C", 90000)}
	html := FormatDigestHTML("reminder", evs)
	for _, want := range []string{"3 sites STILL DOWN", "A — Connection refused, down for 1 h 0 min", "C — Connection refused, down for 1 d 1 h", "2026-11-03 09:30:00 WIB"} {
		if !strings.Contains(html, want) {
			t.Errorf("digest HTML lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "RECOVERED") || strings.Contains(html, "at about the same time") {
		t.Fatalf("a reminder digest must not read like a recovery or a fresh outage:\n%s", html)
	}
	if text := FormatDigestText("reminder", evs); !strings.HasPrefix(text, "[STILL DOWN] 3 sites have not recovered") {
		t.Fatalf("plain digest: %q", text)
	}
}
