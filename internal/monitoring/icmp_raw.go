package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// rawICMP is a raw-socket ICMP endpoint.
//
// Why this exists: the unprivileged datagram socket ("udp4") needs the kernel's
// ping_group_range to cover the process group. iStoreOS 25.12.5 ships with
// /proc/sys/net/ipv4/ping_group_range set to "1 0", an empty range, so the
// datagram socket fails with EPERM even for root with every capability.
//
// The privileged form ("ip4:icmp") is a SOCK_RAW socket, and a raw IPv4 socket
// expects the complete IP packet - header included - to be written. Writing
// only the ICMP message produces a packet the peer never answers, which is why
// the first raw-socket implementation sent packets and then timed out.
//
// A raw socket needs CAP_NET_RAW. The OpenWrt package runs the service as root
// with the default capability set; the procd script can add setcap when the
// installation is tightened down. When the raw socket is not permitted either,
// the caller falls back to the TCP probe and reports the degradation.
type rawICMP struct {
	conn *icmp.PacketConn
}

// newRawICMP opens a raw ICMPv4 socket.
//
// A non-empty local address is important: binding to 0.0.0.0 receives ICMP
// traffic for every interface, which on a router is a lot of noise, and lets the
// kernel pick the source address per route. Binding to the address of the LAN
// interface keeps the traffic relevant and makes the reply come back to the
// socket that asked for it.
func newRawICMP(localAddr string) (*rawICMP, error) {
	c, err := icmp.ListenPacket("ip4:icmp", bindAddr(localAddr))
	if err != nil {
		return nil, err
	}
	return &rawICMP{conn: c}, nil
}

// bindAddr returns the address to bind a raw socket to.
func bindAddr(localAddr string) string {
	if localAddr == "" {
		return "0.0.0.0"
	}
	return localAddr
}

// Close releases the socket.
func (r *rawICMP) Close() error {
	if r == nil || r.conn == nil {
		return nil
	}
	return r.conn.Close()
}

// PingOne opens a short-lived raw socket, sends one echo request and closes it.
//
// A per-probe socket is used rather than a shared one because a raw socket
// carries connection state: its deadline and its receive queue belong to one
// exchange. Sharing it serialised every probe behind the slowest one, so a
// sweep of eighteen hosts took the sum of their timeouts instead of the
// longest. Opening a socket is cheap compared to the round trip.
func PingOne(ctx context.Context, localAddr string, dst net.IP, timeout time.Duration, payload []byte, sourceIP net.IP) (bool, error) {
	raw, err := newRawICMP(localAddr)
	if err != nil {
		return false, err
	}
	defer raw.Close()
	return raw.Ping(ctx, dst, timeout, payload, sourceIP)
}

// Ping sends one echo request and waits for the matching reply.
//
// sourceIP is accepted for symmetry with the other paths but is not used: the
// kernel picks the source address from the routing table, which is the right
// choice for a probe.
func (r *rawICMP) Ping(ctx context.Context, dst net.IP, timeout time.Duration, payload []byte, sourceIP net.IP) (bool, error) {
	if r == nil || r.conn == nil {
		return false, errors.New("icmp: socket is not open")
	}
	if dst.To4() == nil {
		return false, errors.New("icmp: target is not an IPv4 address")
	}

	// A unique id keeps a stale reply from a previous probe from matching.
	// The sequence is fixed at 1, which is what the reply must echo.
	id := int(uint16(time.Now().UnixNano()) | 1)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: 1, Data: payload},
	}
	body, err := msg.Marshal(nil)
	if err != nil {
		return false, err
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(timeout)
	}
	if err := r.conn.SetDeadline(deadline); err != nil {
		return false, err
	}
	defer func() { _ = r.conn.SetDeadline(time.Time{}) }()

	// The ICMP message is written as-is.
	//
	// Contrary to a C raw socket, Go's "ip4:icmp" endpoint on Linux is opened
	// with SOCK_RAW and IP_HDRINCL *cleared*, so the kernel fills in the IP
	// header itself. Writing a hand-built header on top produces a packet the
	// peer never answers: verified on the reference router, where the manual
	// header produced a timeout and the plain message produced a reply.
	if _, err := r.conn.WriteTo(body, &net.IPAddr{IP: dst}); err != nil {
		return false, err
	}

	buf := make([]byte, 1500)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, peer, err := r.conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				// No reply before the deadline. This is a timeout, not a
				// transport error, and the caller has to be able to tell the
				// difference: a timeout means the host is silent, an error
				// means the probe itself broke.
				return false, errICMPTimeout
			}
			return false, err
		}
		// A raw socket receives every ICMP packet on the host, so the reply is
		// filtered by source address and by the echo id.
		if !peerMatchesRaw(peer, dst) {
			continue
		}
		// The reply carries the ICMP message only: the kernel strips the IP
		// header before delivering it to the socket.
		reply, perr := icmp.ParseMessage(1, buf[:n])
		if perr != nil {
			continue
		}
		echo, isEcho := reply.Body.(*icmp.Echo)
		if !isEcho || echo.ID != id {
			continue
		}
		switch reply.Type {
		case ipv4.ICMPTypeEchoReply:
			return true, nil
		case ipv4.ICMPTypeDestinationUnreachable:
			return false, errors.New("icmp destination unreachable")
		default:
			continue
		}
	}
}

// peerMatchesRaw reports whether a raw socket peer is the target host.
func peerMatchesRaw(peer net.Addr, dst net.IP) bool {
	switch a := peer.(type) {
	case *net.IPAddr:
		return a.IP.Equal(dst)
	case *net.UDPAddr:
		return a.IP.Equal(dst)
	}
	return false
}

// rawSocketSupported reports whether a raw ICMP socket can be opened at all.
//
// It is used to decide between the raw and the datagram path without paying
// for a failed open on every probe.
func rawSocketSupported() bool {
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// errICMPTimeout reports that the peer did not answer before the deadline.
var errICMPTimeout = &icmpTimeoutError{}

type icmpTimeoutError struct{}

func (icmpTimeoutError) Error() string   { return "icmp: no reply within the timeout" }
func (icmpTimeoutError) Timeout() bool   { return true }
func (icmpTimeoutError) Temporary() bool { return true }

// errNoRawSocket explains that neither socket form is available.
var errNoRawSocket = fmt.Errorf("no ICMP socket is available: the datagram socket needs " +
	"net.ipv4.ping_group_range to cover this process, and the raw socket needs CAP_NET_RAW")
