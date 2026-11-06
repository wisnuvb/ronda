package check

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

func site(url string) model.Site {
	return model.Site{URL: url, TimeoutSec: 3, AllowPrivate: true}
}

func run(t *testing.T, s model.Site) model.Result {
	t.Helper()
	return New().Check(context.Background(), s)
}

func TestOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != DefaultUserAgent {
			t.Errorf("user agent = %q", got)
		}
		w.Write([]byte("<h1>Halo</h1>"))
	}))
	defer srv.Close()

	r := run(t, site(srv.URL))
	if !r.OK || r.Kind != model.KindOK || r.Status != 200 || r.LatencyMs < 1 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestCustomUserAgentAndHead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.Header.Get("User-Agent") != "x/1" {
			t.Errorf("method=%s ua=%q", r.Method, r.Header.Get("User-Agent"))
		}
	}))
	defer srv.Close()
	s := site(srv.URL)
	s.Method, s.UserAgent = "HEAD", "x/1"
	if r := run(t, s); !r.OK {
		t.Fatalf("%+v", r)
	}
}

func TestStatusClassification(t *testing.T) {
	cases := []struct {
		code int
		kind string
		hint string
	}{
		{502, model.KindHTTP5xx, "Bad Gateway"},
		{500, model.KindHTTP5xx, "HTTP 500"},
		{521, model.KindCloudflare, "Cloudflare 521"},
		{524, model.KindCloudflare, "524"},
		{403, model.KindHTTP4xx, "WAF"},
		{404, model.KindHTTP4xx, "404"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.code) }))
		r := run(t, site(srv.URL))
		srv.Close()
		if r.OK || r.Kind != c.kind || r.Status != c.code || !strings.Contains(r.Hint, c.hint) {
			t.Errorf("%d: got %+v", c.code, r)
		}
	}
}

func TestExpectStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	s := site(srv.URL)
	s.ExpectStatus = []int{401}
	if r := run(t, s); !r.OK {
		t.Fatalf("401 should be accepted: %+v", r)
	}
	s.ExpectStatus = []int{200}
	if r := run(t, s); r.OK {
		t.Fatal("401 should fail when only 200 is expected")
	}
}

func TestKeyword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>Selamat Datang di Toko</html>"))
	}))
	defer srv.Close()

	s := site(srv.URL)
	s.Keyword = "selamat datang" // case-insensitive
	if r := run(t, s); !r.OK {
		t.Fatalf("keyword should match: %+v", r)
	}
	s.Keyword = "database error"
	r := run(t, s)
	if r.OK || r.Kind != model.KindKeyword || r.Status != 200 {
		t.Fatalf("keyword should be missing: %+v", r)
	}
}

func TestTimeoutResponse(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	defer close(done)

	s := site(srv.URL)
	s.TimeoutSec = 1
	start := time.Now()
	r := run(t, s)
	if r.OK || r.Kind != model.KindTimeoutResponse {
		t.Fatalf("got %+v", r)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	r := run(t, site("http://"+addr))
	if r.OK || r.Kind != model.KindRefused {
		t.Fatalf("got %+v", r)
	}
}

func TestDNS(t *testing.T) {
	r := run(t, site("http://tidak-ada.invalid/"))
	if r.OK || r.Kind != model.KindDNS {
		t.Fatalf("got %+v", r)
	}
}

func TestPrivateBlockedByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request must not reach a private address")
	}))
	defer srv.Close()

	s := site(srv.URL)
	s.AllowPrivate = false
	r := run(t, s)
	if r.OK || r.Kind != model.KindBlocked {
		t.Fatalf("got %+v", r)
	}
}

func TestRedirects(t *testing.T) {
	var loop *httptest.Server
	loop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, loop.URL+"/again", http.StatusFound)
	}))
	defer loop.Close()

	r := run(t, site(loop.URL))
	if r.OK || r.Kind != model.KindRedirectLoop {
		t.Fatalf("loop: %+v", r)
	}

	s := site(loop.URL)
	s.NoRedirect = true
	if r := run(t, s); !r.OK || r.Status != 302 {
		t.Fatalf("no_redirect should accept the 302: %+v", r)
	}
}

func TestTLSUntrustedAndCertExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	r := run(t, site(srv.URL))
	if r.OK || r.Kind != model.KindTLSUntrusted {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyExpiredCertificate(t *testing.T) {
	cert := &x509.Certificate{NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(-24 * time.Hour)}
	err := x509.CertificateInvalidError{Cert: cert, Reason: x509.Expired}
	kind, hint, _ := classifyError(err, phases{}, 10*time.Second)
	if kind != model.KindTLSExpired || !strings.Contains(hint, "expired") {
		t.Fatalf("kind=%s hint=%s", kind, hint)
	}
}

func TestBlockedIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "192.168.0.5": true, "172.16.0.1": true,
		"169.254.169.254": true, "100.64.0.1": true, "0.0.0.0": true, "::1": true,
		"fe80::1": true, "fd00::1": true, "::ffff:127.0.0.1": true, "224.0.0.1": true,
		"8.8.8.8": false, "1.1.1.1": false, "2606:4700:4700::1111": false,
		// translation prefixes carry an IPv4 address that must be judged by the IPv4 rules
		"64:ff9b::7f00:1": true, "64:ff9b::a00:5": true, "64:ff9b::a9fe:a9fe": true, // 127.0.0.1, 10.0.0.5, 169.254.169.254
		"64:ff9b::808:808": false, // 8.8.8.8: legitimate on an IPv6-only host behind DNS64
		"2002:7f00:1::1":   true, "2002:c0a8:101::1": true, "2002:808:808::1": false,
		"fec0::1": true, "::7f00:1": true, "100::1": true, "2001:0:4136:e378:8000:63bf:3fff:fdd2": true,
		"64:ff9b:1::1": true,
	} {
		if got := blockedIP(netip.MustParseAddr(ip)); got != want {
			t.Errorf("blockedIP(%s) = %v, want %v", ip, got, want)
		}
	}
}
