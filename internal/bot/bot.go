// Package bot lets an authorised Telegram chat manage ronda: see status, add, pause and
// remove sites. It long-polls Telegram, so it needs no public address and works behind NAT.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wisnuvb/ronda/sitespec"
	"html"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/monitor"
	"github.com/wisnuvb/ronda/notify"
	"github.com/wisnuvb/ronda/report"
)

type Bot struct {
	tg      *notify.Telegram
	allowed map[string]bool
	admins  map[int64]bool // user ids that may change things from a group chat
	hist    *history.Store // for /report; optional
	reg     *registry.Registry
	mon     *monitor.Monitor
	client  *http.Client
	pause   func(ctx context.Context, d time.Duration)

	offset  int64
	lastErr string
}

// New serves the chats in tg.ChatIDs and nobody else.
func New(tg *notify.Telegram, reg *registry.Registry, mon *monitor.Monitor) *Bot {
	allowed := map[string]bool{}
	for _, id := range tg.ChatIDs {
		allowed[id] = true
	}
	return &Bot{
		tg: tg, allowed: allowed, reg: reg, mon: mon,
		client: &http.Client{Timeout: 45 * time.Second}, // longer than the 30s long poll
		pause: func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		},
	}
}

// WithHistory lets /report cover uptime over the past week.
func (b *Bot) WithHistory(h *history.Store) *Bot {
	b.hist = h
	return b
}

// WithAdmins sets who may run commands that change things when the message comes from a group.
// In a private chat the one person is already the chat owner; in a group every member is not.
func (b *Bot) WithAdmins(ids []int64) *Bot {
	b.admins = map[int64]bool{}
	for _, id := range ids {
		b.admins[id] = true
	}
	return b
}

// commandAliases maps the other accepted spellings of a command, the Indonesian ones the bot started with
// among them, to its primary name. Primary names are English: help, status, report, list, check, pause,
// resume, maintenance, endmaint, add, remove.
var commandAliases = map[string]string{
	"start": "help", "bantuan": "help",
	"laporan":      "report",
	"daftar":       "list",
	"cek":          "check",
	"jeda":         "pause",
	"lanjut":       "resume",
	"pemeliharaan": "maintenance",
	"akhiri":       "endmaint", "selesai": "endmaint", "endmaintenance": "endmaint",
	"tambah": "add",
	"hapus":  "remove", "delete": "remove",
}

// primary returns the primary name of a command typed in any accepted spelling.
func primary(cmd string) string {
	if p, ok := commandAliases[cmd]; ok {
		return p
	}
	return cmd
}

// mutating lists the commands (by primary name) that change state; everything else only reads.
var mutating = map[string]bool{
	"pause": true, "resume": true, "maintenance": true, "endmaint": true, "add": true, "remove": true,
}

type update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return fmt.Sprintf("telegram %d: %s", e.code, e.msg) }

