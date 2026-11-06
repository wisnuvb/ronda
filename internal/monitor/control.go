package monitor

import (
	"context"
	"net"
	"sync"
	"time"
)

// Control answers "does the monitor itself have internet?" by dialling a few
// well-known addresses. If at least one answers, the network is fine. The answer
// is cached briefly so a mass failure costs one probe, not one per site.
type Control struct {
	Addrs   []string
	TTL     time.Duration                                // default 15s
	Timeout time.Duration                                // default 3s
	Dial    func(ctx context.Context, addr string) error // replaceable in tests

	mu sync.Mutex
	at time.Time
	up bool
}

func (c *Control) dial(ctx context.Context, addr string) error {
	if c.Dial != nil {
		return c.Dial(ctx, addr)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// Up is true when at least one control address is reachable. With no addresses
// configured (or a nil Control) it is always true.
func (c *Control) Up(ctx context.Context) bool {
	if c == nil || len(c.Addrs) == 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	ttl, timeout := c.TTL, c.Timeout
	if ttl == 0 {
		ttl = 15 * time.Second
	}
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	if !c.at.IsZero() && time.Since(c.at) < ttl {
		return c.up
	}

	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res := make(chan bool, len(c.Addrs))
	for _, a := range c.Addrs {
		go func() { res <- c.dial(pctx, a) == nil }()
	}
	up := false
	for range c.Addrs {
		if <-res {
			up = true
			break
		}
	}
	if ctx.Err() != nil {
		return true // shutting down: do not cache a verdict made under cancellation
	}
	c.at, c.up = time.Now(), up
	return up
}
