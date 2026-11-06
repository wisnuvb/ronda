// Package integration holds tests that wire several real packages together: the registry, the monitor with its
// real scheduler, the notification queue and the Telegram client, against a fake Telegram server.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/monitor"
	"github.com/wisnuvb/ronda/notify"
)

type message struct{ Chat, Text string }

type fakeTelegram struct {
	mu   sync.Mutex
	msgs []message
	srv  *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		f.msgs = append(f.msgs, message{p["chat_id"].(string), p["text"].(string)})
		f.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) to(chat string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.msgs {
		if m.Chat == chat {
			out = append(out, m.Text)
		}
	}
	return out
}

// switchable fails the sites whose names are in down.
type switchable struct {
	mu   sync.Mutex
	down map[string]bool
}

func (s *switchable) set(name string, v bool) { s.mu.Lock(); s.down[name] = v; s.mu.Unlock() }
func (s *switchable) Check(_ context.Context, site model.Site) model.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down[site.Name] {
		return model.Result{Time: time.Now().UnixMilli(), Kind: model.KindRefused, Hint: "mati"}
	}
	return model.Result{Time: time.Now().UnixMilli(), OK: true, Status: 200, LatencyMs: 5, Kind: model.KindOK}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEachClientHearsOnlyAboutItsOwnSitesThroughTheWholePipeline(t *testing.T) {
	fake := newFakeTelegram(t)
	tg := notify.NewTelegram("123:SECRET", []string{"main"}).WithRoutes(map[string][]string{"klien-a": {"chat-a"}, "klien-b": {"chat-b"}})
	tg.BaseURL, tg.Sleep = fake.srv.URL, func(time.Duration) {}
	queue := notify.NewQueueWith(tg, notify.QueueOptions{BatchGap: 50 * time.Millisecond, BatchMax: time.Second, DigestMin: 3})
	defer queue.Close(2 * time.Second)

	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg.SetRecipients([]string{"klien-a", "klien-b"})
	chk := &switchable{down: map[string]bool{}}
	add := func(name string, to ...string) {
		if _, err := reg.Add(model.Site{Name: name, URL: "https://" + strings.ToLower(strings.ReplaceAll(name, " ", "-")) + ".contoh.com", FailThreshold: 1, IntervalSec: 10, NotifyTo: to}); err != nil {
			t.Fatal(err)
		}
	}
	add("Toko A1", "klien-a")
	add("Toko A2", "klien-a")
	add("Toko A3", "klien-a")
	add("Toko B", "klien-b")
	add("Internal")

	mon := monitor.New(monitor.Options{Sites: reg, Checker: chk, Notifier: queue, DataDir: t.TempDir(), StartSpread: -1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { mon.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	for _, n := range []string{"Toko A1", "Toko A2", "Toko A3", "Toko B", "Internal"} {
		chk.set(n, true)
	}
	waitFor(t, "every audience to be told", func() bool {
		return len(fake.to("chat-a")) >= 1 && len(fake.to("chat-b")) >= 1 && len(fake.to("main")) >= 1
	})
	time.Sleep(300 * time.Millisecond)

	a, b, main := fake.to("chat-a"), fake.to("chat-b"), fake.to("main")
	if len(a) != 1 || !strings.Contains(a[0], "3 sites DOWN") || !strings.Contains(a[0], "Toko A1") {
		t.Fatalf("client A's three sites are one digest, in A's chat only: %q", a)
	}
	if len(b) != 1 || !strings.Contains(b[0], "Toko B") || strings.Contains(b[0], "Toko A") {
		t.Fatalf("client B hears only about its own site: %q", b)
	}
	if len(main) != 1 || !strings.Contains(main[0], "Internal") || strings.Contains(main[0], "Toko") {
		t.Fatalf("the main chat hears only about the site without routing: %q", main)
	}
	for _, chat := range []string{"chat-a", "chat-b", "main"} {
		for _, m := range fake.to(chat) {
			for _, other := range map[string][]string{"chat-a": {"Toko B", "Internal"}, "chat-b": {"Toko A", "Internal"}, "main": {"Toko A", "Toko B"}}[chat] {
				if strings.Contains(m, other) {
					t.Errorf("%s was told about %q: %s", chat, other, m)
				}
			}
		}
	}
}
