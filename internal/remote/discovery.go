package remote

import (
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"time"
)

const discoveryQuery = "AEGIS?"

type Found struct {
	Name string
	Addr string // host:port
}

type announce struct {
	App  string
	Name string
	Port int
}

// Announce answers LAN discovery requests on DiscoveryPort (UDP).
func Announce(name string, port int) error {
	pc, err := net.ListenPacket("udp4", ":"+strconv.Itoa(DiscoveryPort))
	if err != nil {
		return err
	}
	defer pc.Close()
	reply, _ := json.Marshal(announce{App: "aegis", Name: name, Port: port})
	buf := make([]byte, 64)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		if string(buf[:n]) == discoveryQuery {
			pc.WriteTo(reply, addr)
		}
	}
}

// Discover broadcasts a query and collects the servers that answer within wait.
func Discover(wait time.Duration) ([]Found, error) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	targets := []string{"255.255.255.255", "127.0.0.1"}
	if ifs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
				ip := ipn.IP.To4()
				bc := make(net.IP, 4)
				for i := range ip {
					bc[i] = ip[i] | ^ipn.Mask[len(ipn.Mask)-4+i]
				}
				targets = append(targets, bc.String())
			}
		}
	}
	for _, t := range targets {
		if ua, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(t, strconv.Itoa(DiscoveryPort))); err == nil {
			pc.WriteTo([]byte(discoveryQuery), ua)
		}
	}
	seen := map[string]Found{}
	pc.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 512)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		var a announce
		if json.Unmarshal(buf[:n], &a) != nil || a.App != "aegis" {
			continue
		}
		host, _, _ := net.SplitHostPort(addr.String())
		hp := net.JoinHostPort(host, strconv.Itoa(a.Port))
		seen[hp] = Found{Name: a.Name, Addr: hp}
	}
	out := make([]Found, 0, len(seen))
	for _, f := range seen {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out, nil
}
