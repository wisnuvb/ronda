package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
	"github.com/wisnuvb/ronda/internal/monitor"
	"github.com/wisnuvb/ronda/internal/notify"
	"github.com/wisnuvb/ronda/internal/registry"
)

// fakeTelegram serves getUpdates like the real one (offset, offset=-1, long poll) and records replies.
type fakeTelegram struct {
	mu      sync.Mutex
	all     []map[string]any
	nextID  int64
	sent    []sentMsg
	failGet []int // status codes to answer the next getUpdates calls with
	polls   int   // regular (non-startup) getUpdates calls seen
	srv     *httptest.Server
}

type sentMsg struct{ Chat, Text string }

func newFake(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{nextID: 1}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// push sends text from a private chat, where the chat id is also the user id.
func (f *fakeTelegram) push(chat int64, text string) { f.pushFrom(chat, chat, "private", text) }

// pushFrom sends text as user `from` in a chat of the given type ("private", "group", "supergroup").
func (f *fakeTelegram) pushFrom(chat, from int64, kind, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.all = append(f.all, map[string]any{
		"update_id": f.nextID,
		"message": map[string]any{"text": text, "from": map[string]any{"id": from},
			"chat": map[string]any{"id": chat, "type": kind}},
	})
	f.nextID++
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case strings.HasSuffix(r.URL.Path, "/getUpdates"):
		f.mu.Lock()
		if len(f.failGet) > 0 {
			code := f.failGet[0]
			f.failGet = f.failGet[1:]
			f.mu.Unlock()
			w.WriteHeader(code)
			io.WriteString(w, `{"ok":false,"description":"boom"}`)
			return
		}
		offset := int64(body["offset"].(float64))
		if offset != -1 {
			f.polls++
		}
		var out []map[string]any
		if offset == -1 {
			if len(f.all) > 0 {
				out = f.all[len(f.all)-1:]
			}
		} else {
			for _, u := range f.all {
				if int64(u["update_id"].(int64)) >= offset {
					out = append(out, u)
				}
			}
		}
		f.mu.Unlock()
		if len(out) == 0 {
			time.Sleep(20 * time.Millisecond) // a short "long poll"
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": out})
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		f.mu.Lock()
		f.sent = append(f.sent, sentMsg{Chat: body["chat_id"].(string), Text: body["text"].(string)})
		f.mu.Unlock()
		io.WriteString(w, `{"ok":true}`)
	default:
		http.NotFound(w, r)
	}
}

// waitPolling returns once the bot has finished its startup (backlog skip) and is polling normally.
// A message pushed earlier would be mistaken for backlog and skipped, by design.
func (f *fakeTelegram) waitPolling(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := f.polls
		f.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the bot never started polling")
}

func (f *fakeTelegram) replies() []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMsg(nil), f.sent...)
}

func (f *fakeTelegram) waitReplies(t *testing.T, n int) []sentMsg {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.replies(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited for %d replies, got %v", n, f.replies())
	return nil
}

type okChecker struct{}

func (okChecker) Check(_ context.Context, s model.Site) model.Result {
	if strings.Contains(s.URL, "rusak") {
		return model.Result{Time: time.Now().UnixMilli(), Kind: model.KindRefused, Hint: "server mati <b>"}
	}
	return model.Result{Time: time.Now().UnixMilli(), OK: true, Status: 200, LatencyMs: 42, Kind: model.KindOK}
}

type env struct {
	f   *fakeTelegram
	reg *registry.Registry
	mon *monitor.Monitor
}

// start runs a bot for chat "100" and waits for it to stop at cleanup.
func start(t *testing.T, preload func(f *fakeTelegram)) *env { return startAdmins(t, preload, nil) }

func startAdmins(t *testing.T, preload func(f *fakeTelegram), admins []int64) *env {
	t.Helper()
	f := newFake(t)
	if preload != nil {
		preload(f)
	}
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mon := monitor.New(monitor.Options{Registry: reg, Checker: okChecker{}})
	tg := notify.NewTelegram("123:SECRET", []string{"100"})
	tg.BaseURL = f.srv.URL
	tg.Sleep = func(time.Duration) {}
	b := New(tg, reg, mon).WithAdmins(admins)
	b.pause = func(ctx context.Context, d time.Duration) { time.Sleep(5 * time.Millisecond) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("bot did not stop")
		}
	})
	f.waitPolling(t)
	return &env{f, reg, mon}
}

