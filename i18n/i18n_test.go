package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestFallsBackToEnglish(t *testing.T) {
	t.Cleanup(func() {
		_ = SetLang(Default)
		// The throwaway language must not outlive this test, or the catalog-completeness test would
		// check it against every message in the code.
		mu.Lock()
		delete(catalogs, "xx")
		mu.Unlock()
	})
	Register("xx", map[string]string{"Hello %s": "Halo %s"})
	if err := SetLang("xx"); err != nil {
		t.Fatal(err)
	}
	if got := T("Hello %s", "dunia"); got != "Halo dunia" {
		t.Fatalf("translated: %q", got)
	}
	if got := T("Not in the catalog %d", 3); got != "Not in the catalog 3" {
		t.Fatalf("fallback: %q", got)
	}
	if got := T("100% sure"); got != "100% sure" {
		t.Fatalf("a literal percent with no args must survive: %q", got)
	}
	if err := SetLang("zz"); err == nil {
		t.Fatal("an unknown language must be refused")
	}
	if Lang() != "xx" {
		t.Fatalf("a refused SetLang must not change the language: %q", Lang())
	}
}

// literalArg returns the string a call's first argument spells out, joining "a" + "b".
func literalArg(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, ok1 := literalArg(v.X)
		r, ok2 := literalArg(v.Y)
		return l + r, ok1 && ok2
	case *ast.ParenExpr:
		return literalArg(v.X)
	}
	return "", false
}

// used collects every message passed as a literal to i18n.T or i18n.N in the module's Go code.
func used(t *testing.T) map[string]string {
	t.Helper()
	root := ".."
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "dist", "node_modules", "data":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "i18n" || (sel.Sel.Name != "T" && sel.Sel.Name != "N") {
				return true
			}
			if s, ok := literalArg(call.Args[0]); ok {
				out[s] = fset.Position(call.Pos()).String()
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every message in the code must exist in every shipped catalog; otherwise that language would silently
// show English in the middle of a sentence.
func TestShippedCatalogsAreComplete(t *testing.T) {
	msgs := used(t)
	if len(msgs) < 20 {
		t.Fatalf("found only %d messages: has the scan broken?", len(msgs))
	}
	for _, lang := range Supported() {
		if lang == Default {
			continue
		}
		var missing []string
		mu.RLock()
		for m, pos := range msgs {
			if catalogs[lang][m] == "" {
				missing = append(missing, pos+": "+strconv.Quote(m))
			}
		}
		mu.RUnlock()
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: %d messages have no translation:\n%s", lang, len(missing), strings.Join(missing, "\n"))
		}
	}
}

// A translation must take the same verbs as its source, or Sprintf prints %!d(MISSING).
func TestTranslationsKeepTheirFormatVerbs(t *testing.T) {
	verbs := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] != '%' {
				continue
			}
			i++
			for i < len(s) && strings.IndexByte("+-# 0123456789.*", s[i]) >= 0 {
				i++
			}
			if i < len(s) && s[i] != '%' {
				b.WriteByte(s[i])
			}
		}
		return b.String()
	}
	mu.RLock()
	defer mu.RUnlock()
	for lang, c := range catalogs {
		for src, tr := range c {
			if verbs(src) != verbs(tr) {
				t.Errorf("%s: %q -> %q: format verbs differ (%q vs %q)", lang, src, tr, verbs(src), verbs(tr))
			}
		}
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
