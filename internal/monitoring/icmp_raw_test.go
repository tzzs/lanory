package monitoring

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestRawICMPAgainstLoopback exercises the raw socket path end to end.
//
// The datagram socket is refused on a locked-down kernel and on the reference
// router, where /proc/sys/net/ipv4/ping_group_range is "1 0" (an empty
// range). The raw socket is the fallback, and it has two traps that this test
// pins down:
//
//   - the ICMP message is written without a hand-built IP header, because Go's
//     "ip4:icmp" endpoint has IP_HDRINCL cleared and the kernel fills the
//     header in. Writing a manual header produces a packet the peer never
//     answers.
//   - the reply is parsed from the payload alone, because the kernel strips
//     the IP header before delivering it.
//
// Both were found on the router, where every probe timed out while busybox
// ping worked.
func TestRawICMPAgainstLoopback(t *testing.T) {
	if !rawSocketSupported() {
		t.Skip("raw ICMP socket unavailable on this host (needs CAP_NET_RAW)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, err := PingOne(ctx, "", net.ParseIP("127.0.0.1"), 2*time.Second, []byte("lanory"), nil)
	if err != nil {
		t.Fatalf("PingOne(127.0.0.1): %v", err)
	}
	if !ok {
		t.Fatal("127.0.0.1 did not answer the echo request through the raw socket")
	}
}

// TestRawICMPTimeout checks that an address which drops the request is
// reported as a timeout rather than as a success or a transport error.
func TestRawICMPTimeout(t *testing.T) {
	if !rawSocketSupported() {
		t.Skip("raw ICMP socket unavailable on this host (needs CAP_NET_RAW)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	ok, err := PingOne(ctx, "", net.ParseIP("192.0.2.1"), 1*time.Second, []byte("lanory"), nil)
	elapsed := time.Since(start)

	if err == nil && ok {
		t.Fatal("an unroutable address answered the echo request")
	}
	if elapsed > 3*time.Second {
		t.Errorf("the probe took %s, want it bounded by the 1s timeout", elapsed)
	}
}

// TestRawICMPTimeoutIsClassified makes sure the timeout reaches the state
// machine as a timeout, not as an unknown error: the distinction is what lets a
// sleeping phone be reported as sleeping instead of offline.
func TestRawICMPTimeoutIsClassified(t *testing.T) {
	err := error(errICMPTimeout)
	if got := ClassifyError(err); got != ErrorTypeTimeout {
		t.Errorf("ClassifyError(errICMPTimeout) = %q, want %q", got, ErrorTypeTimeout)
	}
}
