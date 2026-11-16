package broadlink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"
)

// DefaultDiscoverWait is how long Discover listens for answers.
const DefaultDiscoverWait = 3 * time.Second

// maxScanHosts bounds the unicast sweep of one network (a /22).
const maxScanHosts = 1024

// Found is a device that answered the discovery hello.
type Found struct {
	Host      string
	Info      Info
	Supported bool // an IR remote this bridge can drive
}

// DiscoverOptions tunes Discover.
type DiscoverOptions struct {
	// Networks are CIDRs that are swept host by host with a unicast hello.
	// Needed when broadcasts do not reach the LAN (a container with its own
	// network namespace). Empty: only the broadcast addresses are used.
	Networks []string
	Wait     time.Duration // zero means DefaultDiscoverWait
	Port     int           // zero means DefaultPort
}

// Discover finds Broadlink devices. It sends the hello packet to the
// broadcast addresses of the local interfaces and to every host of the given
// networks, then collects the answers for opts.Wait.
func Discover(ctx context.Context, opts DiscoverOptions) ([]Found, error) {
	port := opts.Port
	if port == 0 {
		port = DefaultPort
	}
	wait := opts.Wait
	if wait == 0 {
		wait = DefaultDiscoverWait
	}
	targets, err := discoverTargets(opts.Networks)
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if uc, ok := conn.(*net.UDPConn); ok {
		_ = enableBroadcast(uc)
	}

	pkt := helloPacket()
	for _, t := range targets {
		// Errors (no route, broadcast not permitted) are per target and
		// expected; the other targets still work.
		_, _ = conn.WriteTo(pkt, &net.UDPAddr{IP: t, Port: port})
	}

	deadline := time.Now().Add(wait)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	found := map[string]Found{}
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		_ = conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			return nil, err
		}
		if n < 0x40 || !validChecksum(buf[:n]) {
			continue
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		devtype, _, _, info := parseHello(buf[:n])
		_, supported := specs[devtype]
		host := ua.IP.String()
		found[host] = Found{Host: host, Info: info, Supported: supported}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]Found, 0, len(found))
	for _, f := range found {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := netip.ParseAddr(out[i].Host)
		b, _ := netip.ParseAddr(out[j].Host)
		return a.Less(b)
	})
	return out, nil
}

// discoverTargets lists the addresses to probe: the limited broadcast, the
// directed broadcast of every local IPv4 network and all hosts of networks.
func discoverTargets(networks []string) ([]net.IP, error) {
	seen := map[string]bool{}
	var out []net.IP
	add := func(ip net.IP) {
		if k := ip.String(); !seen[k] {
			seen[k] = true
			out = append(out, ip)
		}
	}
	add(net.IPv4bcast)

	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
				add(directedBroadcast(ipn))
			}
		}
	}

	for _, cidr := range networks {
		p, err := netip.ParsePrefix(cidr)
		if err != nil || !p.Addr().Is4() {
			return nil, fmt.Errorf("discovery network %q: want an IPv4 CIDR like 10.0.10.0/24", cidr)
		}
		p = p.Masked()
		if hosts := 1 << (32 - p.Bits()); hosts > maxScanHosts {
			return nil, fmt.Errorf("discovery network %q is too large (max %d addresses)", cidr, maxScanHosts)
		}
		for a := p.Addr(); p.Contains(a); a = a.Next() {
			b := a.As4()
			add(net.IPv4(b[0], b[1], b[2], b[3]))
		}
	}
	return out, nil
}

func directedBroadcast(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := net.IP(n.Mask).To4()
	if ip == nil || mask == nil {
		return net.IPv4bcast
	}
	b := make(net.IP, 4)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return b
}
