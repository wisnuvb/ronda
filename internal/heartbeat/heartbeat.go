// Package heartbeat is the dead-man's switch: ronda pings an outside URL (for example a
// healthchecks.io check) on a schedule, and that service raises the alarm when the pings stop.
// It is what tells you the monitor itself has died, which nothing inside it ever could.
package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type Pinger struct {
	URL      string
	Interval time.Duration
	// Healthy gates each ping: a monitor that is alive but no longer checking anything must go silent.
	Healthy func() bool
	Client  *http.Client

	failing bool
}

func New(url string, every time.Duration, healthy func() bool) *Pinger {
	return &Pinger{URL: url, Interval: every, Healthy: healthy, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Run pings immediately and then every Interval until ctx ends.
func (p *Pinger) Run(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		p.once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Pinger) once(ctx context.Context) {
	if p.Healthy != nil && !p.Healthy() {
		p.report(errors.New("the monitor is stuck; the heartbeat is deliberately not sent"))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		p.report(errors.New("invalid heartbeat URL"))
		return
	}
	req.Header.Set("User-Agent", "ronda-heartbeat/1")
	resp, err := p.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		// Go's errors quote the URL, and a heartbeat URL is a secret (it often holds the check's key).
		p.report(errors.New("could not reach the heartbeat service"))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		p.report(fmt.Errorf("the heartbeat service answered HTTP %d", resp.StatusCode))
		return
	}
	p.report(nil)
}

// report logs the first failure and the recovery, not every missed ping.
func (p *Pinger) report(err error) {
	switch {
	case err != nil && !p.failing:
		p.failing = true
		log.Printf("heartbeat: %v", err)
	case err == nil && p.failing:
		p.failing = false
		log.Printf("heartbeat: recovered")
	}
}