func (b *Bot) getUpdates(ctx context.Context, offset int64, timeout int) ([]update, error) {
	body, _ := json.Marshal(map[string]any{"offset": offset, "timeout": timeout, "allowed_updates": []string{"message"}})
	url := strings.TrimRight(b.tg.BaseURL, "/") + "/bot" + b.tg.Token + "/getUpdates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New(b.tg.Redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, errors.New(b.tg.Redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out struct {
		OK          bool     `json:"ok"`
		Result      []update `json:"result"`
		Description string   `json:"description"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || !out.OK {
		return nil, &apiError{resp.StatusCode, out.Description}
	}
	return out.Result, nil
}

// Run polls until ctx ends. Commands that piled up while ronda was down are skipped on purpose:
// replaying a stale "/remove" an hour later would be a nasty surprise.
func (b *Bot) Run(ctx context.Context) {
	if ups, err := b.getUpdates(ctx, -1, 0); err == nil && len(ups) > 0 {
		b.offset = ups[len(ups)-1].ID + 1
	}
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		ups, err := b.getUpdates(ctx, b.offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.logErr(err)
			b.pause(ctx, backoff)
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		backoff = 5 * time.Second
		b.lastErr = ""
		for _, u := range ups {
			b.offset = u.ID + 1
			b.handle(ctx, u)
		}
	}
}

// logErr says what is wrong once, in words that point at the fix, instead of every retry.
func (b *Bot) logErr(err error) {
	msg := err.Error()
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.code {
		case http.StatusConflict:
			msg = "another bot is polling with the same token (409). Stop the other one, or turn off notify.telegram_commands on one of them."
		case http.StatusUnauthorized:
			msg = "Telegram rejected the bot token (401). Check notify.telegram_token."
		}
	}
	if msg != b.lastErr {
		log.Printf("bot: %s", msg)
		b.lastErr = msg
	}
}

func (b *Bot) handle(ctx context.Context, u update) {
	if u.Message == nil || u.Message.Text == "" {
		return
	}
	chat := strconv.FormatInt(u.Message.Chat.ID, 10)
	text := strings.TrimSpace(u.Message.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}
	if !b.allowed[chat] {
		// Say nothing back (a reply would invite strangers), but log the id: it is what you put in the config.
		log.Printf("bot: command from chat %s ignored; that id is not in notify.telegram_chat_id", chat)
		return
	}
	fields := strings.Fields(text)
	cmd := strings.ToLower(strings.SplitN(fields[0][1:], "@", 2)[0])
	name := primary(cmd)
	var reply string
	// Fail closed: only a chat Telegram says is "private" is trusted as one person.
	if mutating[name] && u.Message.Chat.Type != "private" && !b.admins[u.Message.From.ID] {
		log.Printf("bot: /%s from user %d in chat %s refused; that user id is not in notify.telegram_admin_ids",
			cmd, u.Message.From.ID, chat)
		reply = i18n.T("⚠️ In a group, commands that change data are for Ronda admins only. You can still use /status, /list and /check.")
	} else {
		reply = b.run(ctx, name, fields[1:])
	}
	log.Printf("bot: /%s from chat %s", cmd, chat)
	if err := b.tg.SendText(chat, reply); err != nil {
		log.Printf("bot: could not reply: %v", err)
	}
}

func helpText() string {
	return i18n.T(`<b>Ronda</b> — commands:
/status — summary, and the sites that are down
/list — all sites
/report [<i>daily</i>|<i>weekly</i>] — summary of uptime, incidents and certificates (default: weekly)
/check <i>site</i> — check now
/pause <i>site</i> · /resume <i>site</i>
/maintenance <i>site</i> <i>duration</i> — e.g. 30m, 2h, 1d (stops checking, then resumes by itself)
/endmaint <i>site</i> — end maintenance
/add <i>url</i> [name] — add a site; <code>tcp://host:port</code> checks a port, <code>dns://name</code> checks a DNS record
/remove <i>site</i> yes — remove a site

<i>site</i> can be an id, a name, or part of a name or address.`)
}

// run executes a command, given by its primary name (see primary).
func (b *Bot) run(ctx context.Context, cmd string, args []string) string {
	switch cmd {
	case "help":
		return helpText()
	case "status":
		return b.status()
	case "report":
		return b.report(args)
	case "list":
		return b.list()
	case "check":
		return b.withSite(args, func(s model.Site) string { return b.check(ctx, s) })
	case "pause":
		return b.withSite(args, func(s model.Site) string {
			if _, err := b.reg.SetPaused(s.ID, true); err != nil {
				return failure(err)
			}
			return i18n.T("⏸ <b>%s</b> paused. No checks or notifications until /resume.", esc(s.Name))
		})
	case "resume":
		return b.withSite(args, func(s model.Site) string {
			if _, err := b.reg.SetPaused(s.ID, false); err != nil {
				return failure(err)
			}
			return i18n.T("▶️ <b>%s</b> resumed and checked right away.", esc(s.Name))
		})
	case "maintenance":
		return b.maintenance(args)
	case "endmaint":
		return b.withSite(args, func(s model.Site) string {
			if _, err := b.reg.EndMaintenance(s.ID); err != nil {
				return failure(err)
			}
			return i18n.T("▶️ Maintenance on <b>%s</b> ended; the site is checked right away.", esc(s.Name))
		})
	case "add":
		return b.add(args)
	case "remove":
		return b.remove(args)
	}
	return i18n.T("Unknown command. Send /help for the list of commands.")
}

func (b *Bot) report(args []string) string {
	kind := report.Weekly
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "daily", "harian", "hari":
			kind = report.Daily
		case "weekly", "mingguan", "minggu":
		default:
			return i18n.T("⚠️ Use <code>/report daily</code> or <code>/report weekly</code>.")
		}
	}
	sites, incs := report.Collect(b.mon, b.hist)
	return report.Build(kind, sites, incs, time.Now()).HTML()
}

func esc(s string) string { return html.EscapeString(s) }

// plural picks the singular or the plural form of a message for n and formats it with n and rest.
// Both forms are passed through i18n.N at the call site so the catalog check sees them.
func plural(n int, one, many string, rest ...any) string {
	if n == 1 {
		return i18n.T(one, append([]any{n}, rest...)...)
	}
	return i18n.T(many, append([]any{n}, rest...)...)
}

func failure(err error) string {
	var inv *model.InvalidError
	if errors.As(err, &inv) {
		return "⚠️ " + esc(err.Error())
	}
	if errors.Is(err, model.ErrNotFound) {
		return i18n.T("⚠️ Site not found.")
	}
	log.Printf("bot: %v", err)
	return i18n.T("⚠️ An internal error occurred.")
}

var stateIcon = map[string]string{
	monitor.StUp: "🟢", monitor.StDown: "🔴", monitor.StPaused: "⏸", monitor.StMaint: "🔧", monitor.StUnknown: "⚪",
}

func pct(v *float64) string {
	if v == nil {
		return "–"
	}
	if *v == 100 {
		return "100%"
	}
	return i18n.Float(*v, 2) + "%"
}

func (b *Bot) views() []monitor.View {
	vs := b.mon.Views()
	rank := map[string]int{monitor.StDown: 0, monitor.StUnknown: 1, monitor.StUp: 2, monitor.StMaint: 3, monitor.StPaused: 4}
	slices.SortFunc(vs, func(a, c monitor.View) int {
		if d := rank[a.State] - rank[c.State]; d != 0 {
			return d
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(c.Name))
	})
	return vs
}

func (b *Bot) status() string {
	vs := b.views()
	if len(vs) == 0 {
		return i18n.T("No sites yet. Add one with /add <i>url</i>.")
	}
	n := map[string]int{}
	for _, v := range vs {
		n[v.State]++
	}
	var sb strings.Builder
	sb.WriteString(plural(len(vs),
		i18n.N("<b>%d site</b>: 🟢 %d up · 🔴 %d down · ⏸ %d paused · 🔧 %d maintenance"),
		i18n.N("<b>%d sites</b>: 🟢 %d up · 🔴 %d down · ⏸ %d paused · 🔧 %d maintenance"),
		n[monitor.StUp], n[monitor.StDown], n[monitor.StPaused], n[monitor.StMaint]))
	if n[monitor.StUnknown] > 0 {
		sb.WriteString(i18n.T(" · ⚪ %d pending", n[monitor.StUnknown]))
	}
	for _, v := range vs {
		if v.State != monitor.StDown {
			continue
		}
		why := ""
		if v.Last != nil {
			why = " — " + model.KindLabel(v.Last.Kind)
		}
		fmt.Fprintf(&sb, "\n🔴 <b>%s</b>%s", esc(v.Name), esc(why))
	}
	if n[monitor.StDown] == 0 {
		sb.WriteString("\n" + i18n.T("All active sites are healthy."))
	}
	return sb.String()
}

const listMax = 40

func (b *Bot) list() string {
	vs := b.views()
	if len(vs) == 0 {
		return i18n.T("No sites yet. Add one with /add <i>url</i>.")
	}
	var sb strings.Builder
	for i, v := range vs {
		if i == listMax {
			sb.WriteString("\n" + plural(len(vs)-listMax, i18n.N("… and %d more site (see the dashboard)"), i18n.N("… and %d more sites (see the dashboard)")))
			break
		}
		if i > 0 {
			sb.WriteByte('\n')
		}
		lat := ""
		if v.Last != nil && v.Last.OK {
			lat = fmt.Sprintf(" · %d ms", v.Last.LatencyMs)
		}
		fmt.Fprintf(&sb, "%s <b>%s</b> · %s%s · <code>%s</code>", stateIcon[v.State], esc(v.Name), pct(v.Uptime24h), lat, esc(v.ID))
	}
	return sb.String()
}

// resolve finds one site from an id, a name, or part of a name or address.
func (b *Bot) resolve(ref string) (model.Site, string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.Site{}, i18n.T("Name the site: its id, name, or part of a name. Send /list to see them.")
	}
	sites := b.reg.List()
	low := strings.ToLower(ref)
	for _, s := range sites {
		if s.ID == ref || strings.ToLower(s.Name) == low {
			return s, ""
		}
	}
	var hits []model.Site
	for _, s := range sites {
		if strings.Contains(strings.ToLower(s.Name), low) || strings.Contains(strings.ToLower(s.URL), low) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		return model.Site{}, i18n.T("No site matches “%s”. Send /list to see them.", esc(ref))
	}
	var names []string
	for i, s := range hits {
		if i == 5 {
			names = append(names, "… (+"+strconv.Itoa(len(hits)-5)+")")
			break
		}
		names = append(names, fmt.Sprintf("%s (<code>%s</code>)", esc(s.Name), esc(s.ID)))
	}
	return model.Site{}, i18n.T("%d sites match “%s”: %s.\nUse the id to be certain.", len(hits), esc(ref), strings.Join(names, ", "))
}

func (b *Bot) withSite(args []string, fn func(model.Site) string) string {
	s, msg := b.resolve(strings.Join(args, " "))
	if msg != "" {
		return msg
	}
	return fn(s)
}

func (b *Bot) check(ctx context.Context, s model.Site) string {
	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSec+5)*time.Second)
	defer cancel()
	r, err := b.mon.CheckSite(cctx, s.ID)
	if err != nil {
		return failure(err)
	}
	if r.OK {
		return i18n.T("✅ <b>%s</b>: HTTP %d · %d ms\n<i>Manual check: does not change the status.</i>", esc(s.Name), r.Status, r.LatencyMs)
	}
	return i18n.T("❌ <b>%s</b>: %s\n%s\n<i>Manual check: does not change the status.</i>", esc(s.Name), esc(model.KindLabel(r.Kind)), esc(r.Hint))
}

// parseDuration reads 90, 90m, 2h, 1d (and the Indonesian 2j, 1hari) as minutes.
func parseDuration(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n < 1 {
		return 0, false
	}
	switch s[i:] {
	case "", "m", "min", "mins", "minute", "minutes", "mnt", "menit":
		return n, true
	case "h", "hr", "hrs", "hour", "hours", "j", "jam":
		return n * 60, true
	case "d", "day", "days", "hari":
		return n * 1440, true
	}
	return 0, false
}

func (b *Bot) maintenance(args []string) string {
	usage := i18n.T("Usage: /maintenance <i>site</i> <i>duration</i>, e.g. <code>/maintenance shop 2h</code> (30m, 2h, 1d).")
	if len(args) < 2 {
		return usage
	}
	mins, ok := parseDuration(args[len(args)-1])
	if !ok {
		return usage
	}
	maxMin := int(sitespec.MaxMaintenance / time.Minute)
	if mins > maxMin {
		return plural(maxMin/1440, i18n.N("⚠️ Maintenance can last at most %d day."), i18n.N("⚠️ Maintenance can last at most %d days."))
	}
	return b.withSite(args[:len(args)-1], func(s model.Site) string {
		until := time.Now().Add(time.Duration(mins) * time.Minute)
		if _, err := b.reg.SetMaintenance(s.ID, until.UnixMilli()); err != nil {
			return failure(err)
		}
		return i18n.T("🔧 <b>%s</b> is under maintenance until %s. No checks and no notifications; it resumes by itself afterwards (or /endmaint).",
			esc(s.Name), model.Stamp(until.UnixMilli(), "2 Jan 15:04"))
	})
}

func (b *Bot) add(args []string) string {
	if len(args) == 0 {
		return i18n.T("Usage: /add <i>url</i> [name], e.g. <code>/add shop.example.com Online Shop</code>.")
	}
	raw := args[0]
	if strings.HasPrefix(strings.ToLower(raw), "push://") {
		// The ping address of a scheduled job contains a secret. A chat (and every member of a group) is
		// no place to receive it, so these are created in the dashboard.
		return i18n.T("⚠️ Scheduled jobs are created from the dashboard: their ping address holds a secret that does not belong in a chat.")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	s, err := b.reg.Add(model.Site{URL: raw, Name: strings.Join(args[1:], " ")})
	if err != nil {
		return failure(err)
	}
	return i18n.T("✅ <b>%s</b> added and checked right away.\n%s\nid: <code>%s</code>\n<i>Private/internal addresses are blocked; other options can be set in the dashboard.</i>",
		esc(s.Name), esc(s.URL), esc(s.ID))
}

func (b *Bot) remove(args []string) string {
	if len(args) == 0 {
		return i18n.T("Usage: /remove <i>site</i> yes")
	}
	confirmed := strings.EqualFold(args[len(args)-1], "yes") || strings.EqualFold(args[len(args)-1], "ya")
	ref := args
	if confirmed {
		ref = args[:len(args)-1]
	}
	return b.withSite(ref, func(s model.Site) string {
		if !confirmed {
			return i18n.T("Removing <b>%s</b> also deletes its entire uptime history and cannot be undone.\nSend <code>/remove %s yes</code> to continue.", esc(s.Name), esc(s.ID))
		}
		if err := b.reg.Delete(s.ID); err != nil {
			return failure(err)
		}
		return i18n.T("🗑 <b>%s</b> removed.", esc(s.Name))
	})
}
