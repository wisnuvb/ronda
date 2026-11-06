package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

func downEvent() model.Event {
	return model.Event{
		Time: time.Date(2026, 11, 5, 3, 42, 11, 0, time.UTC).UnixMilli(), State: "down",
		SiteName: "Toko <A>", URL: "https://toko.example/?a=1&b=2", Fails: 3,
		Since: time.Date(2026, 11, 5, 3, 41, 41, 0, time.UTC).UnixMilli(),
		Hint:  "Cloudflare 521: origin menolak koneksi.",
	}
}

func TestFormatHTMLEscapesAndUsesWIB(t *testing.T) {
	s := FormatHTML(downEvent())
	for _, want := range []string{"<b>DOWN</b>", "Toko &lt;A&gt;", "a=1&amp;b=2", "Cloudflare 521", "Failed 3 times", "10:41:41 WIB"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<A>") {
		t.Errorf("site name not escaped:\n%s", s)
	}

	up := FormatHTML(model.Event{State: "up", SiteName: "Toko", URL: "https://t", DowntimeSec: 252, LatencyMs: 231})
	if !strings.Contains(up, "RECOVERED") || !strings.Contains(up, "4 min 12 s") || !strings.Contains(up, "231 ms") {
		t.Errorf("up message:\n%s", up)
	}
}

func TestDuration(t *testing.T) {
	for sec, want := range map[int64]string{
		0: "0 s", 59: "59 s", 60: "1 min 0 s", 252: "4 min 12 s",
		3600: "1 h 0 min", 7500: "2 h 5 min", 90000: "1 d 1 h",
	} {
		if got := Duration(sec); got != want {
			t.Errorf("Duration(%d) = %q, want %q", sec, got, want)
		}
	}
}

func fastTelegram(url string, chats ...string) *Telegram {
	tg := NewTelegram("123:SECRET", chats)
	tg.BaseURL = url
	tg.Sleep = func(time.Duration) {}
	return tg
}

func TestTelegramSendsToEveryChat(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:SECRET/sendMessage" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		got[p["chat_id"].(string)] = p["parse_mode"].(string)
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if err := fastTelegram(srv.URL, "111", "-100222").Send(downEvent()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["111"] != "HTML" || got["-100222"] != "HTML" {
		t.Fatalf("got %v", got)
	}
}

func TestTelegramRetriesOn429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	var slept []time.Duration
	tg := fastTelegram(srv.URL, "1")
	tg.Sleep = func(d time.Duration) { slept = append(slept, d) }
	if err := tg.Send(downEvent()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(slept) != 1 || slept[0] != 7*time.Second {
		t.Fatalf("calls=%d slept=%v", calls.Load(), slept)
	}
}

func TestTelegramDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(400)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()

	err := fastTelegram(srv.URL, "1").Send(downEvent())
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, a 400 must not be retried", calls.Load())
	}
}

func TestTelegramRetriesServerErrorsThenGivesUp(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(502)
	}))
	defer srv.Close()

	if err := fastTelegram(srv.URL, "1").Send(downEvent()); err == nil {
		t.Fatal("want error")
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestTelegramErrorNeverContainsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused; Go's error text would echo the URL, which holds the token

	err := fastTelegram(url, "1").Send(downEvent())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("token leaked: %v", err)
	}
}

type recorder struct {
	mu  sync.Mutex
	got []model.Event
}

func (r *recorder) Notify(ev model.Event) { r.mu.Lock(); r.got = append(r.got, ev); r.mu.Unlock() }

func TestQueueDeliversInOrderAndFlushesOnClose(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(rec, 0)
	for i := 0; i < 20; i++ {
		q.Notify(model.Event{SiteName: "s", Fails: i})
	}
	q.Close(2 * time.Second)

	if len(rec.got) != 20 {
		t.Fatalf("delivered %d of 20", len(rec.got))
	}
	for i, ev := range rec.got {
		if ev.Fails != i {
			t.Fatalf("out of order at %d: %d", i, ev.Fails)
		}
	}
}

func TestWebhookPostsJSON(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()

	NewWebhook(srv.URL).Notify(downEvent())
	if body["text"] == nil || body["event"] == nil || !strings.Contains(body["text"].(string), "[DOWN]") {
		t.Fatalf("body = %v", body)
	}
}

func TestFormatCertificateWarning(t *testing.T) {
	ev := model.Event{State: "cert", SiteName: "Toko <A>", URL: "https://toko.example", DaysLeft: 7,
		CertNotAfter: time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC).UnixMilli(), Hint: "Perpanjang sertifikat."}
	html := FormatHTML(ev)
	for _, want := range []string{"CERTIFICATE", "Toko &lt;A&gt;", "<b>7 days</b>", "2026-11-12 WIB", "Perpanjang sertifikat."} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if txt := FormatText(ev); !strings.Contains(txt, "[CERTIFICATE]") || !strings.Contains(txt, "7 days") {
		t.Errorf("text form: %s", txt)
	}
	if got := FormatHTML(model.Event{State: "cert", DaysLeft: 0}); !strings.Contains(got, "less than 1 day") {
		t.Errorf("0 days must not read as '0 days': %s", got)
	}
}