// sayIn sends text as `from` in the allowed chat 100 typed as `kind`, and returns the reply.
func (e *env) sayIn(t *testing.T, from int64, kind, text string) string {
	t.Helper()
	before := len(e.f.replies())
	e.f.pushFrom(100, from, kind, text)
	return e.f.waitReplies(t, before+1)[before].Text
}

func TestGroupMembersCannotChangeThingsUnlessAdmin(t *testing.T) {
	const admin, member = 7, 8
	e := startAdmins(t, nil, []int64{admin})
	if _, err := e.reg.Add(model.Site{Name: "Toko", URL: "https://toko.contoh.com"}); err != nil {
		t.Fatal(err)
	}
	count := func() int { return len(e.reg.List()) }

	// A plain member of the group may read...
	if r := e.sayIn(t, member, "supergroup", "/status"); strings.Contains(r, "Ronda admins") {
		t.Fatalf("read-only commands must work for every member: %q", r)
	}
	if r := e.sayIn(t, member, "group", "/list"); strings.Contains(r, "Ronda admins") {
		t.Fatalf("/list must work for every member: %q", r)
	}
	// ...but every command that changes something is refused, and nothing changes.
	for _, cmd := range []string{
		"/add https://baru.contoh.com", "/remove Toko yes", "/pause Toko", "/resume Toko", "/maintenance Toko 1h", "/endmaint Toko",
		// the Indonesian spellings are the same commands and must be held to the same rule
		"/tambah https://baru.contoh.com", "/hapus Toko ya", "/jeda Toko", "/lanjut Toko", "/pemeliharaan Toko 1j", "/akhiri Toko",
		"/delete Toko yes", "/endmaintenance Toko",
	} {
		if r := e.sayIn(t, member, "supergroup", cmd); !strings.Contains(r, "Ronda admins") {
			t.Fatalf("%s from a member must be refused, got %q", cmd, r)
		}
	}
	if count() != 1 {
		t.Fatalf("a refused command changed the site list (%d sites)", count())
	}
	if s, _ := e.reg.Get(e.reg.List()[0].ID); s.Paused || s.MaintenanceUntil != 0 {
		t.Fatalf("a refused command changed the site: %+v", s)
	}
	// Positive control: the same command from the admin in the same group works.
	if r := e.sayIn(t, admin, "supergroup", "/add https://baru.contoh.com Baru"); strings.Contains(r, "Ronda admins") || count() != 2 {
		t.Fatalf("the admin must be able to add: %q (%d sites)", r, count())
	}
	// A private chat needs no admin entry.
	if r := e.sayIn(t, member, "private", "/remove Baru yes"); strings.Contains(r, "Ronda admins") || count() != 1 {
		t.Fatalf("a private chat must be able to delete: %q (%d sites)", r, count())
	}
}

func TestGroupWithoutAnyAdminIsReadOnlyAndUnknownChatTypeFailsClosed(t *testing.T) {
	e := startAdmins(t, nil, nil)
	if r := e.sayIn(t, 7, "group", "/add https://x.contoh.com"); !strings.Contains(r, "Ronda admins") {
		t.Fatalf("with no admins configured a group must be read-only, got %q", r)
	}
	if r := e.sayIn(t, 7, "", "/tambah https://x.contoh.com"); !strings.Contains(r, "Ronda admins") {
		t.Fatalf("a missing chat type must not be trusted as private, got %q", r)
	}
	if n := len(e.reg.List()); n != 0 {
		t.Fatalf("nothing may have been added, got %d sites", n)
	}
}

func (e *env) say(t *testing.T, text string) string {
	t.Helper()
	before := len(e.f.replies())
	e.f.push(100, text)
	return e.f.waitReplies(t, before+1)[before].Text
}

