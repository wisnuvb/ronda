package web

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Scripts the server generates instead of serving from the embedded files: /app-config.js tells the page
// which language and time zone it runs in. They are not in static/, but a reverse proxy still has to let
// them through for the public status page, which the README test below checks.
var dynamicRoutes = map[string]bool{"/app-config.js": true}

var (
	importRe = regexp.MustCompile(`(?m)^\s*(?:import|export)[^'"\n]*?from\s+'(\./[^']+)'`)
	srcRe    = regexp.MustCompile(`(?:src|href)="(/[^"]+)"`)
)

// There is no build step, so nothing else notices a module that is imported but missing from the binary.
func TestEveryReferencedFileIsEmbedded(t *testing.T) {
	static := Static()

	index, err := fs.ReadFile(static, "index.html")
	if err != nil {
		t.Fatal("index.html not embedded:", err)
	}
	// Routes the server answers itself, mapped to the embedded file behind each.
	routes := map[string]string{"/status": "status.html"}
	for _, m := range srcRe.FindAllStringSubmatch(string(index), -1) {
		file := strings.TrimPrefix(m[1], "/")
		if dynamicRoutes[m[1]] {
			continue
		}
		if backing, ok := routes[m[1]]; ok {
			file = backing
		}
		if _, err := fs.Stat(static, file); err != nil {
			t.Errorf("index.html references %s, which is not embedded (looked for %s)", m[1], file)
		}
	}

	// The public page must be as self-contained as the dashboard.
	status, err := fs.ReadFile(static, "status.html")
	if err != nil {
		t.Fatal("status.html not embedded:", err)
	}
	for _, m := range srcRe.FindAllStringSubmatch(string(status), -1) {
		if dynamicRoutes[m[1]] {
			continue
		}
		if _, err := fs.Stat(static, strings.TrimPrefix(m[1], "/")); err != nil {
			t.Errorf("status.html references %s, which is not embedded", m[1])
		}
	}

	js, _ := fs.Glob(static, "*.js")
	if len(js) < 10 {
		t.Fatalf("only %d JS files embedded: %v", len(js), js)
	}
	for _, name := range js {
		b, _ := fs.ReadFile(static, name)
		for _, m := range importRe.FindAllStringSubmatch(string(b), -1) {
			if _, err := fs.Stat(static, strings.TrimPrefix(m[1], "./")); err != nil {
				t.Errorf("%s imports %s, which is not embedded", name, m[1])
			}
		}
	}
}

// The CSP forbids inline script and style, so a stray inline handler would silently do nothing in production.
func TestNoInlineScriptOrStyleInHTML(t *testing.T) {
	for _, name := range []string{"index.html", "status.html"} {
		checkNoInline(t, name)
	}
}

func checkNoInline(t *testing.T, name string) {
	b, err := fs.ReadFile(Static(), name)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	inline := regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>\s*\S`)
	for _, m := range inline.FindAllString(html, -1) {
		if !strings.Contains(m, "src=") {
			t.Errorf("inline <script> would be blocked by the CSP: %q", m)
		}
	}
	for _, bad := range []string{"<style", " style=", " onclick=", " onload=", " onerror="} {
		if strings.Contains(strings.ToLower(html), bad) {
			t.Errorf("%s contains %q, which the CSP blocks", name, bad)
		}
	}
}

func TestNoInlineStyleAttributesInScripts(t *testing.T) {
	static := Static()
	js, _ := fs.Glob(static, "*.js")
	for _, name := range js {
		b, _ := fs.ReadFile(static, name)
		src := string(b)
		for _, bad := range []string{"setAttribute('style'", `setAttribute("style"`, "innerHTML +=", "insertAdjacentHTML", "document.write", "eval(", "new Function("} {
			if strings.Contains(src, bad) {
				t.Errorf("%s uses %s, which the CSP blocks or which risks injecting markup", name, bad)
			}
		}
		// innerHTML is allowed only for the static icon paths.
		if strings.Contains(src, "innerHTML") && name != "icons.js" {
			t.Errorf("%s uses innerHTML; only icons.js may, with static markup", name)
		}
	}
}

// The README tells people to expose only the files the public status page needs. If status.js grows an
// import, the page would break behind that proxy rule and nobody would notice until a visitor did.
func TestPublicPageFilesAreAllListedInTheREADME(t *testing.T) {
	static := Static()
	need := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		if need[name] {
			return
		}
		need[name] = true
		if !strings.HasSuffix(name, ".js") || dynamicRoutes["/"+name] {
			return
		}
		b, err := fs.ReadFile(static, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, m := range importRe.FindAllStringSubmatch(string(b), -1) {
			walk(strings.TrimPrefix(m[1], "./"))
		}
	}
	html, _ := fs.ReadFile(static, "status.html")
	for _, m := range srcRe.FindAllStringSubmatch(string(html), -1) {
		walk(strings.TrimPrefix(m[1], "/"))
	}
	if len(need) < 6 {
		t.Fatalf("suspiciously few files for the public page: %v", need)
	}

	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for name := range need {
		if !strings.Contains(string(readme), strings.ReplaceAll(name, ".", `\.`)) {
			t.Errorf("the public status page needs %s, but the README's reverse-proxy example does not list it", name)
		}
	}
}
