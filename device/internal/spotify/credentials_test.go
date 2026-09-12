package spotify

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The lines are copied VERBATIM off a device (2026-09-12) rather than written
// from the librespot source, because what matters is what it actually prints.
const (
	refusedSpirc = `[2026-09-12T12:27:02Z ERROR librespot] could not initialize spirc: ` +
		`Invalid state { Login request was denied: INVALID_CREDENTIALS }`
	macMismatch = `[2026-09-12T12:27:35Z WARN  librespot_discovery::server] ` +
		`Login error for user "l2cjtpvw967oi5olkpuon54mu": MAC mismatch`
)

func TestARefusedStoredCredentialIsRecognised(t *testing.T) {
	if !credentialRejection(refusedSpirc) {
		t.Fatal("the line that crash-looped a device for an hour did not match")
	}
}

func TestMacMismatchIsNotAStoredCredentialRejection(t *testing.T) {
	// This is the REPAIR path failing, not the stored blob being refused: a
	// phone's sign-in raced a restart, so its blob was decrypted with a key
	// pair that had just changed. Deleting our credential because somebody
	// else's did not decrypt would throw away a working authorisation.
	if credentialRejection(macMismatch) {
		t.Fatal("a client-side MAC mismatch must not delete the stored credential")
	}
}

func TestOrdinaryLibrespotChatterIsNotARejection(t *testing.T) {
	for _, line := range []string{
		`[2026-09-12T12:20:00Z INFO  librespot] librespot 0.7.1`,
		`[2026-09-12T12:20:01Z INFO  librespot_core::session] Connecting to AP`,
		`[2026-09-12T12:20:02Z INFO  librespot_playback] Using pipe sink`,
		`[2026-09-12T12:20:03Z WARN  librespot_core] Connection reset by peer`,
		``,
	} {
		if credentialRejection(line) {
			t.Fatalf("ordinary line treated as a credential rejection: %q", line)
		}
	}
}

func TestClearingRemovesTheBlobAndSaysSoOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, credentialsFile)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !clearCredentials(dir) {
		t.Fatal("clearing reported nothing to remove when the blob was there")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the blob survived: %v", err)
	}
	// The rejection repeats until the next login arrives. A warning per
	// restart about a file that is already gone is the noise this ends.
	if clearCredentials(dir) {
		t.Fatal("a second pass reported a removal that did not happen")
	}
}

func TestClearingAnAbsentCacheIsSafe(t *testing.T) {
	if clearCredentials(filepath.Join(t.TempDir(), "never", "created")) {
		t.Fatal("reported a removal from a directory that does not exist")
	}
}

func TestTheSupervisorDeletesARefusedCredentialBetweenRestarts(t *testing.T) {
	// The whole point, end to end: librespot refuses, exits, and the blob is
	// gone before it is started again — so the next run comes up in
	// discovery instead of reading the same dead credential for ever.
	dir := t.TempDir()
	path := filepath.Join(dir, credentialsFile)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := fakeLibrespot(t, "echo '"+refusedSpirc+"' >&2\nexit 1\n")
	c := New(Options{Binary: bin, CacheDir: dir}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the refused credential was still there — librespot would read it " +
		"again and exit again, which is the crash loop this fixes")
}

func TestAHealthyRunKeepsItsCredential(t *testing.T) {
	// The opposite failure, and the expensive one: deleting a working
	// credential signs the speaker out of an account it was correctly
	// authorised for, and nothing about an ordinary exit says to.
	dir := t.TempDir()
	path := filepath.Join(dir, credentialsFile)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := fakeLibrespot(t, "echo '[INFO  librespot] connected' >&2\nsleep 2\n")
	c := New(Options{Binary: bin, CacheDir: dir}, &fakeSink{}, &fakePlane{})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a healthy session lost its credential: %v", err)
	}
}
