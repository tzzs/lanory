package scan

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestRealProberAgainstLoopback exercises the actual ICMP socket path, including
// the unprivileged datagram fallback, rather than only the fake prober.
//
// 127.0.0.1 always answers an echo request, so a false result here means the
// socket setup or the reply matching is broken. When no ICMP socket can be
// opened at all (locked-down CI, no CAP_NET_RAW and no ping_group_range) the test
// skips with an explicit reason instead of passing silently.
func TestRealProberAgainstLoopback(t *testing.T) {
	p := newICMPProber()
	defer p.Close()
	if !p.Available() {
		t.Skipf("no ICMP socket in this environment: %v", p.Err())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ok, err := p.Ping(ctx, net.ParseIP("127.0.0.1"), time.Second)
	if err != nil {
		t.Fatalf("Ping(127.0.0.1) returned an error: %v", err)
	}
	if !ok {
		t.Fatal("127.0.0.1 did not answer an echo request; the prober is broken")
	}
}