func TestUnauthorisedChatsGetNoAnswerAndAreLogged(t *testing.T) {
	var buf lockedBuf
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	e := start(t, nil)
	e.f.push(999, "/status")
	e.f.push(999, "/remove anything yes")
	reply := e.say(t, "/status")
	if got := e.f.replies(); len(got) != 1 || got[0].Chat != "100" {
		t.Fatalf("strangers must get no reply, got %v", got)
	}
	if !strings.Contains(reply, "No sites yet") {
		t.Fatalf("authorised chat: %q", reply)
	}
	if !strings.Contains(buf.String(), "chat 999") {
		t.Fatalf("the ignored chat id must be logged so it can be configured: %q", buf.String())
	}
}

func TestBacklogFromWhileWeWereDownIsNotExecuted(t *testing.T) {
	e0 := start(t, nil)
	site, _ := e0.reg.Add(model.Site{URL: "https://a.example", Name: "Toko"})
	_ = site

	// A fresh bot, with a stale destructive command already waiting at Telegram.
	f := newFake(t)
	f.push(100, "/remove toko yes")
	f.push(100, "/pause toko")
	tg := notify.NewTelegram("123:SECRET", []string{"100"})
	tg.BaseURL = f.srv.URL
	b := New(tg, e0.reg, e0.mon)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	f.waitPolling(t)
	f.push(100, "/status") // a live command after startup is served
	got := f.waitReplies(t, 1)
	if len(got) != 1 || !strings.Contains(got[0].Text, "1 site") {
		t.Fatalf("only the live command may be answered: %v", got)
	}
	if s, err := e0.reg.Get(site.ID); err != nil || s.Paused {
		t.Fatalf("a stale command was executed: %+v %v", s, err)
	}
}

func TestStatusAndListEscapeHTML(t *testing.T) {
	e := start(t, nil)
	e.reg.Add(model.Site{URL: "https://a.example", Name: "Toko <b>A</b>"})
	e.reg.Add(model.Site{URL: "https://b.example", Name: "Blog"})
	e.reg.SetPaused(mustID(t, e, "Blog"), true)

	list := e.say(t, "/list")
	if strings.Contains(list, "<b>A</b>") || !strings.Contains(list, "Toko &lt;b&gt;A&lt;/b&gt;") {
		t.Fatalf("site names must be escaped, or Telegram renders markup (or rejects the message): %q", list)
	}
	if !strings.Contains(list, "Blog") {
		t.Fatalf("list: %q", list)
	}
	if st := e.say(t, "/status"); !strings.Contains(st, "2 sites") {
		t.Fatalf("status: %q", st)
	}
}

