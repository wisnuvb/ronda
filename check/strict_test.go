package check

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/model"
)

// A service that probes addresses chosen by strangers must stay shut even when a site says allow_private: that is
// the whole point of NewStrict, so each kind of check is tried with the flag set.
func TestStrictCheckerIgnoresAllowPrivate(t *testing.T) {
	var hits atomic.Int32
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer web.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	strict := NewStrict()
	for name, s := range map[string]model.Site{
		"http": {URL: web.URL, TimeoutSec: 3, AllowPrivate: true},
		"tcp":  {Type: "tcp", URL: "tcp://" + ln.Addr().String(), TimeoutSec: 3, AllowPrivate: true},
		"dns":  {Type: "dns", URL: "dns://example.com", DNSType: "A", DNSServer: ln.Addr().String(), TimeoutSec: 3, AllowPrivate: true},
	} {
		r := strict.Check(context.Background(), s)
		if r.OK || r.Kind != model.KindBlocked {
			t.Errorf("%s: want a blocked result even with allow_private, got %+v", name, r)
		}
	}

	if n := hits.Load(); n != 0 {
		t.Errorf("the strict checker reached the web server %d time(s)", n)
	}

	// The same site is reachable with the ordinary checker, so the test above proves the policy and not a broken setup.
	if r := New().Check(context.Background(), model.Site{URL: web.URL, TimeoutSec: 3, AllowPrivate: true}); !r.OK || hits.Load() != 1 {
		t.Errorf("control: New() with allow_private must reach loopback, got %+v (hits %d)", r, hits.Load())
	}
}

func TestGuardedClientRefusesInternalAddressesAndRedirects(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	if _, err := GuardedClient(2*time.Second).Post(srv.URL, "application/json", nil); err == nil || hit {
		t.Fatalf("a loopback webhook must be refused (hit=%v, err=%v)", hit, err)
	}

	// A redirect is returned, not followed: a public webhook must not be able to bounce the call inward.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redir.Close()
	c := GuardedClient(2 * time.Second)
	// Loopback is refused before any redirect could matter; check the redirect policy itself directly.
	if err := c.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("redirects must not be followed, got %v", err)
	}
}
