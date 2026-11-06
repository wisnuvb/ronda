package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/model"
)

func routed(srvURL string) *Telegram {
	t := fastTelegram(srvURL, "main1", "main2")
	return t.WithRoutes(map[string][]string{"klien-a": {"a1"}, "klien-b": {"b1", "b2"}, "tim": {"main2", "t1"}})
}

func TestChatsFor(t *testing.T) {
	tg := routed("")
	for _, c := range []struct {
		name string
		to   []string
		want []string
	}{
		{"nobody named: the main chats", nil, []string{"main1", "main2"}},
		{"one recipient only", []string{"klien-a"}, []string{"a1"}},
		{"several recipients", []string{"klien-a", "klien-b"}, []string{"a1", "b1", "b2"}},
		{"'default' adds the main chats", []string{"klien-a", "default"}, []string{"a1", "main1", "main2"}},
		{"a chat reachable twice is told once", []string{"tim", "default"}, []string{"main2", "t1", "main1"}},
		{"a vanished recipient is skipped", []string{"klien-a", "hilang"}, []string{"a1"}},
		{"only vanished recipients: main chats, never nobody", []string{"hilang"}, []string{"main1", "main2"}},
	} {
		if got := tg.ChatsFor(c.to); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// chatServer records which chats got which text.
type chatServer struct {
	mu   sync.Mutex
	sent map[string][]string
	srv  *httptest.Server
}

func newChatServer(t *testing.T) *chatServer {
	c := &chatServer{sent: map[string][]string{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		c.mu.Lock()
		c.sent[p["chat_id"].(string)] = append(c.sent[p["chat_id"].(string)], p["text"].(string))
		c.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *chatServer) chats() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for k := range c.sent {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestAnEventReachesOnlyTheChatsOfItsRecipients(t *testing.T) {
	cs := newChatServer(t)
	tg := routed(cs.srv.URL)
	ev := model.Event{State: "down", SiteName: "Toko Klien A", To: []string{"klien-a"}}
	if err := tg.Send(ev); err != nil {
		t.Fatal(err)
	}
	if got := cs.chats(); !slices.Equal(got, []string{"a1"}) {
		t.Fatalf("a client's site must not be announced in the main chats or another client's: %v", got)
	}
	cs2 := newChatServer(t)
	routed(cs2.srv.URL).Notify(model.Event{State: "down", SiteName: "Internal"})
	if got := cs2.chats(); !slices.Equal(got, []string{"main1", "main2"}) {
		t.Fatalf("no routing: main chats as before: %v", got)
	}
}

// groupRec records every delivery with its audience.
type groupRec struct {
	mu  sync.Mutex
	log []string
}

func (g *groupRec) add(s string) { g.mu.Lock(); g.log = append(g.log, s); g.mu.Unlock() }
func (g *groupRec) Notify(ev model.Event) {
	g.add(fmt.Sprintf("1:%s:%s->%s", ev.State, ev.SiteName, RouteKey(ev.To)))
}
func (g *groupRec) NotifyDigest(state string, evs []model.Event) {
	var names []string
	for _, e := range evs {
		names = append(names, e.SiteName)
	}
	g.add(fmt.Sprintf("N%d:%s:%s->%s", len(evs), state, strings.Join(names, "+"), RouteKey(evs[0].To)))
}
func (g *groupRec) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.log)
}

func TestADigestNeverMixesAudiences(t *testing.T) {
	g := &groupRec{}
	q := NewQueueWith(g, QueueOptions{BatchGap: 80 * time.Millisecond, BatchMax: 2 * time.Second, DigestMin: 3})
	defer q.Close(2 * time.Second)
	send := func(name string, to ...string) { q.Notify(model.Event{State: "down", SiteName: name, To: to}) }
	// Four of client A and three of client B go down together, interleaved, plus two of the main chats.
	send("a1", "klien-a")
	send("b1", "klien-b")
	send("a2", "klien-a")
	send("m1")
	send("b2", "klien-b")
	send("a3", "klien-a")
	send("b3", "klien-b")
	send("a4", "klien-a")
	send("m2")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(g.snapshot()) < 4 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	got := g.snapshot()
	want := []string{"N4:down:a1+a2+a3+a4->klien-a", "N3:down:b1+b2+b3->klien-b", "1:down:m1->", "1:down:m2->"}
	if !slices.Equal(got, want) {
		t.Fatalf("each audience gets its own digest, counted on its own:\n got %v\nwant %v", got, want)
	}
}

func TestTheSameRecipientsInAnyOrderAreOneAudience(t *testing.T) {
	if RouteKey([]string{"b", "a"}) != RouteKey([]string{"a", "b"}) || RouteKey(nil) != RouteKey([]string{}) || RouteKey([]string{"a"}) == RouteKey([]string{"a", "default"}) {
		t.Fatal("RouteKey")
	}
}

func TestNotifyTextToReachesOnlyThatRecipient(t *testing.T) {
	cs := newChatServer(t)
	tg := routed(cs.srv.URL)
	tg.NotifyTextTo("klien-b", "laporan")
	tg.NotifyTextTo("tidak-ada", "x")
	if got := cs.chats(); !slices.Equal(got, []string{"b1", "b2"}) {
		t.Fatalf("%v", got)
	}
	cs2 := newChatServer(t)
	Multi{routed(cs2.srv.URL), &recorder{}}.NotifyTextTo("klien-a", "halo")
	if got := cs2.chats(); !slices.Equal(got, []string{"a1"}) {
		t.Fatalf("Multi passes it only to notifiers that have recipients: %v", got)
	}
}
