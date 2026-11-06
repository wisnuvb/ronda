package web

import (
	"encoding/json"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	// t('...'), t("..."), tNodes('...'): the message must be a plain string literal so it can be found here.
	callRe = regexp.MustCompile(`\b(?:t|tNodes)\(\s*('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*")`)
	// data-i18n="..." and data-i18n-aria-label="..." in the static HTML.
	attrRe     = regexp.MustCompile(`\bdata-i18n(-[a-z-]+)?="([^"]*)"`)
	textAttrRe = regexp.MustCompile(`\bdata-i18n="([^"]+)"[^>]*>([^<]*)<`)
	placeRe    = regexp.MustCompile(`\{(\w+)\}`)
	escapeRe   = regexp.MustCompile(`\\(.)`)
)

func unquoteJS(q string) string {
	return escapeRe.ReplaceAllStringFunc(q[1:len(q)-1], func(m string) string {
		if m[1] == 'n' {
			return "\n"
		}
		return m[1:]
	})
}

func placeholders(s string) string {
	var out []string
	seen := map[string]bool{}
	for _, m := range placeRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// isComment reports whether a source line is only a comment, so examples such as t('Add site') in the
// documentation are not mistaken for messages.
func isComment(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") || strings.HasPrefix(s, "*")
}

// usedMessages maps every message the dashboard can show to where it is used. i18n.js is the library
// (its comments and examples are not messages) and i18n-id.js is the catalog itself.
func usedMessages(t *testing.T) map[string]string {
	t.Helper()
	static := Static()
	out := map[string]string{}
	js, _ := fs.Glob(static, "*.js")
	for _, name := range js {
		if name == "i18n.js" || name == "i18n-id.js" {
			continue
		}
		b, _ := fs.ReadFile(static, name)
		for i, line := range strings.Split(string(b), "\n") {
			if isComment(line) {
				continue
			}
			for _, m := range callRe.FindAllStringSubmatch(line, -1) {
				msg := unquoteJS(m[1])
				if _, ok := out[msg]; !ok {
					out[msg] = name + ":" + itoa(i+1)
				}
			}
		}
	}
	html, _ := fs.Glob(static, "*.html")
	for _, name := range html {
		b, _ := fs.ReadFile(static, name)
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range attrRe.FindAllStringSubmatch(line, -1) {
				if _, ok := out[m[2]]; !ok {
					out[m[2]] = name + ":" + itoa(i+1)
				}
			}
		}
	}
	return out
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// idCatalog reads i18n-id.js, which is strict JSON after "export default", and reports duplicate keys, which
// a JS object literal would silently let the last one win.
func idCatalog(t *testing.T) map[string]string {
	t.Helper()
	b, err := fs.ReadFile(Static(), "i18n-id.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "export default ")
	if i < 0 {
		t.Fatal(`i18n-id.js must contain "export default {...};"`)
	}
	body := strings.TrimSpace(src[i+len("export default "):])
	body = strings.TrimSuffix(body, ";")
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("i18n-id.js: not a JSON object after export default: %v", err)
	}
	cat := map[string]string{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("i18n-id.js: %v (it must be strict JSON: double quotes, one entry per line, no trailing comma)", err)
		}
		var v string
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("i18n-id.js: value of %q: %v", k, err)
		}
		key := k.(string)
		if _, dup := cat[key]; dup {
			t.Errorf("i18n-id.js: duplicate entry %q", key)
		}
		cat[key] = v
	}
	return cat
}

