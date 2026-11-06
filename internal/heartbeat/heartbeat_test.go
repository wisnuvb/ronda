package heartbeat

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// logBuf is a bytes.Buffer that the logger and the test can touch at the same time.
type logBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuf) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}
func (l *logBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

func capture(t *testing.T) *logBuf {
	l := &logBuf{}
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return l
}

func waitLog(t *testing.T, l *logBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(l.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log never contained %q: %q", want, l.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPingsOnScheduleAndStops(t *testing.T) {
	var hits atomic.Int32
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ua.Store(r.Header.Get("User-Agent"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p := New(srv.URL+"/ping/abc", 30*time.Millisecond, func() bool { return true })
	go func() { p.Run(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for hits.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() < 3 {
		t.Fatalf("only %d pings", hits.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("did not stop")
	}
	if ua.Load() != "ronda-heartbeat/1" {
		t.Fatalf("user agent %v", ua.Load())
	}
}

func TestUnhealthyMonitorGoesSilent(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	logs := capture(t)

	var healthy atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(srv.URL, 20*time.Millisecond, healthy.Load).Run(ctx)

	time.Sleep(150 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("a stalled monitor sent %d pings; the alarm must be allowed to fire", hits.Load())
	}
	healthy.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Fatal("pings must resume when checks run again")
	}
	waitLog(t, logs, "recovered")
	if !strings.Contains(logs.String(), "deliberately not sent") {
		t.Fatalf("the stall must have been logged: %q", logs.String())
	}
}

func TestSecretURLNeverReachesTheLog(t *testing.T) {
	logs := capture(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/ping/SECRET-KEY-123"
	srv.Close() // connection refused: Go's error text would quote the URL

	p := New(url, time.Hour, nil)
	p.once(context.Background())

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	q := New(bad.URL+"/ping/SECRET-KEY-123", time.Hour, nil)
	q.once(context.Background())

	out := logs.String()
	if out == "" {
		t.Fatal("failures must be logged")
	}
	if strings.Contains(out, "SECRET") {
		t.Fatalf("the secret leaked into the log: %q", out)
	}
	if !strings.Contains(out, "HTTP 500") {
		t.Fatalf("an HTTP error should say its status: %q", out)
	}
}

func TestLogsOnlyTransitions(t *testing.T) {
	logs := capture(t)
	p := New("http://127.0.0.1:1", time.Hour, func() bool { return false })
	for i := 0; i < 5; i++ {
		p.once(context.Background())
	}
	if n := strings.Count(logs.String(), "heartbeat:"); n != 1 {
		t.Fatalf("logged %d lines for 5 identical failures, want 1:\n%s", n, logs.String())
	}
}
