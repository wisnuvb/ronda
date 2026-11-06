// Package i18n translates the messages the server produces (Telegram, API errors, CLI, reports).
//
// The key of a message is its English text, so the code reads naturally and English needs no catalog:
//
//	i18n.T("Cause: %s", why)
//
// Other languages are catalogs of English text -> translation, registered from init() in files of this
// package (id_*.go). A message with no entry in the active language falls back to English, so a missing
// translation is never an empty string. i18n_test.go fails the build if a message used in the code has
// no entry in a shipped catalog, which keeps them complete.
//
// The language is process-wide: one ronda instance speaks one language (config "language"). Set it once
// at startup, before anything runs.
package i18n

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Default is the language of the source text; it is always available.
const Default = "en"

var (
	mu       sync.RWMutex
	catalogs = map[string]map[string]string{}
	current  atomic.Value // string
)

func init() { current.Store(Default) }

// Register adds translations for lang. Calling it again for the same language merges the entries; a
// later entry for the same key wins.
func Register(lang string, entries map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	c := catalogs[lang]
	if c == nil {
		c = make(map[string]string, len(entries))
		catalogs[lang] = c
	}
	for k, v := range entries {
		c[k] = v
	}
}

// Supported lists the languages that can be selected, the default first.
func Supported() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := []string{Default}
	var rest []string
	for l := range catalogs {
		if l != Default {
			rest = append(rest, l)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// SetLang selects the language. An empty string means the default.
func SetLang(lang string) error {
	if lang == "" {
		lang = Default
	}
	mu.RLock()
	_, ok := catalogs[lang]
	mu.RUnlock()
	if !ok && lang != Default {
		return fmt.Errorf("unsupported language %q (available: %v)", lang, Supported())
	}
	current.Store(lang)
	return nil
}

// Lang returns the active language.
func Lang() string { return current.Load().(string) }

// T returns msg in the active language. With args it is fmt.Sprintf(translation, args...); without,
// the text is returned as is, so a literal "%" in a message with no arguments is safe.
func T(msg string, args ...any) string {
	s := lookup(msg)
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// N marks msg for translation without translating it now, for text that is stored in a table or a
// variable and passed to T later (like gettext's N_). i18n_test.go treats it like T.
func N(msg string) string { return msg }

func lookup(msg string) string {
	lang := Lang()
	if lang == Default {
		return msg
	}
	mu.RLock()
	defer mu.RUnlock()
	if s, ok := catalogs[lang][msg]; ok && s != "" {
		return s
	}
	return msg
}

// decimalComma lists the languages that write 90,5 rather than 90.5.
var decimalComma = map[string]bool{"id": true}

// Float formats v with prec decimals using the active language's decimal separator.
func Float(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if decimalComma[Lang()] {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}
