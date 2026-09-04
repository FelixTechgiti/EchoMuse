package sendspin

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// Finding a Sendspin server.
//
// Discovery runs both ways in this protocol: a server advertises
// `_sendspin-server._tcp` on 8927 and a client advertises `_sendspin._tcp` on
// 8928, so either side can start the connection. This device browses and
// dials — the client-advertising half is for servers that want to reach out
// to speakers they have already been paired with, and a device that only ever
// listens cannot be set up by someone who has just installed Music Assistant.
//
// Advertising as well would be strictly better and is not free: it needs a
// listener, an accept path, and the multi-server arbitration below to work in
// both directions. It is left out deliberately rather than forgotten.

// ServerServiceType is what a Sendspin server advertises.
const ServerServiceType = "_sendspin-server._tcp"

// ClientServiceType is what a Sendspin client advertises. Named here because
// it is part of the contract even though this device does not yet use it.
const ClientServiceType = "_sendspin._tcp"

// DefaultServerPort, from the specification. Only a fallback: the port comes
// from the SRV record, and a server on a non-standard port is ordinary.
const DefaultServerPort = 8927

// DefaultPath is the recommended WebSocket endpoint. The `path` TXT record is
// authoritative; this is what to assume when it is absent, and assuming it is
// better than refusing — a server that omits an optional record is not a
// broken server.
const DefaultPath = "/sendspin"

// Server is a discovered Sendspin server.
type Server struct {
	// Name is the friendly name from the TXT record. NOT authoritative:
	// server/hello carries the real one, and this is only what to show
	// before the handshake has happened.
	Name string
	Host string
	Port int
	Path string
}

// URL is the WebSocket endpoint to dial.
//
// Always `ws://`, never `wss://`, and that is the protocol's design rather
// than an oversight: encryption is at the APPLICATION layer, inside the
// WebSocket, so that the Noise handshake authenticates the peer rather than a
// certificate chain nobody can provision on a speaker. Reaching for `wss`
// here would add TLS underneath an already-encrypted channel and require a CA
// on every device.
func (s Server) URL() string {
	path := s.Path
	if path == "" {
		path = DefaultPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{
		Scheme: "ws",
		Host:   net.JoinHostPort(s.Host, fmt.Sprint(s.Port)),
		Path:   path,
	}
	return u.String()
}

// ServerFromEntry converts an mDNS entry, or reports why it cannot.
//
// Split out from the browse loop so the record handling is testable without a
// network: every field here is optional or absent on some implementation, and
// the interesting cases are the malformed ones.
func ServerFromEntry(instance string, addrsV4 []net.IP, addrsV6 []net.IP,
	port int, txt []string) (Server, bool) {
	host := ""
	if len(addrsV4) > 0 {
		host = addrsV4[0].String()
	} else if len(addrsV6) > 0 {
		host = addrsV6[0].String()
	}
	if host == "" {
		// An advertisement with no address is a record we cannot act on.
		// It happens: zeroconf publishes the PTR before the A record on
		// some stacks, and the entry arrives with a hostname and nothing
		// to dial.
		return Server{}, false
	}
	if port <= 0 || port > 65535 {
		port = DefaultServerPort
	}
	return Server{
		Name: txtValue(txt, "name", instance),
		Host: host,
		Port: port,
		Path: txtValue(txt, "path", DefaultPath),
	}, true
}

// txtValue reads a key from mDNS TXT records ("key=value"), falling back when
// the key is absent or empty. An empty value is treated as absent: a server
// publishing `path=` means it did not set one, not that the path is "".
func txtValue(txt []string, key, fallback string) string {
	prefix := key + "="
	for _, t := range txt {
		if v, ok := strings.CutPrefix(t, prefix); ok && v != "" {
			return v
		}
	}
	return fallback
}

// Discover browses for Sendspin servers for the length of the context or
// until one is found.
//
// ⚠ THE FIRST SERVER FOUND WINS, and the spec asks for more than that. A
// client holds ONE admitted connection and is meant to rank competing servers
// by the activity they offer — playback above pairing above nothing — and to
// send `client/goodbye` with reason `another_server` to the one it leaves.
// With one Music Assistant on a network the two behave identically, which is
// exactly why this needs writing down: the gap only appears in a house that
// runs two servers, and then it appears as a speaker that picks the wrong one
// and stays there.
func Discover(ctx context.Context, iface string) (Server, error) {
	entries := make(chan *zeroconf.ServiceEntry, 4)

	opts := []zeroconf.ClientOption{}
	if iface != "" {
		if nic, err := net.InterfaceByName(iface); err == nil {
			opts = append(opts, zeroconf.SelectIfaces([]net.Interface{*nic}))
		} else {
			log.Printf("[sendspin] mDNS: no %s, using the default interface: %v", iface, err)
		}
	}

	resolver, err := zeroconf.NewResolver(opts...)
	if err != nil {
		return Server{}, fmt.Errorf("sendspin: mDNS resolver: %w", err)
	}
	if err := resolver.Browse(ctx, ServerServiceType, "local.", entries); err != nil {
		return Server{}, fmt.Errorf("sendspin: mDNS browse: %w", err)
	}

	for {
		select {
		case entry, ok := <-entries:
			if !ok {
				return Server{}, fmt.Errorf("sendspin: no server found")
			}
			if entry == nil {
				continue
			}
			srv, ok := ServerFromEntry(entry.Instance, entry.AddrIPv4,
				entry.AddrIPv6, entry.Port, entry.Text)
			if !ok {
				log.Printf("[sendspin] mDNS: skipping %s — no address", entry.Instance)
				continue
			}
			// Reachability is checked before the handshake for the reason
			// the controller's own discovery checks it: an advertisement is
			// a claim, and a stale one costs a full connect timeout on the
			// audio path.
			if !reachable(srv.Host, srv.Port) {
				log.Printf("[sendspin] mDNS: %s:%d did not answer — skipping",
					srv.Host, srv.Port)
				continue
			}
			log.Printf("[sendspin] found %q at %s", srv.Name, srv.URL())
			return srv, nil
		case <-ctx.Done():
			return Server{}, ctx.Err()
		}
	}
}

func reachable(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)),
		500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
