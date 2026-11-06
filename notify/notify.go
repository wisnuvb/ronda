// Package notify delivers events to humans: Telegram and an optional JSON webhook.
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
)

type Notifier interface {
	Notify(ev model.Event)
}

// Digester is a Notifier that can say "several sites did the same thing" in one message.
type Digester interface {
	NotifyDigest(state string, evs []model.Event)
}

// TextNotifier delivers a ready-made message that is not one site's event, such as the periodic report.
// html is for Telegram (parse_mode=HTML), plain for everything else.
type TextNotifier interface {
	NotifyText(html, plain string)
}

// RoutedTextNotifier delivers a ready-made message to one named recipient only.
type RoutedTextNotifier interface {
	NotifyTextTo(recipient, html string)
}

// Multi fans one event out to several notifiers.
type Multi []Notifier

// NotifyTextTo hands a message for one recipient to the notifiers that have recipients.
func (m Multi) NotifyTextTo(recipient, html string) {
	for _, n := range m {
		if t, ok := n.(RoutedTextNotifier); ok {
			t.NotifyTextTo(recipient, html)
		}
	}
}

// NotifyText hands a message to the notifiers that can carry free text.
func (m Multi) NotifyText(html, plain string) {
	for _, n := range m {
		if t, ok := n.(TextNotifier); ok {
			t.NotifyText(html, plain)
		}
	}
}

func (m Multi) Notify(ev model.Event) {
	for _, n := range m {
		n.Notify(ev)
	}
}

// NotifyDigest hands a digest to the notifiers that understand it and the plain events to the rest.
func (m Multi) NotifyDigest(state string, evs []model.Event) {
	for _, n := range m {
		if d, ok := n.(Digester); ok {
			d.NotifyDigest(state, evs)
			continue
		}
		for _, ev := range evs {
			n.Notify(ev)
		}
	}
}

// QueueOptions tunes delivery.
type QueueOptions struct {
	// Pace is the pause between two messages, for Telegram's limit of about one per second per chat.
	Pace time.Duration
	// BatchGap holds events back until this long passes with nothing new, so a burst (a server
	// dying takes dozens of sites with it) can be told as one story. 0 turns batching off.
	BatchGap time.Duration
	// BatchMax caps how long a burst can keep extending the wait.
	BatchMax time.Duration
	// DigestMin is how many consecutive events of the same kind become one digest. Below it,
	// events are sent one by one. Values under 2 turn digests off.
	DigestMin int
}

// Queue delivers events in order from a single worker, so "down" always arrives before "up"
// for the same site, paces them, and folds bursts into digests.
type Queue struct {
	ch   chan model.Event
	n    Notifier
	opt  QueueOptions
	done chan struct{}
}

// NewQueue is a queue with no batching: one message per event, Pace apart.
func NewQueue(n Notifier, pace time.Duration) *Queue {
	return NewQueueWith(n, QueueOptions{Pace: pace})
}

func NewQueueWith(n Notifier, opt QueueOptions) *Queue {
	if opt.BatchGap > 0 && opt.BatchMax < opt.BatchGap {
		opt.BatchMax = opt.BatchGap
	}
	q := &Queue{ch: make(chan model.Event, 256), n: n, opt: opt, done: make(chan struct{})}
	go q.run()
	return q
}

func (q *Queue) run() {
	defer close(q.done)
	for ev := range q.ch {
		batch := []model.Event{ev}
		if q.opt.BatchGap > 0 {
			batch = q.gather(batch)
		}
		q.flush(batch)
	}
}

// gather keeps collecting while events keep arriving less than BatchGap apart, up to BatchMax in
// total. A closing queue stops waiting at once: nothing may be lost, and nothing should be delayed.
func (q *Queue) gather(batch []model.Event) []model.Event {
	hard := time.NewTimer(q.opt.BatchMax)
	quiet := time.NewTimer(q.opt.BatchGap)
	defer hard.Stop()
	defer quiet.Stop()
	for {
		select {
		case ev, ok := <-q.ch:
			if !ok {
				return batch
			}
			batch = append(batch, ev)
			quiet.Reset(q.opt.BatchGap)
		case <-quiet.C:
			return batch
		case <-hard.C:
			return batch
		}
	}
}

