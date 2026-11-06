package check

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/wisnuvb/ronda/internal/model"
)

// ---- a tiny DNS server, so DNS checks can be tested without the internet ----

const (
	tA, tNS, tMX, tTXT, tAAAA = 1, 2, 15, 16, 28
)

type fakeDNS struct {
	mu      sync.RWMutex // the server goroutine reads these while tests fill them in
	conn    *net.UDPConn
	answers map[string][]string // "name/TYPE" -> values
	rcode   map[string]int      // "name/TYPE" -> response code (3 = NXDOMAIN, 2 = SERVFAIL)
	drop    map[string]bool     // "name/TYPE" -> never answer
}

func startDNS(t *testing.T) (*fakeDNS, string) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDNS{conn: conn, answers: map[string][]string{}, rcode: map[string]int{}, drop: map[string]bool{}}
	t.Cleanup(func() { conn.Close() })
	go d.serve()
	return d, conn.LocalAddr().String()
}

func (d *fakeDNS) put(key string, vals ...string) { d.mu.Lock(); d.answers[key] = vals; d.mu.Unlock() }
func (d *fakeDNS) fail(key string, rcode int)     { d.mu.Lock(); d.rcode[key] = rcode; d.mu.Unlock() }
func (d *fakeDNS) silence(key string)             { d.mu.Lock(); d.drop[key] = true; d.mu.Unlock() }

func typeName(q uint16) string {
	return map[uint16]string{tA: "A", tNS: "NS", tMX: "MX", tTXT: "TXT", tAAAA: "AAAA"}[q]
}

