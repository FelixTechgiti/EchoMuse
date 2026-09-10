package discovery

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// CachePath remembers the controller this device last registered with. It
// sits beside the TLS credentials and state.json in /data/local/etc, which
// OTA slot flips do not touch — the flip only ever replaces
// /data/local/bin/server.
//
// # Why a fresh process must not have to rediscover
//
// `lastServer` was in memory only, so it existed for the life of ONE process.
// A reboot repopulates it the slow way and nobody notices; an in-place restart
// — every OTA, every supervisor restart — starts with nothing and has no path
// to the controller except mDNS.
//
// That is the whole of "the Echo disappears after every update and comes back
// after a power cycle". Measured on a device 2026-09-10, first boot of
// v2.24.0-fx.1, from the persistent log this pairs with:
//
//	16:34:39  firmware: v2.24.0-fx.1 starting
//	16:35:58  firmware: no controller for 1m15s — 4 browse rounds, wlan0=192.168.178.140
//	16:40:18  firmware: no controller for 5m35s — 8 browse rounds, wlan0=192.168.178.140
//	16:45:00  boot slot=server_a        ← a REBOOT, and it connected in seconds
//
// The binary was fine, the network was fine, the address was right there in
// the log line. Only the *discovery* was broken, and the same restart-then-
// reboot pair appears six times in that one day's log.
//
// So this does not fix mDNS — it removes mDNS from the path a restart depends
// on. The cached endpoint is a HINT and nothing more: `Run` proves it with a
// 3s TCP probe before using it and falls back to a browse when that fails, so
// a controller that has moved costs three seconds and is then found the old
// way. Why mDNS stops answering after an in-place restart is filed separately
// and is no longer something a user has to live through while it is open.
//
// It adds no exposure the LAN did not already have: an attacker who can
// answer at this address could equally answer an mDNS browse, more easily and
// without waiting for a restart, and the link is TLS-verified against a fixed
// SAN wherever credentials are installed.
const CachePath = "/data/local/etc/revoice/controller.json"

type cachedServer struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLSPort int    `json:"tlsPort"`
}

// LoadEndpoint returns the remembered controller, or nil when there is none
// to remember, the file is unreadable, or what it holds is not usable.
//
// Absence is the ordinary case — first boot of this firmware, a device that
// has never registered — so it is not an error and is not logged as one.
func LoadEndpoint(path string) *ServerInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c cachedServer
	if err := json.Unmarshal(data, &c); err != nil {
		log.Printf("[discovery] %s unreadable, ignoring: %v", path, err)
		return nil
	}
	if c.Host == "" || c.Port <= 0 || c.Port > 65535 {
		return nil
	}
	return &ServerInfo{
		Host:    c.Host,
		Port:    c.Port,
		Addr:    fmt.Sprintf("%s:%d", c.Host, c.Port),
		TLSPort: c.TLSPort,
	}
}

// SaveEndpoint records the controller a registration just succeeded against.
//
// **Only when it has changed.** The call site is every successful connect, and
// this fleet reconnects often — a device that drops and redials all evening
// would otherwise spend a flash write per reconnect to store bytes already
// there, on eMMC that cannot be replaced. Same rule as WriteConsolePassword,
// for the same reason.
//
// Written to a temp file and renamed, so a power cut mid-write cannot leave a
// truncated record: a half-written one does not parse, which reads as "no
// cached controller" and costs a browse rather than a wrong address.
func SaveEndpoint(path string, info *ServerInfo) {
	if info == nil || info.Host == "" || info.Port <= 0 {
		return
	}
	next := cachedServer{Host: info.Host, Port: info.Port, TLSPort: info.TLSPort}
	if cur := LoadEndpoint(path); cur != nil &&
		cur.Host == next.Host && cur.Port == next.Port && cur.TLSPort == next.TLSPort {
		return
	}
	data, err := json.Marshal(next)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[discovery] cache mkdir failed: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[discovery] cache write failed: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[discovery] cache rename failed: %v", err)
		return
	}
	log.Printf("[discovery] remembered controller %s (tls_port=%d)", info.Addr, info.TLSPort)
}
