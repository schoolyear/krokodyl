package main

import (
	"net"
	"sort"
)

// A host with Hyper-V / WSL / Docker / VPN adapters has several IPv4
// addresses, only some of which a peer on the real LAN can route to. Rather
// than guess which is "real", we advertise them all and let the sender try
// each — correctness comes from trying every candidate, not from perfect
// classification. Ordering only affects which is tried first (speed).

const maxAdvertisedAddrs = 8

// localUnicastIPs returns this host's up, non-loopback, non-link-local IPv4
// unicast addresses, ordered most-likely-reachable first.
func localUnicastIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var ips []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip4.String())
		}
	}

	sort.SliceStable(ips, func(i, j int) bool {
		return addrRank(ips[i]) < addrRank(ips[j])
	})
	// Prefix rank is a heuristic and gets the wrong answer on a multi-adapter
	// host: e.g. a phone hotspot hands the laptop 172.20.10.x while a host-only
	// virtual switch (AutoVirtNAT 10.x / Hyper-V 172.20.128.x) ranks higher by
	// prefix. The address the OS uses to reach the outside world is the one the
	// peer on that same link can actually reach, so it goes first.
	if primary := primaryOutboundIP(); primary != "" {
		ips = moveToFront(ips, primary)
	}
	if len(ips) > maxAdvertisedAddrs {
		ips = ips[:maxAdvertisedAddrs]
	}
	return ips
}

// primaryOutboundIP returns the source IPv4 the OS would use to reach an
// external host — the address bound to the default-route interface. A UDP
// "connection" sends no packets; it only triggers a routing-table lookup and a
// local-address bind, so this works offline and leaks nothing. It is the IP a
// peer on the shared Wi-Fi or a joined phone hotspot can actually reach, and it
// sidesteps host-only virtual adapters that confuse prefix ranking. "" if it
// can't be determined (caller falls back to the ranked list).
func primaryOutboundIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	ip4 := ua.IP.To4()
	if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
		return ""
	}
	return ip4.String()
}

// moveToFront returns ips with target first (added if absent), de-duplicated,
// preserving the order of the rest.
func moveToFront(ips []string, target string) []string {
	out := make([]string, 0, len(ips)+1)
	out = append(out, target)
	for _, ip := range ips {
		if ip != target {
			out = append(out, ip)
		}
	}
	return out
}

// addrRank orders candidates: ordinary home/office LANs first, the ranges
// favored by virtual switches (Hyper-V/Docker live in 172.16/12) last. Lower
// is tried earlier.
func addrRank(ip string) int {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return 9
	}
	switch {
	case parsed[0] == 192 && parsed[1] == 168:
		return 0
	case parsed[0] == 10:
		return 1
	case parsed[0] == 172 && parsed[1] >= 16 && parsed[1] <= 31:
		return 3 // 172.16/12 — common for Hyper-V/Docker virtual switches
	default:
		return 2
	}
}

// orderedCandidates merges the peer's advertised addresses with the address
// the announcement actually arrived from, de-duplicated and ordered so the
// most-likely-reachable is dialed first. The packet source is appended last
// as a fallback (it may be a virtual adapter on a multi-homed sender).
func orderedCandidates(advertised []string, packetSource string) []string {
	ranked := append([]string(nil), advertised...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return addrRank(ranked[i]) < addrRank(ranked[j])
	})
	if packetSource != "" {
		ranked = append(ranked, packetSource)
	}

	seen := make(map[string]bool, len(ranked))
	// In-place filter: out shares ranked's backing array; safe because the
	// write index never passes the read index.
	out := ranked[:0]
	for _, ip := range ranked {
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out
}