func encodeName(name string) []byte {
	var b []byte
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func (d *fakeDNS) serve() {
	buf := make([]byte, 512)
	for {
		n, addr, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		q := append([]byte(nil), buf[:n]...)
		if n < 17 {
			continue
		}
		// question: labels, then QTYPE and QCLASS
		i := 12
		var labels []string
		for i < n && q[i] != 0 {
			l := int(q[i])
			labels = append(labels, string(q[i+1:i+1+l]))
			i += 1 + l
		}
		qend := i + 1 + 4
		qtype := binary.BigEndian.Uint16(q[i+1:])
		key := strings.Join(labels, ".") + "/" + typeName(qtype)
		d.mu.RLock()
		dropped, vals, rc := d.drop[key], d.answers[key], d.rcode[key]
		d.mu.RUnlock()
		if dropped {
			continue
		}
		resp := make([]byte, 12)
		copy(resp, q[:2])                                       // ID
		binary.BigEndian.PutUint16(resp[2:], 0x8180|uint16(rc)) // response, recursion desired + available, rcode
		binary.BigEndian.PutUint16(resp[4:], 1)                 // one question
		if rc == 0 {
			binary.BigEndian.PutUint16(resp[6:], uint16(len(vals)))
		}
		resp = append(resp, q[12:qend]...)
		if rc == 0 {
			for _, v := range vals {
				var rdata []byte
				switch qtype {
				case tA:
					rdata = net.ParseIP(v).To4()
				case tAAAA:
					rdata = net.ParseIP(v).To16()
				case tTXT:
					rdata = append([]byte{byte(len(v))}, v...)
				case tNS:
					rdata = encodeName(v)
				case tMX:
					rdata = append([]byte{0, 10}, encodeName(v)...)
				}
				rr := []byte{0xC0, 0x0C, 0, byte(qtype), 0, 1, 0, 0, 0, 60, byte(len(rdata) >> 8), byte(len(rdata))}
				resp = append(resp, rr...)
				resp = append(resp, rdata...)
			}
		}
		d.conn.WriteToUDP(resp, addr)
	}
}

func dnsSite(url, server string) model.Site {
	return model.Site{URL: url, Type: "dns", DNSType: "A", DNSServer: server, TimeoutSec: 3, AllowPrivate: true}
}

func TestDNSRecordTypes(t *testing.T) {
	d, addr := startDNS(t)
	d.put("ok.example/A", "192.0.2.8", "192.0.2.7")
	d.put("ok.example/AAAA", "2001:db8::1")
	d.put("ok.example/MX", "mail.ok.example.")
	d.put("ok.example/TXT", "v=spf1 -all")
	d.put("ok.example/NS", "ns1.ok.example.")

	for typ, want := range map[string]string{
		"A": "192.0.2.7, 192.0.2.8", "AAAA": "2001:db8::1", "MX": "10 mail.ok.example",
		"TXT": "v=spf1 -all", "NS": "ns1.ok.example",
	} {
		s := dnsSite("dns://ok.example", addr)
		s.DNSType = typ
		r := run(t, s)
		if !r.OK || r.Kind != model.KindOK || r.Answer != want || r.LatencyMs < 1 {
			t.Errorf("%s: %+v, want answer %q", typ, r, want)
		}
	}
}

func TestDNSExpectedAnswer(t *testing.T) {
	d, addr := startDNS(t)
	d.put("ok.example/A", "192.0.2.7")

	s := dnsSite("dns://ok.example", addr)
	s.Keyword = "192.0.2.7"
	if r := run(t, s); !r.OK {
		t.Fatalf("the expected answer is present: %+v", r)
	}
	s.Keyword = "203.0.113.9"
	r := run(t, s)
	if r.OK || r.Kind != model.KindKeyword || !strings.Contains(r.Hint, "192.0.2.7") || !strings.Contains(r.Hint, "203.0.113.9") {
		t.Fatalf("a record that points somewhere else must fail and show what it returned: %+v", r)
	}
}

func TestDNSFailureModes(t *testing.T) {
	d, addr := startDNS(t)
	d.fail("gone.example/A", 3)   // NXDOMAIN
	d.fail("broken.example/A", 2) // SERVFAIL
	d.silence("silent.example/A")

	r := run(t, dnsSite("dns://gone.example", addr))
	if r.OK || r.Kind != model.KindDNS || !strings.Contains(r.Hint, "NXDOMAIN") || !strings.Contains(r.Hint, "no A record for gone.example") {
		t.Fatalf("NXDOMAIN: %+v", r)
	}
	if r := run(t, dnsSite("dns://empty.example", addr)); r.OK || r.Kind != model.KindDNS || !strings.Contains(r.Hint, "no record of that type") {
		t.Fatalf("a name with no record of that type: %+v", r)
	}
	if r := run(t, dnsSite("dns://broken.example", addr)); r.OK || r.Kind != model.KindDNS {
		t.Fatalf("SERVFAIL: %+v", r)
	}

	s := dnsSite("dns://silent.example", addr)
	s.TimeoutSec = 1
	r = run(t, s)
	if r.OK || r.Kind != model.KindDNS || !strings.Contains(r.Hint, "did not answer") || !strings.Contains(r.Hint, addr) {
		t.Fatalf("a server that never answers must say which server: %+v", r)
	}
}

func TestDNSServerOnPrivateAddressIsBlockedByDefault(t *testing.T) {
	d, addr := startDNS(t)
	d.put("ok.example/A", "192.0.2.7")
	s := dnsSite("dns://ok.example", addr)
	s.AllowPrivate = false
	r := run(t, s)
	if r.OK || r.Kind != model.KindBlocked {
		t.Fatalf("the dashboard must not be usable to query internal DNS servers: %+v", r)
	}
}

// ---- TCP ----

func tcpSite(addr string) model.Site {
	return model.Site{URL: "tcp://" + addr, Type: "tcp", TimeoutSec: 3, AllowPrivate: true}
}

func TestTCPOpenAndClosedPorts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			c.Close()
		}
	}()

	r := run(t, tcpSite(ln.Addr().String()))
	if !r.OK || r.Kind != model.KindOK || r.LatencyMs < 1 || r.ConnectMs < 1 || r.Status != 0 {
		t.Fatalf("open port: %+v", r)
	}
	<-accepted

	addr := ln.Addr().String()
	ln.Close()
	r = run(t, tcpSite(addr))
	if r.OK || r.Kind != model.KindRefused || !strings.Contains(r.Hint, "service") || strings.Contains(r.Hint, "web server") {
		t.Fatalf("closed port must be 'refused' and not talk about web servers: %+v", r)
	}
}

func TestTCPPrivateBlockedAndDNSFailure(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	s := tcpSite(ln.Addr().String())
	s.AllowPrivate = false
	if r := run(t, s); r.OK || r.Kind != model.KindBlocked {
		t.Fatalf("the same guard as web checks must apply, or this is a port scanner: %+v", r)
	}
	if r := run(t, tcpSite("tidak-ada.invalid:25")); r.OK || r.Kind != model.KindDNS {
		t.Fatalf("an unresolvable host: %+v", r)
	}
}

func TestDispatchFollowsType(t *testing.T) {
	// A tcp site must never be fetched over HTTP, and an http site must never be dialled raw.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	r := New().Check(context.Background(), tcpSite(ln.Addr().String()))
	if !r.OK || r.Status != 0 {
		t.Fatalf("tcp: %+v", r)
	}
}