// Every message the code can show has a translation with the same placeholders, and the catalog holds nothing
// the code no longer uses. Without this a typo in a key silently shows English in the Indonesian dashboard.
func TestIndonesianCatalogMatchesTheCode(t *testing.T) {
	used := usedMessages(t)
	if len(used) < 100 {
		t.Fatalf("found only %d messages: has the scan broken?", len(used))
	}
	cat := idCatalog(t)

	var missing, stale []string
	for msg, where := range used {
		tr, ok := cat[msg]
		if !ok || strings.TrimSpace(tr) == "" {
			missing = append(missing, where+": "+msg)
			continue
		}
		if placeholders(msg) != placeholders(tr) {
			t.Errorf("%s: placeholders differ: %q -> %q", where, msg, tr)
		}
	}
	for msg := range cat {
		if _, ok := used[msg]; !ok {
			stale = append(stale, msg)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("%d messages have no Indonesian translation in i18n-id.js:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("%d entries of i18n-id.js are not used by the code (renamed or removed messages?):\n%s", len(stale), strings.Join(stale, "\n"))
	}
}

// The English text written in the HTML is what shows when scripts fail, so it must be the message it names.
func TestHTMLFallbackTextIsTheMessageKey(t *testing.T) {
	for _, name := range []string{"index.html", "status.html"} {
		b, _ := fs.ReadFile(Static(), name)
		for _, m := range textAttrRe.FindAllStringSubmatch(string(b), -1) {
			if strings.TrimSpace(m[2]) != m[1] {
				t.Errorf("%s: data-i18n=%q but the element reads %q", name, m[1], m[2])
			}
		}
	}
}

// Nothing user-visible may be hard-coded in a language or a zone: text goes through t(), clock times through
// the configured time zone.
func TestNoHardCodedLanguageOrZone(t *testing.T) {
	banned := regexp.MustCompile(`\b(WIB|Asia/Jakarta)\b|'id-ID'|\b(Gagal|Berhasil|Memuat|Simpan|Hapus|Batal|Tambah|Situs|Ekspor|Impor|detik|menit)\b`)
	static := Static()
	files, _ := fs.Glob(static, "*.js")
	for _, name := range files {
		if name == "i18n-id.js" {
			continue
		}
		b, _ := fs.ReadFile(static, name)
		for i, line := range strings.Split(string(b), "\n") {
			if isComment(line) {
				continue
			}
			// The one legitimate use of the locale tag is the language table in i18n.js.
			if name == "i18n.js" && strings.Contains(line, "'id-ID'") {
				continue
			}
			if m := banned.FindString(line); m != "" {
				t.Errorf("%s:%d hard-codes %q: use t() or the configured zone\n\t%s", name, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}

// ---- behaviour, by running the real modules in Node (skipped when Node is not installed) ----

func runNode(t *testing.T, cfg map[string]string, script string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir, err := filepath.Abs("static")
	if err != nil {
		t.Fatal(err)
	}
	cfgJSON := "undefined"
	if cfg != nil {
		b, _ := json.Marshal(cfg)
		cfgJSON = string(b)
	}
	// The config is set before the modules load, like /app-config.js does; each call is its own process
	// because the modules read it once.
	prog := `globalThis.window = { RONDA: ` + cfgJSON + ` }; if (!window.RONDA) delete window.RONDA;
const base = 'file://` + dir + `/';
const i18n = await import(base + 'i18n.js');
const fmt = await import(base + 'fmt.js');
const sched = await import(base + 'schedule.js');
` + script
	cmd := exec.Command(node, "--input-type=module", "-e", prog)
	cmd.Dir = t.TempDir() // not the static directory: an interpreter loads from its working directory
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestClockTimesAndZoneNames(t *testing.T) {
	const script = `
const jul = Date.UTC(2026, 6, 1, 12), jan = Date.UTC(2026, 0, 1, 12);
console.log([fmt.fmtDateTime(jul), fmt.fmtDateTime(jan), fmt.fmtClockZone(jul, true), fmt.fmtShort(jul), fmt.fmtDay(jul)].join(' | '));`
	cases := []struct {
		name string
		cfg  map[string]string
		want string
	}{
		// en-GB knows CEST/CET, so a European zone matches what the server prints.
		{"Berlin, English", map[string]string{"lang": "en", "timezone": "Europe/Berlin"},
			"1 Jul 2026 14:00:00 CEST | 1 Jan 2026 13:00:00 CET | 14:00:00 CEST | 1 Jul 14:00 | 1 Jul"},
		{"New York, English", map[string]string{"lang": "en", "timezone": "America/New_York"},
			"1 Jul 2026 08:00:00 EDT | 1 Jan 2026 07:00:00 EST | 08:00:00 EDT | 1 Jul 08:00 | 1 Jul"},
		// Intl has no abbreviation for Jakarta outside the Indonesian locale: English shows the offset.
		{"Jakarta, English", map[string]string{"lang": "en", "timezone": "Asia/Jakarta"},
			"1 Jul 2026 19:00:00 GMT+7 | 1 Jan 2026 19:00:00 GMT+7 | 19:00:00 GMT+7 | 1 Jul 19:00 | 1 Jul"},
		{"Jakarta, Indonesian", map[string]string{"lang": "id", "timezone": "Asia/Jakarta"},
			"1 Jul 2026 19:00:00 WIB | 1 Jan 2026 19:00:00 WIB | 19:00:00 WIB | 1 Jul 19:00 | 1 Jul"},
		{"UTC", map[string]string{"lang": "en", "timezone": "UTC"},
			"1 Jul 2026 12:00:00 UTC | 1 Jan 2026 12:00:00 UTC | 12:00:00 UTC | 1 Jul 12:00 | 1 Jul"},
		// No /app-config.js at all, or a zone the browser does not know: English and UTC, never a crash.
		{"no config", nil, "1 Jul 2026 12:00:00 UTC | 1 Jan 2026 12:00:00 UTC | 12:00:00 UTC | 1 Jul 12:00 | 1 Jul"},
		{"bad zone", map[string]string{"lang": "en", "timezone": "Mars/Olympus"},
			"1 Jul 2026 12:00:00 UTC | 1 Jan 2026 12:00:00 UTC | 12:00:00 UTC | 1 Jul 12:00 | 1 Jul"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runNode(t, c.cfg, script); got != c.want {
				t.Errorf("\n got  %s\n want %s", got, c.want)
			}
		})
	}
}

func TestLanguageNumbersAndSentences(t *testing.T) {
	const script = `
const { t } = i18n;
console.log([
  i18n.lang, t('Cancel'), t('Not in any catalog {x}', { x: 7 }), t('Sign in to {app}'),
  fmt.fmtPct(99.5), fmt.fmtMs(1500), fmt.fmtMs(250), fmt.fmtDur(4 * 60 + 12), fmt.ago(Date.now() - 90000),
  fmt.fmtLong(7200), fmt.fmtLong(604800), fmt.fmtLong(90), fmt.fmtLong(2592000),
  sched.describeWindow({ days: [6, 0], start: '02:00', minutes: 120 }),
  sched.describeWindow({ days: [0, 1, 2, 3, 4, 5, 6], start: '03:30', minutes: 30 }),
  fmt.weekdayName(1, 'short'), fmt.weekdayName(0, 'long'),
].join(' | '));`
	en := "en | Cancel | Not in any catalog 7 | Sign in to ronda | 99.50% | 1.5 s | 250 ms | 4 min 12 s | 1 min ago | 2 hours | 1 week | 90 seconds | 30 days | " +
		"Every Saturday and Sunday at 02:00, for 2 hours | Every day at 03:30, for 30 minutes | Mon | Sunday"
	id := "id | Batal | Not in any catalog 7 | Masuk ke ronda | 99,50% | 1,5 dtk | 250 ms | 4 mnt 12 dtk | 1 mnt lalu | 2 jam | 1 minggu | 90 detik | 30 hari | " +
		"Setiap Sabtu dan Minggu pukul 02:00, selama 2 jam | Setiap hari pukul 03:30, selama 30 menit | Sen | Minggu"
	if got := runNode(t, map[string]string{"lang": "en", "timezone": "UTC"}, script); got != en {
		t.Errorf("English:\n got  %s\n want %s", got, en)
	}
	if got := runNode(t, map[string]string{"lang": "id", "timezone": "UTC"}, script); got != id {
		t.Errorf("Indonesian:\n got  %s\n want %s", got, id)
	}
	// A language the dashboard does not have falls back to English rather than showing keys or breaking.
	if got := runNode(t, map[string]string{"lang": "fr", "timezone": "UTC"}, `console.log(i18n.lang + ' ' + i18n.t('Cancel'))`); got != "en Cancel" {
		t.Errorf("unknown language: %q", got)
	}
}

// Messages are never half-translated: every Indonesian message with placeholders renders with the values in
// it and no leftover "{...}", which would mean a placeholder name was mistyped in one language only.
func TestEveryIndonesianMessageRendersItsPlaceholders(t *testing.T) {
	cat := idCatalog(t)
	vars := map[string]string{}
	for msg := range cat {
		for _, m := range placeRe.FindAllStringSubmatch(msg, -1) {
			vars[m[1]] = "<" + m[1] + ">"
		}
	}
	b, _ := json.Marshal(vars)
	keys, _ := json.Marshal(sortedKeys(cat))
	out := runNode(t, map[string]string{"lang": "id", "timezone": "UTC"}, `
const vars = `+string(b)+`;
const bad = [];
for (const k of `+string(keys)+`) {
  const s = i18n.t(k, vars);
  if (/\{\w+\}/.test(s) && !/^\{app\}/.test(k)) bad.push(k + ' => ' + s);
}
console.log(bad.length ? bad.join('\n') : 'ok');`)
	if out != "ok" {
		t.Errorf("messages with unfilled placeholders:\n%s", out)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
