package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func cachePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "etc", "controller.json")
}

// The whole point: a NEW PROCESS finds the controller it was last registered
// with, without a browse. In memory the field survived a reconnect and nothing
// else, and the restart that needs it most — every OTA — began with nothing.
func TestASavedEndpointSurvivesTheProcess(t *testing.T) {
	p := cachePath(t)
	SaveEndpoint(p, &ServerInfo{
		Host: "192.168.178.20", Port: 8767, Addr: "192.168.178.20:8767", TLSPort: 8770,
	})

	got := LoadEndpoint(p)
	if got == nil {
		t.Fatal("nothing was remembered")
	}
	if got.Host != "192.168.178.20" || got.Port != 8767 {
		t.Fatalf("wrong endpoint: %+v", got)
	}
	// Addr is rebuilt rather than stored, and the fast path dials it.
	if got.Addr != "192.168.178.20:8767" {
		t.Fatalf("Addr = %q, want host:port — the probe dials this", got.Addr)
	}
	// The TLS port has to come back too: without it a device with a CA
	// installed re-browses to find one, which is the mDNS round trip this
	// exists to avoid.
	if got.TLSPort != 8770 {
		t.Fatalf("TLSPort = %d, want 8770", got.TLSPort)
	}
}

// Called on every successful connect, on a fleet that reconnects often, on
// eMMC that cannot be replaced. An unchanged endpoint must cost nothing.
func TestUnchangedEndpointIsNotRewritten(t *testing.T) {
	p := cachePath(t)
	info := &ServerInfo{Host: "10.0.0.5", Port: 8767, Addr: "10.0.0.5:8767", TLSPort: 8770}

	SaveEndpoint(p, info)
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		SaveEndpoint(p, info)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if before.ModTime() != after.ModTime() {
		t.Fatal("an unchanged endpoint was rewritten — that is a flash write per reconnect")
	}
}

// A controller that moves must be followed, or the cache becomes a way to
// keep dialling an address that stopped being right.
func TestAMovedControllerIsRecorded(t *testing.T) {
	p := cachePath(t)
	SaveEndpoint(p, &ServerInfo{Host: "10.0.0.5", Port: 8767, Addr: "10.0.0.5:8767"})
	SaveEndpoint(p, &ServerInfo{Host: "10.0.0.9", Port: 8767, Addr: "10.0.0.9:8767", TLSPort: 8770})

	got := LoadEndpoint(p)
	if got == nil || got.Host != "10.0.0.9" {
		t.Fatalf("the move was not recorded: %+v", got)
	}
	if got.TLSPort != 8770 {
		t.Fatal("a controller that gained a TLS listener was not updated")
	}
}

// Every absence is ordinary and must read as "browse as usual", never as an
// endpoint. A device that has never registered, a truncated write, a file
// somebody emptied — all the same answer.
func TestUnusableRecordsReadAsNoEndpoint(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"missing.json": "",
		"empty.json":   "",
		"garbage.json": "{not json",
		"nohost.json":  `{"host":"","port":8767}`,
		"noport.json":  `{"host":"10.0.0.5","port":0}`,
		"badport.json": `{"host":"10.0.0.5","port":70000}`,
		"halfway.json": `{"host":"10.0.0.5","po`,
	} {
		p := filepath.Join(dir, name)
		if name != "missing.json" {
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := LoadEndpoint(p); got != nil {
			t.Fatalf("%s produced an endpoint %+v — it must read as none", name, got)
		}
	}
}

// Saving must never be able to fail the caller: it runs on the connect path,
// right after a registration that succeeded.
func TestSavingIsSilentAndSafe(t *testing.T) {
	SaveEndpoint("/proc/definitely/not/writable/controller.json",
		&ServerInfo{Host: "10.0.0.5", Port: 8767})
	SaveEndpoint(cachePath(t), nil)
	SaveEndpoint(cachePath(t), &ServerInfo{Host: "", Port: 0})
	if got := LoadEndpoint(cachePath(t)); got != nil {
		t.Fatalf("an unusable ServerInfo was stored: %+v", got)
	}
}
