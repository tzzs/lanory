package scan

import (
	"context"
	"errors"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// icmpProber sends a single echo request per host.
//
// It prefers an unprivileged "udp4"/"udp6" datagram socket, which works without
// CAP_NET_RAW and is what an OpenWrt service that does not run as root needs.
// When that is unavailable it falls back to the raw socket, and if that also
// fails the prober reports itself unavailable so the collector can be marked
// unsupported instead of silently returning "nothing answered" for every host —
// a very dangerous failure mode, because it looks like an empty network.
// IANA protocol numbers, hardcoded because golang.org/x/net/internal/iana is an
// internal package that cannot be imported. These values are fixed by the IANA
// registry (1 = ICMP for IPv4, 58 = ICMPv6) and never change.
const (
	ipv4Protocol = 1
	ipv6Protocol = 58
)

type icmpProber struct {
	// A net.PacketConn carries its deadline as connection state, not per-call
	// state. Sharing one socket across concurrent probes means SetDeadline calls
	// trample each other and a goroutine can block far past its own timeout: a
	// 254-host /24 that should take seconds never completed.
	//
	// Serialising one socket fixes correctness but destroys throughput (hosts
	// that do not answer each cost a full timeout), so each probe opens its own
	// short-lived socket instead. Sockets are cheap, the run is bounded and
	// rate limited, and no state is shared between probes.
	v4Mode string // "udp4" or "ip4:icmp", whichever this host allows
	v6Mode string // "udp6" or "ip6:ipv6-icmp", or "" when IPv6 is unavailable

	available bool
	err       error
}

func newICMPProber() *icmpProber {
	p := &icmpProber{}
	// Probe support once by opening and closing a socket. Datagram ICMP first
	// because it needs no privileges; the raw form is the fallback.
	if c, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
		p.v4Mode = "udp4"
		_ = c.Close()
	} else if c, err2 := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err2 == nil {
		p.v4Mode = "ip4:icmp"
		_ = c.Close()
	}
	if c, err := icmp.ListenPacket("udp6", "::"); err == nil {
		p.v6Mode = "udp6"
		_ = c.Close()
	} else if c, err2 := icmp.ListenPacket("ip6:ipv6-icmp", "::"); err2 == nil {
		p.v6Mode = "ip6:ipv6-icmp"
		_ = c.Close()
	}
	p.available = p.v4Mode != "" || p.v6Mode != ""
	if !p.available {
		p.err = errors.New("scan: no ICMP socket available (needs an unprivileged datagram socket or CAP_NET_RAW)")
	}
	return p
}

// Available reports whether any ICMP socket could be opened.
func (p *icmpProber) Available() bool { return p.available }

// Err returns the reason the prober is unavailable.
func (p *icmpProber) Err() error { return p.err }

// Ping sends one echo request and waits for the matching reply.
func (p *icmpProber) Ping(ctx context.Context, host net.IP, timeout time.Duration) (bool, error) {
	if !p.available {
		return false, p.err
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	if v4 := host.To4(); v4 != nil {
		if p.v4Mode == "" {
			return false, nil
		}
		conn, err := icmp.ListenPacket(p.v4Mode, "0.0.0.0")
		if err != nil {
			return false, err
		}
		defer conn.Close()
		return pingOne(ctx, conn, v4, deadline, ipv4Protocol)
	}
	if p.v6Mode == "" {
		return false, nil
	}
	conn, err := icmp.ListenPacket(p.v6Mode, "::")
	if err != nil {
		return false, err
	}
	defer conn.Close()
	return pingOne(ctx, conn, host, deadline, ipv6Protocol)
}

// pingOne performs the request/reply exchange on one socket.
func pingOne(ctx context.Context, conn *icmp.PacketConn, dst net.IP, deadline time.Time, proto int) (bool, error) {
	// A unique id per probe reduces the chance of matching a stale reply.
	id := int(uint16(time.Now().UnixNano()) | 1)
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if proto == ipv6Protocol {
		typ = ipv6.ICMPTypeEchoRequest
	}
	msg := icmp.Message{
		Type: typ,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: 1, Data: []byte("lanory")},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return false, err
	}

	if err := conn.SetDeadline(deadline); err != nil {
		return false, err
	}
	if _, err := conn.WriteTo(wire, &net.UDPAddr{IP: dst}); err != nil {
		// A raw socket needs an *net.IPAddr; retry once with that form.
		if _, err2 := conn.WriteTo(wire, &net.IPAddr{IP: dst}); err2 != nil {
			return false, nil
		}
	}

	buf := make([]byte, 1500)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		// Do not trust the socket deadline alone: re-arm it with the time
		// remaining so a reply for a previous probe cannot extend this wait.
		if time.Now().After(deadline) {
			return false, nil
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return false, err
		}
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return false, nil
			}
			return false, err
		}
		// Make sure the reply came from the host we asked.
		if !peerMatches(peer, dst) {
			continue
		}
		reply, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		switch body := reply.Body.(type) {
		case *icmp.Echo:
			if body.ID == id {
				return true, nil
			}
		case *icmp.PacketTooBig, *icmp.DstUnreach, *icmp.TimeExceeded:
			// An ICMP error about our probe still proves the host or a router
			// on the path is alive and responding.
			return true, nil
		}
	}
}

// peerMatches reports whether an ICMP peer address is the target host.
func peerMatches(peer net.Addr, dst net.IP) bool {
	switch a := peer.(type) {
	case *net.UDPAddr:
		return a.IP.Equal(dst)
	case *net.IPAddr:
		return a.IP.Equal(dst)
	}
	return false
}

// Close releases any resources held by the prober.
//
// Sockets are per probe now, so there is nothing persistent to close; the method
// exists to keep the Prober contract and so a future pooled implementation can
// clean up.
func (p *icmpProber) Close() error { return nil }