// ---- batching and digests ----

// digestRec records what the queue delivered, as one ordered log of "singles" and "digests".
type digestRec struct {
	mu  sync.Mutex
	log []string
}

func (d *digestRec) Notify(ev model.Event) {
	d.mu.Lock()
	d.log = append(d.log, "1:"+ev.State+":"+ev.SiteName)
	d.mu.Unlock()
}

func (d *digestRec) NotifyDigest(state string, evs []model.Event) {
	d.mu.Lock()
	d.log = append(d.log, fmt.Sprintf("N%d:%s", len(evs), state))
	d.mu.Unlock()
}

func (d *digestRec) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.log...)
}

func ev(state, name string) model.Event { return model.Event{State: state, SiteName: name} }

func settle(t *testing.T, d *digestRec, want int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := d.snapshot(); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited for %d deliveries, got %v", want, d.snapshot())
	return nil
}

func newBatching(d Notifier, gap, max time.Duration, min int) *Queue {
	return NewQueueWith(d, QueueOptions{BatchGap: gap, BatchMax: max, DigestMin: min})
}

func TestBurstBecomesOneDigest(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 80*time.Millisecond, 2*time.Second, 3)
	defer q.Close(2 * time.Second)
	for i := 0; i < 7; i++ {
		q.Notify(ev("down", fmt.Sprint("s", i)))
	}
	got := settle(t, d, 1)
	time.Sleep(150 * time.Millisecond)
	if got = d.snapshot(); len(got) != 1 || got[0] != "N7:down" {
		t.Fatalf("seven sites down together must be one message, got %v", got)
	}
}

func TestFewEventsStayIndividual(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 50*time.Millisecond, time.Second, 3)
	defer q.Close(time.Second)
	q.Notify(ev("down", "a"))
	q.Notify(ev("down", "b"))
	got := settle(t, d, 2)
	if got[0] != "1:down:a" || got[1] != "1:down:b" {
		t.Fatalf("below the minimum each event is its own message, in order: %v", got)
	}
}

func TestQuietGapExtendsWhileEventsKeepArriving(t *testing.T) {
	// Events 40ms apart never leave a 70ms quiet gap, so they stay one burst.
	d := &digestRec{}
	q := newBatching(d, 70*time.Millisecond, 2*time.Second, 3)
	defer q.Close(2 * time.Second)
	for i := 0; i < 6; i++ {
		q.Notify(ev("down", fmt.Sprint("s", i)))
		time.Sleep(40 * time.Millisecond)
	}
	settle(t, d, 1)
	time.Sleep(150 * time.Millisecond)
	if got := d.snapshot(); len(got) != 1 || got[0] != "N6:down" {
		t.Fatalf("a trickle that never goes quiet is still one burst: %v", got)
	}
}

func TestBatchMaxStopsAnEndlessBurst(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 100*time.Millisecond, 250*time.Millisecond, 3)
	defer q.Close(2 * time.Second)
	stop := time.After(900 * time.Millisecond)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	i := 0
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-tick.C:
			q.Notify(ev("down", fmt.Sprint("s", i)))
			i++
		}
	}
	if got := settle(t, d, 2); len(got) < 2 {
		t.Fatalf("a burst that never ends must still be reported before BatchMax, got %v", got)
	}
}

func TestOrderIsPreservedAndOnlyConsecutiveRunsMerge(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 60*time.Millisecond, time.Second, 3)
	for _, e := range []model.Event{
		ev("down", "a"), ev("down", "b"), ev("down", "c"), // run of 3: digest
		ev("up", "a"), ev("up", "b"), // run of 2: singles
		ev("down", "a"), // a goes down again after recovering
	} {
		q.Notify(e)
	}
	q.Close(2 * time.Second)
	want := []string{"N3:down", "1:up:a", "1:up:b", "1:down:a"}
	got := d.snapshot()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v: a site's down must never be reordered after its up", got, want)
	}
}

func TestCertAndTestEventsAreNeverDigested(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 50*time.Millisecond, time.Second, 2)
	for i := 0; i < 4; i++ {
		q.Notify(ev("cert", fmt.Sprint("c", i)))
	}
	q.Close(2 * time.Second)
	if got := d.snapshot(); len(got) != 4 || got[0] != "1:cert:c0" {
		t.Fatalf("certificate warnings are about different sites and dates, so they stay separate: %v", got)
	}
}