func mustID(t *testing.T, e *env, name string) string {
	t.Helper()
	for _, s := range e.reg.List() {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("no site %q", name)
	return ""
}

func TestAddAcceptsBareHostAndReportsProblems(t *testing.T) {
	e := start(t, nil)
	reply := e.say(t, "/add toko.contoh.com Toko Online")
	if !strings.Contains(reply, "added") {
		t.Fatalf("%q", reply)
	}
	sites := e.reg.List()
	if len(sites) != 1 || sites[0].URL != "https://toko.contoh.com" || sites[0].Name != "Toko Online" {
		t.Fatalf("sites: %+v", sites)
	}
	if sites[0].AllowPrivate {
		t.Fatal("the bot must never be able to enable allow_private")
	}
	if r := e.say(t, "/add ftp://x.example"); !strings.Contains(r, "http") || len(e.reg.List()) != 1 {
		t.Fatalf("a bad URL must be refused with a reason: %q", r)
	}
	if r := e.say(t, "/add"); !strings.Contains(r, "Usage") {
		t.Fatalf("%q", r)
	}
}

func TestPauseResumeResolveNames(t *testing.T) {
	e := start(t, nil)
	a, _ := e.reg.Add(model.Site{URL: "https://toko.example", Name: "Toko Online"})
	e.reg.Add(model.Site{URL: "https://tokoh.example", Name: "Tokoh Sejarah"})
	e.reg.Add(model.Site{URL: "https://blog.example", Name: "Blog Kantor"})

	if r := e.say(t, "/pause toko"); !strings.Contains(r, "2 sites match") || !strings.Contains(r, a.ID) {
		t.Fatalf("an ambiguous name must list candidates and change nothing: %q", r)
	}
	for _, s := range e.reg.List() {
		if s.Paused {
			t.Fatalf("nothing may be paused after an ambiguous request: %+v", s)
		}
	}
	if r := e.say(t, "/pause toko online"); !strings.Contains(r, "paused") { // exact (case-insensitive) name beats substring
		t.Fatalf("%q", r)
	}
	if s, _ := e.reg.Get(a.ID); !s.Paused {
		t.Fatal("not paused")
	}
	if r := e.say(t, "/resume "+a.ID); !strings.Contains(r, "resumed") {
		t.Fatalf("%q", r)
	}
	if s, _ := e.reg.Get(a.ID); s.Paused {
		t.Fatal("not resumed")
	}
	if r := e.say(t, "/pause tidak-ada"); !strings.Contains(r, "No site matches") {
		t.Fatalf("%q", r)
	}
	if r := e.say(t, "/pause"); !strings.Contains(r, "Name the site") {
		t.Fatalf("%q", r)
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	e := start(t, nil)
	s, _ := e.reg.Add(model.Site{URL: "https://a.example", Name: "Toko"})

	r := e.say(t, "/remove toko")
	if !strings.Contains(r, "/remove "+s.ID+" yes") || len(e.reg.List()) != 1 {
		t.Fatalf("without 'yes' nothing may be deleted: %q", r)
	}
	if r := e.say(t, "/remove toko yes"); !strings.Contains(r, "removed") || len(e.reg.List()) != 0 {
		t.Fatalf("%q", r)
	}
}

func TestMaintenanceCommand(t *testing.T) {
	e := start(t, nil)
	s, _ := e.reg.Add(model.Site{URL: "https://a.example", Name: "Toko Online"})

	r := e.say(t, "/maintenance toko online 2h")
	got, _ := e.reg.Get(s.ID)
	want := time.Now().Add(2 * time.Hour).UnixMilli()
	if !strings.Contains(r, "under maintenance") || got.MaintenanceUntil < want-5000 || got.MaintenanceUntil > want+5000 {
		t.Fatalf("until=%d want about %d: %q", got.MaintenanceUntil, want, r)
	}
	if r := e.say(t, "/endmaint toko"); !strings.Contains(r, "ended") {
		t.Fatalf("%q", r)
	}
	if got, _ := e.reg.Get(s.ID); got.MaintenanceUntil != 0 {
		t.Fatal("maintenance not ended")
	}
	for _, bad := range []string{"/maintenance toko", "/maintenance toko lama", "/maintenance toko 0", "/maintenance toko 90d"} {
		if r := e.say(t, bad); !strings.Contains(r, "Usage") && !strings.Contains(r, "at most") {
			t.Errorf("%s: %q", bad, r)
		}
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]int{"30": 30, "30m": 30, "2j": 120, "2h": 120, "1d": 1440, "1hari": 1440, "45menit": 45, "2JAM": 120,
		"15min": 15, "3hours": 180, "2hr": 120, "1day": 1440, "3days": 4320} {
		if got, ok := parseDuration(in); !ok || got != want {
			t.Errorf("parseDuration(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0", "-5", "m", "abc", "5x", "1.5j"} {
		if _, ok := parseDuration(in); ok {
			t.Errorf("parseDuration(%q) must fail", in)
		}
	}
}

func TestCheckCommandDoesNotChangeState(t *testing.T) {
	e := start(t, nil)
	e.reg.Add(model.Site{URL: "https://sehat.example", Name: "Sehat"})
	e.reg.Add(model.Site{URL: "https://rusak.example", Name: "Rusak"})

	if r := e.say(t, "/check sehat"); !strings.Contains(r, "HTTP 200") || !strings.Contains(r, "42 ms") {
		t.Fatalf("%q", r)
	}
	r := e.say(t, "/check rusak")
	if !strings.Contains(r, html.EscapeString(model.KindLabel(model.KindRefused))) || !strings.Contains(r, "server mati &lt;b&gt;") {
		t.Fatalf("the cause must be shown, escaped: %q", r)
	}
	for _, v := range e.mon.Views() {
		if v.State != monitor.StUnknown {
			t.Fatalf("a manual check must not set a verdict: %+v", v)
		}
	}
}

func TestCommandForms(t *testing.T) {
	e := start(t, nil)
	if r := e.say(t, "/STATUS@ronda_bot"); !strings.Contains(r, "No sites yet") {
		t.Fatalf("case and @botname must be tolerated: %q", r)
	}
	if r := e.say(t, "/help"); !strings.Contains(r, "/add") {
		t.Fatalf("%q", r)
	}
	if r := e.say(t, "/entah"); !strings.Contains(r, "Unknown command") {
		t.Fatalf("%q", r)
	}
	e.f.push(100, "halo, apa kabar") // plain chat is not a command
	e.f.push(100, "/status")
	if got := e.f.waitReplies(t, 4); len(got) != 4 {
		t.Fatalf("plain text must not be answered: %v", got)
	}
}

func TestSurvivesTelegramErrors(t *testing.T) {
	var buf lockedBuf
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	e := start(t, func(f *fakeTelegram) { f.failGet = []int{409, 409, 500, 401} })
	if r := e.say(t, "/status"); !strings.Contains(r, "No sites yet") {
		t.Fatalf("the bot must recover after errors: %q", r)
	}
	out := buf.String()
	if !strings.Contains(out, "409") && !strings.Contains(out, "another bot") {
		t.Fatalf("a polling conflict must be explained: %q", out)
	}
	if strings.Count(out, "another bot is polling") != 1 {
		t.Fatalf("a repeated error must be logged once, not on every retry: %q", out)
	}
	if strings.Contains(out, "SECRET") {
		t.Fatalf("token leaked: %q", out)
	}
}

type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuf) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

func TestAddTCPAndDNSByScheme(t *testing.T) {
	e := start(t, nil)
	if r := e.say(t, "/add tcp://db.example:5432 Database Utama"); !strings.Contains(r, "added") {
		t.Fatalf("%q", r)
	}
	if r := e.say(t, "/add dns://contoh.example"); !strings.Contains(r, "added") {
		t.Fatalf("%q", r)
	}
	got := map[string]model.Site{}
	for _, s := range e.reg.List() {
		got[s.Type] = s
	}
	if s := got["tcp"]; s.URL != "tcp://db.example:5432" || s.Name != "Database Utama" {
		t.Fatalf("tcp site: %+v", s)
	}
	if s := got["dns"]; s.URL != "dns://contoh.example" || s.DNSType != "A" {
		t.Fatalf("dns site: %+v", s)
	}
	if r := e.say(t, "/add tcp://db.example"); !strings.Contains(r, "host:port") {
		t.Fatalf("a tcp address without a port must say what is wrong: %q", r)
	}
	if r := e.say(t, "/add tcp://127.0.0.1:22"); !strings.Contains(r, "added") || func() bool {
		for _, s := range e.reg.List() {
			if s.URL == "tcp://127.0.0.1:22" {
				return s.AllowPrivate
			}
		}
		return true
	}() {
		t.Fatalf("a loopback target may be added but must stay blocked (allow_private off): %q", r)
	}
}

func TestBotRefusesToCreateOrRevealPushSites(t *testing.T) {
	e := start(t, nil)
	r := e.say(t, "/add push://backup Backup Malam")
	if !strings.Contains(r, "dashboard") || len(e.reg.List()) != 0 {
		t.Fatalf("a ping address holds a secret and must not be minted into a chat: %q", r)
	}
	s, _ := e.reg.Add(model.Site{Type: "push", Name: "Backup"})
	for _, cmd := range []string{"/list", "/status", "/check backup", "/pause backup", "/resume backup"} {
		if out := e.say(t, cmd); strings.Contains(out, s.PushToken) {
			t.Fatalf("%s leaked the token: %s", cmd, out)
		}
	}
	if out := e.say(t, "/check backup"); !strings.Contains(strings.ToLower(out), "push") {
		t.Fatalf("a push site is not checked by hand, and should say so: %q", out)
	}
}

// The bot started in Indonesian. Every Indonesian command, and the Indonesian words for its arguments,
// must keep working next to the English ones.
func TestIndonesianCommandAliasesStillWork(t *testing.T) {
	e := startAdmins(t, nil, nil)
	if r := e.say(t, "/tambah toko.contoh.com Toko Online"); !strings.Contains(r, "added") || len(e.reg.List()) != 1 {
		t.Fatalf("/tambah: %q", r)
	}
	id := e.reg.List()[0].ID
	state := func() model.Site { s, _ := e.reg.Get(id); return s }

	if r := e.say(t, "/daftar"); !strings.Contains(r, "Toko Online") {
		t.Fatalf("/daftar: %q", r)
	}
	if r := e.say(t, "/cek toko"); !strings.Contains(r, "HTTP 200") {
		t.Fatalf("/cek: %q", r)
	}
	e.say(t, "/jeda toko")
	if !state().Paused {
		t.Fatal("/jeda did not pause")
	}
	e.say(t, "/lanjut toko")
	if state().Paused {
		t.Fatal("/lanjut did not resume")
	}
	e.say(t, "/pemeliharaan toko 2j")
	if d := time.UnixMilli(state().MaintenanceUntil).Sub(time.Now()); d < 119*time.Minute || d > 121*time.Minute {
		t.Fatalf("/pemeliharaan 2j: maintenance for %v", d)
	}
	e.say(t, "/akhiri toko")
	if state().MaintenanceUntil != 0 {
		t.Fatal("/akhiri did not end maintenance")
	}
	e.say(t, "/selesai toko") // a second spelling of the same command is harmless on an idle site
	if r := e.say(t, "/hapus toko"); !strings.Contains(r, "/remove "+id+" yes") || len(e.reg.List()) != 1 {
		t.Fatalf("/hapus without confirmation must ask first: %q", r)
	}
	if r := e.say(t, "/hapus toko ya"); !strings.Contains(r, "removed") || len(e.reg.List()) != 0 {
		t.Fatalf("/hapus ... ya: %q", r)
	}
	if r := e.say(t, "/bantuan"); !strings.Contains(r, "/remove") {
		t.Fatalf("/bantuan is help: %q", r)
	}
}

func TestEveryAliasPointsAtARealCommand(t *testing.T) {
	e := start(t, nil)
	b := New(notify.NewTelegram("123:SECRET", []string{"100"}), e.reg, e.mon)
	for alias, want := range commandAliases {
		if primary(alias) != want {
			t.Errorf("primary(%q) = %q, want %q", alias, primary(alias), want)
		}
		if _, isAlias := commandAliases[want]; isAlias {
			t.Errorf("%q points at %q, which is itself an alias", alias, want)
		}
		if r := b.run(context.Background(), want, nil); strings.Contains(r, "Unknown command") {
			t.Errorf("alias %q -> %q is not a command run() knows", alias, want)
		}
	}
	for _, name := range []string{"help", "status", "report", "list", "check", "pause", "resume", "maintenance", "endmaint", "add", "remove"} {
		if r := b.run(context.Background(), name, nil); strings.Contains(r, "Unknown command") {
			t.Errorf("primary command %q is unknown to run()", name)
		}
	}
}

func TestRepliesFollowTheConfiguredLanguage(t *testing.T) {
	t.Cleanup(func() { _ = i18n.SetLang("en") }) // registered first, so it runs after the bot has stopped
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	e := start(t, nil)
	if r := e.say(t, "/status"); !strings.Contains(r, "Belum ada situs") || !strings.Contains(r, "/tambah") {
		t.Fatalf("status in Indonesian: %q", r)
	}
	help := e.say(t, "/help")
	for _, want := range []string{"perintah", "/laporan", "/pemeliharaan", "/hapus"} {
		if !strings.Contains(help, want) {
			t.Fatalf("Indonesian help lacks %q: %s", want, help)
		}
	}
	// English commands still work in an Indonesian chat.
	if r := e.say(t, "/add toko.contoh.com Toko"); !strings.Contains(r, "ditambahkan") {
		t.Fatalf("/add in an Indonesian chat: %q", r)
	}
	if r := e.say(t, "/status"); !strings.Contains(r, "1 situs") {
		t.Fatalf("%q", r)
	}
	if r := e.say(t, "/remove toko"); !strings.Contains(r, "/hapus") || !strings.Contains(r, " ya") {
		t.Fatalf("the confirmation prompt must show the Indonesian command: %q", r)
	}
	e.say(t, "/remove toko yes") // English confirmation word is accepted too
	if len(e.reg.List()) != 0 {
		t.Fatal("'yes' must confirm in an Indonesian chat")
	}
}