// flush sends a batch in order. Only a run of consecutive "down" (or "up", or "reminder") events becomes a digest,
// so a site's own down and up can never swap places.
func (q *Queue) flush(batch []model.Event) {
	first := true
	gate := func() { // pause between messages, never before the first
		if !first && q.opt.Pace > 0 {
			time.Sleep(q.opt.Pace)
		}
		first = false
	}
	for i := 0; i < len(batch); {
		j := i
		for j < len(batch) && batch[j].State == batch[i].State {
			j++
		}
		run, state := batch[i:j], batch[i].State
		i = j
		// A digest is one message to one audience, so a run is first split by who is to hear it: three sites of
		// three different clients must never become one message that tells each client about the others.
		for _, group := range byRoute(run) {
			if d, ok := q.n.(Digester); ok && (state == "down" || state == "up" || state == "reminder") && q.opt.DigestMin >= 2 && len(group) >= q.opt.DigestMin {
				gate()
				d.NotifyDigest(state, group)
				continue
			}
			for _, ev := range group {
				gate()
				q.n.Notify(ev)
			}
		}
	}
}

// RouteKey identifies the audience of an event: the same recipients in any order are the same audience.
func RouteKey(to []string) string {
	s := slices.Clone(to)
	slices.Sort(s)
	return strings.Join(s, ",")
}

// byRoute splits events into groups with the same audience, in order of first appearance and keeping the
// order inside each group.
func byRoute(evs []model.Event) [][]model.Event {
	idx := map[string]int{}
	var out [][]model.Event
	for _, ev := range evs {
		k := RouteKey(ev.To)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], ev)
	}
	return out
}

// Notify never blocks; when the queue is full the event is dropped and logged.
// It must not be called after Close.
func (q *Queue) Notify(ev model.Event) {
	select {
	case q.ch <- ev:
	default:
		log.Printf("notify: queue full, dropping notification %s %s", ev.State, ev.SiteName)
	}
}

// Close stops accepting events and waits up to timeout for pending ones to be delivered.
func (q *Queue) Close(timeout time.Duration) {
	close(q.ch)
	select {
	case <-q.done:
	case <-time.After(timeout):
		log.Printf("notify: some notifications were still undelivered at shutdown")
	}
}

const digestLines = 15

// digestList shortens a long list to the first few and a count of the rest.
func digestList(evs []model.Event, line func(model.Event) string) []string {
	out := make([]string, 0, digestLines+1)
	for i, ev := range evs {
		if i == digestLines {
			out = append(out, i18n.T("… and %d more (see the dashboard)", len(evs)-digestLines))
			break
		}
		out = append(out, "• "+line(ev))
	}
	return out
}

func earliest(evs []model.Event) int64 {
	t := evs[0].Since
	for _, ev := range evs {
		if ev.Since != 0 && (t == 0 || ev.Since < t) {
			t = ev.Since
		}
	}
	return t
}