func TestCloseDoesNotWaitForTheQuietGap(t *testing.T) {
	d := &digestRec{}
	q := newBatching(d, 10*time.Second, time.Minute, 3)
	for i := 0; i < 4; i++ {
		q.Notify(ev("down", fmt.Sprint("s", i)))
	}
	start := time.Now()
	q.Close(5 * time.Second)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("shutdown waited %v for a gap that can no longer be filled", time.Since(start))
	}
	if got := d.snapshot(); len(got) != 1 || got[0] != "N4:down" {
		t.Fatalf("what was pending must still be delivered on shutdown: %v", got)
	}
}

func TestNotifierWithoutDigestSupportGetsEveryEvent(t *testing.T) {
	rec := &recorder{}
	q := newBatching(rec, 30*time.Millisecond, time.Second, 3)
	for i := 0; i < 5; i++ {
		q.Notify(ev("down", fmt.Sprint("s", i)))
	}
	q.Close(2 * time.Second)
	if len(rec.got) != 5 {
		t.Fatalf("a plain notifier must receive all 5 events, got %d", len(rec.got))
	}
}

func TestMultiSplitsDigestForPlainNotifiers(t *testing.T) {
	d := &digestRec{}
	plain := &recorder{}
	Multi{d, plain}.NotifyDigest("down", []model.Event{ev("down", "a"), ev("down", "b"), ev("down", "c")})
	if got := d.snapshot(); len(got) != 1 || got[0] != "N3:down" {
		t.Fatalf("the digest-aware notifier gets one digest: %v", got)
	}
	if len(plain.got) != 3 {
		t.Fatalf("the plain notifier gets the three events: %d", len(plain.got))
	}
}

func TestBatchingOffDeliversImmediately(t *testing.T) {
	d := &digestRec{}
	q := NewQueue(d, 0)
	defer q.Close(time.Second)
	for i := 0; i < 5; i++ {
		q.Notify(ev("down", fmt.Sprint("s", i)))
	}
	if got := settle(t, d, 5); got[0] != "1:down:s0" {
		t.Fatalf("with batching off nothing is held back or merged: %v", got)
	}
}

func TestDigestMessages(t *testing.T) {
	down := make([]model.Event, 20)
	for i := range down {
		down[i] = model.Event{State: "down", SiteName: fmt.Sprintf("Site <%d>", i), Kind: model.KindRefused,
			Since: time.Date(2026, 11, 5, 3, 41, 41+i, 0, time.UTC).UnixMilli()}
	}
	html := FormatDigestHTML("down", down)
	for _, want := range []string{"<b>20 sites DOWN at about the same time</b>", "Site &lt;0&gt; — Connection refused", "… and 5 more", "the likely cause is shared", "10:41:41 WIB"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<0>") || strings.Count(html, "• ") != digestLines {
		t.Errorf("names must be escaped and the list capped at %d:\n%s", digestLines, html)
	}
	if len(html) > 4000 {
		t.Errorf("must fit Telegram's 4096-character limit, got %d", len(html))
	}

	up := FormatDigestHTML("up", []model.Event{{State: "up", SiteName: "A", DowntimeSec: 252}, {State: "up", SiteName: "B", DowntimeSec: 30}})
	if !strings.Contains(up, "2 sites RECOVERED") || !strings.Contains(up, "A — was down for 4 min 12 s") || !strings.Contains(up, "B — was down for 30 s") {
		t.Errorf("up digest:\n%s", up)
	}
	if txt := FormatDigestText("down", down); !strings.Contains(txt, "[DOWN] 20 sites") || strings.Contains(txt, "&lt;") || strings.Contains(txt, "<b>") {
		t.Errorf("text form:\n%s", txt)
	}
}

func TestTelegramSendsDigestToEveryChat(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		texts = append(texts, p["text"].(string))
		mu.Unlock()
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	fastTelegram(srv.URL, "1", "2").NotifyDigest("down", []model.Event{ev("down", "a"), ev("down", "b"), ev("down", "c")})
	if len(texts) != 2 || !strings.Contains(texts[0], "3 sites DOWN") {
		t.Fatalf("one digest per chat, not one message per site: %v", texts)
	}
}

func TestWebhookDigestPostsOneDocument(t *testing.T) {
	var body map[string]any
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()
	NewWebhook(srv.URL).NotifyDigest("down", []model.Event{ev("down", "a"), ev("down", "b"), ev("down", "c")})
	if calls != 1 || body["state"] != "down" || len(body["events"].([]any)) != 3 || !strings.Contains(body["text"].(string), "[DOWN] 3 sites") {
		t.Fatalf("calls=%d body=%v", calls, body)
	}
}
