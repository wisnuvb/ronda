package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestTelegramNotifyTextSendsTheHTMLToEveryChat(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		got[p["chat_id"].(string)] = p["text"].(string) + "|" + p["parse_mode"].(string)
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	fastTelegram(srv.URL, "111", "-100222").NotifyText("<b>halo</b>", "halo")
	if len(got) != 2 || got["111"] != "<b>halo</b>|HTML" || got["-100222"] != "<b>halo</b>|HTML" {
		t.Fatalf("got %v", got)
	}
}

func TestWebhookNotifyTextPostsThePlainReport(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewDecoder(r.Body).Decode(&body) }))
	defer srv.Close()
	NewWebhook(srv.URL).NotifyText("<b>halo</b>", "halo")
	if body["report"] != true || body["text"] != "halo" || body["event"] != nil {
		t.Fatalf("a report is not an event: %v", body)
	}
}

func TestMultiTextSkipsNotifiersThatCannotCarryFreeText(t *testing.T) {
	rec := &recorder{}
	txt := &textRec{}
	Multi{rec, txt}.NotifyText("h", "p")
	if len(rec.got) != 0 {
		t.Fatalf("an events-only notifier must not be handed free text: %v", rec.got)
	}
	if txt.html != "h" || txt.plain != "p" {
		t.Fatalf("%+v", txt)
	}
}

type textRec struct {
	recorder
	html, plain string
}

func (r *textRec) NotifyText(h, p string) { r.html, r.plain = h, p }