// FormatDigestHTML renders several same-kind events as one Telegram message.
func FormatDigestHTML(state string, evs []model.Event) string {
	esc := html.EscapeString
	var b strings.Builder
	if state == "down" {
		b.WriteString(i18n.T("🔴 <b>%d sites DOWN at about the same time</b>", len(evs)) + "\n")
		for _, l := range digestList(evs, func(ev model.Event) string {
			return fmt.Sprintf("%s — %s", esc(ev.SiteName), esc(model.KindLabel(ev.Kind)))
		}) {
			b.WriteString(l + "\n")
		}
		b.WriteString(i18n.T("Many sites failed at once: the likely cause is shared (server, network, DNS, or CDN), not each site.\nSince %s", clock(earliest(evs))))
		return b.String()
	}
	if state == "reminder" {
		b.WriteString(i18n.T("⏰ <b>%d sites STILL DOWN</b>", len(evs)) + "\n")
		for _, l := range digestList(evs, func(ev model.Event) string {
			return i18n.T("%s — %s, down for %s", esc(ev.SiteName), esc(model.KindLabel(ev.Kind)), Duration(ev.DowntimeSec))
		}) {
			b.WriteString(l + "\n")
		}
		b.WriteString(i18n.T("None have recovered since %s.", since(earliest(evs))))
		return b.String()
	}
	b.WriteString(i18n.T("🟢 <b>%d sites RECOVERED</b>", len(evs)) + "\n")
	for _, l := range digestList(evs, func(ev model.Event) string {
		return i18n.T("%s — was down for %s", esc(ev.SiteName), Duration(ev.DowntimeSec))
	}) {
		b.WriteString(l + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatDigestText is the plain-text form, for webhooks.
func FormatDigestText(state string, evs []model.Event) string {
	var b strings.Builder
	if state == "down" {
		b.WriteString(i18n.T("[DOWN] %d sites down at about the same time", len(evs)) + "\n")
		for _, l := range digestList(evs, func(ev model.Event) string { return ev.SiteName + " — " + model.KindLabel(ev.Kind) }) {
			b.WriteString(l + "\n")
		}
		b.WriteString(i18n.T("Likely a shared cause (server, network, DNS, or CDN). Since %s", clock(earliest(evs))))
		return b.String()
	}
	if state == "reminder" {
		b.WriteString(i18n.T("[STILL DOWN] %d sites have not recovered", len(evs)) + "\n")
		for _, l := range digestList(evs, func(ev model.Event) string {
			return i18n.T("%s — %s, down for %s", ev.SiteName, model.KindLabel(ev.Kind), Duration(ev.DowntimeSec))
		}) {
			b.WriteString(l + "\n")
		}
		b.WriteString(i18n.T("Since %s", since(earliest(evs))))
		return b.String()
	}
	b.WriteString(i18n.T("[RECOVERED] %d sites recovered", len(evs)) + "\n")
	for _, l := range digestList(evs, func(ev model.Event) string {
		return i18n.T("%s — was down for %s", ev.SiteName, Duration(ev.DowntimeSec))
	}) {
		b.WriteString(l + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// addressLine is the site's address on its own line, or nothing for a push site, whose "address" is
// only an internal label (push://<id>) that tells a reader nothing.
func addressLine(url string) string {
	if url == "" || strings.HasPrefix(url, "push://") {
		return ""
	}
	return html.EscapeString(url) + "\n"
}

// FormatHTML renders an event for Telegram (parse_mode=HTML).
func FormatHTML(ev model.Event) string {
	esc := html.EscapeString
	var b strings.Builder
	switch ev.State {
	case "down":
		b.WriteString(i18n.T("🔴 <b>DOWN</b> — %s\n%s", esc(ev.SiteName), addressLine(ev.URL)))
		if ev.Hint != "" {
			b.WriteString(i18n.T("<b>Cause:</b> %s", esc(ev.Hint)) + "\n")
		} else if ev.Error != "" {
			b.WriteString(i18n.T("<b>Cause:</b> %s", esc(ev.Error)) + "\n")
		}
		b.WriteString(i18n.T("Failed %d times in a row · since %s", ev.Fails, clock(ev.Since)))
	case "reminder":
		b.WriteString(i18n.T("⏰ <b>STILL DOWN</b> — %s\n%s", esc(ev.SiteName), addressLine(ev.URL)))
		if ev.Hint != "" {
			b.WriteString(i18n.T("<b>Cause now:</b> %s", esc(ev.Hint)) + "\n")
		} else if ev.Error != "" {
			b.WriteString(i18n.T("<b>Cause now:</b> %s", esc(ev.Error)) + "\n")
		}
		b.WriteString(i18n.T("Down for <b>%s</b> · since %s", Duration(ev.DowntimeSec), since(ev.Since)))
	case "up":
		b.WriteString(i18n.T("🟢 <b>RECOVERED</b> — %s\n%s", esc(ev.SiteName), addressLine(ev.URL)))
		b.WriteString(i18n.T("Was down for %s · response %d ms", Duration(ev.DowntimeSec), ev.LatencyMs))
	case "cert":
		b.WriteString(i18n.T("🟠 <b>CERTIFICATE</b> — %s\n%s", esc(ev.SiteName), esc(ev.URL)) + "\n")
		b.WriteString(i18n.T("The TLS certificate expires in <b>%s</b> (%s).\n%s", daysLeft(ev.DaysLeft), date(ev.CertNotAfter), esc(ev.Hint)))
	default:
		b.WriteString(i18n.T("✅ <b>%s</b>: test notification delivered · %s", esc(ev.SiteName), clock(ev.Time)))
	}
	return b.String()
}

// FormatText is the plain-text form, used for webhooks.
func FormatText(ev model.Event) string {
	switch ev.State {
	case "down":
		why := ev.Hint
		if why == "" {
			why = ev.Error
		}
		return i18n.T("[DOWN] %s — %s\nCause: %s\nFailed %d times in a row · since %s", ev.SiteName, ev.URL, why, ev.Fails, clock(ev.Since))
	case "reminder":
		why := ev.Hint
		if why == "" {
			why = ev.Error
		}
		return i18n.T("[STILL DOWN] %s — %s\nCause now: %s\nDown for %s · since %s", ev.SiteName, ev.URL, why, Duration(ev.DowntimeSec), since(ev.Since))
	case "up":
		return i18n.T("[RECOVERED] %s — %s\nWas down for %s · response %d ms", ev.SiteName, ev.URL, Duration(ev.DowntimeSec), ev.LatencyMs)
	case "cert":
		return i18n.T("[CERTIFICATE] %s — %s\nExpires in %s (%s). %s", ev.SiteName, ev.URL, daysLeft(ev.DaysLeft), date(ev.CertNotAfter), ev.Hint)
	}
	return i18n.T("[TEST] %s · %s", ev.SiteName, clock(ev.Time))
}

func daysLeft(d int) string {
	switch {
	case d < 1:
		return i18n.T("less than 1 day")
	case d == 1:
		return i18n.T("1 day")
	}
	return i18n.T("%d days", d)
}

func date(ms int64) string { return model.Stamp(ms, "2006-01-02") }

// since is date and time, because an outage that reminders speak of often began on an earlier day.
func since(ms int64) string { return model.Stamp(ms, "2006-01-02 15:04:05") }

func clock(ms int64) string { return model.Stamp(ms, "15:04:05") }

// Duration formats seconds as "4 min 12 s" ("4 mnt 12 dtk" in Indonesian).
func Duration(sec int64) string {
	switch {
	case sec < 60:
		return i18n.T("%d s", max(sec, 0))
	case sec < 3600:
		return i18n.T("%d min %d s", sec/60, sec%60)
	case sec < 86400:
		return i18n.T("%d h %d min", sec/3600, sec%3600/60)
	}
	return i18n.T("%d d %d h", sec/86400, sec%86400/3600)
}

// Telegram sends through the Bot API.
type Telegram struct {
	Token   string
	ChatIDs []string            // the main chats: where everything goes unless a site names other recipients
	Routes  map[string][]string // recipient name -> chats
	BaseURL string              // default https://api.telegram.org
	Client  *http.Client
	Sleep   func(time.Duration) // replaceable in tests
}

func NewTelegram(token string, chatIDs []string) *Telegram {
	return &Telegram{
		Token: token, ChatIDs: chatIDs, BaseURL: "https://api.telegram.org",
		Client: &http.Client{Timeout: 15 * time.Second}, Sleep: time.Sleep,
	}
}

// WithRoutes sets the named recipients a site can send to.
func (t *Telegram) WithRoutes(routes map[string][]string) *Telegram {
	t.Routes = routes
	return t
}

// ChatsFor resolves a site's recipients to chats. No recipients means the main chats. "default" adds the main
// chats to the named ones. A name that no longer exists is skipped, and if that leaves nobody the main chats
// hear it: a notification must never be lost because the routing went stale.
func (t *Telegram) ChatsFor(to []string) []string {
	if len(to) == 0 {
		return t.ChatIDs
	}
	var out []string
	add := func(ids []string) {
		for _, id := range ids {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	for _, name := range to {
		if name == model.DefaultRecipient {
			add(t.ChatIDs)
		} else if ids, ok := t.Routes[name]; ok {
			add(ids)
		} else {
			log.Printf("notify telegram: recipient %q is not in the config; skipped", name)
		}
	}
	if len(out) == 0 {
		return t.ChatIDs
	}
	return out
}

// NotifyTextTo sends one message to the chats of a single named recipient. An unknown name sends nothing.
func (t *Telegram) NotifyTextTo(recipient, html string) {
	for _, chat := range t.Routes[recipient] {
		if err := t.sendOne(chat, html); err != nil {
			log.Printf("notify telegram: chat %s: %v", chat, err)
		}
	}
}

func (t *Telegram) Notify(ev model.Event) {
	if err := t.Send(ev); err != nil {
		log.Printf("notify telegram: %v", err)
	}
}

// Send delivers ev to every chat and reports failures. Errors never contain the bot token.
func (t *Telegram) Send(ev model.Event) error {
	text := FormatHTML(ev)
	var errs []error
	for _, chat := range t.ChatsFor(ev.To) {
		if err := t.sendOne(chat, text); err != nil {
			errs = append(errs, fmt.Errorf("chat %s: %w", chat, err))
		}
	}
	return errors.Join(errs...)
}

// NotifyDigest sends one message for several events of the same kind.
func (t *Telegram) NotifyDigest(state string, evs []model.Event) {
	text := FormatDigestHTML(state, evs)
	for _, chat := range t.ChatsFor(evs[0].To) {
		if err := t.sendOne(chat, text); err != nil {
			log.Printf("notify telegram: chat %s: %v", chat, err)
		}
	}
}

// NotifyText sends one message to every chat.
func (t *Telegram) NotifyText(html, _ string) {
	for _, chat := range t.ChatIDs {
		if err := t.sendOne(chat, html); err != nil {
			log.Printf("notify telegram: chat %s: %v", chat, err)
		}
	}
}

// SendText sends one already-formatted HTML message to a single chat (used for bot replies).
func (t *Telegram) SendText(chat, html string) error { return t.sendOne(chat, html) }

// Redact removes the bot token from s, for logging errors that may quote the request URL.
func (t *Telegram) Redact(s string) string { return t.redact(s) }

func (t *Telegram) sendOne(chat, text string) error {
	body, _ := json.Marshal(map[string]any{
		"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
	})
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/bot" + t.Token + "/sendMessage"

	var last error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return errors.New(t.redact(err.Error()))
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := t.Client.Do(req)
		if err != nil {
			last = errors.New(t.redact(err.Error()))
			t.Sleep(backoff(attempt))
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()

		var tr struct {
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(raw, &tr)

		switch {
		case resp.StatusCode == http.StatusOK:
			return nil
		case resp.StatusCode == http.StatusTooManyRequests:
			wait := time.Duration(min(max(tr.Parameters.RetryAfter, 1), 60)) * time.Second
			last = fmt.Errorf("rate limited by Telegram (429), retrying after %s", wait)
			t.Sleep(wait)
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			// Wrong token, unknown chat, bot blocked, bad markup: retrying cannot help.
			return fmt.Errorf("telegram rejected the message (%d): %s", resp.StatusCode, tr.Description)
		default:
			last = fmt.Errorf("telegram error %d", resp.StatusCode)
			t.Sleep(backoff(attempt))
		}
	}
	return fmt.Errorf("failed after several attempts: %w", last)
}

func backoff(attempt int) time.Duration { return time.Duration(2<<attempt) * time.Second }

func (t *Telegram) redact(s string) string {
	if t.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, t.Token, "***")
}

// Webhook posts the event as JSON.
type Webhook struct {
	URL    string
	Client *http.Client
}

func NewWebhook(url string) *Webhook {
	return &Webhook{URL: url, Client: &http.Client{Timeout: 10 * time.Second}}
}

// NotifyDigest posts several same-kind events as one JSON document.
func (w *Webhook) NotifyDigest(state string, evs []model.Event) {
	body, _ := json.Marshal(map[string]any{"state": state, "events": evs, "text": FormatDigestText(state, evs)})
	w.post(body)
}

// NotifyText posts a report as {"report": true, "text": ...}.
func (w *Webhook) NotifyText(_, plain string) {
	body, _ := json.Marshal(map[string]any{"report": true, "text": plain})
	w.post(body)
}

func (w *Webhook) Notify(ev model.Event) {
	body, _ := json.Marshal(map[string]any{"event": ev, "text": FormatText(ev)})
	w.post(body)
}

func (w *Webhook) post(body []byte) {
	resp, err := w.Client.Post(w.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("notify webhook: delivery failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("notify webhook: status %s", resp.Status)
	}
}
