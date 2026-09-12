package airplay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The strings are real shapes from shairport-sync's get_version_string()
// (common.c), which appends one hyphenated token per compiled-in feature. The
// point of testing against them rather than against a parser of our own design
// is that a build gaining a feature must not stop being recognised.
func TestParseVersionReadsTheFeatureTokens(t *testing.T) {
	cases := []struct {
		name   string
		out    string
		ap2    bool
		shm    int
		verSub string
	}{
		{
			name:   "the classic build we ship today",
			out:    "4.3.7-mbedTLS-stdout-metadata-sysconfdir:/etc\n",
			ap2:    false,
			shm:    0,
			verSub: "4.3.7",
		},
		{
			name:   "an AirPlay 2 build",
			out:    "4.3.7-AirPlay2-smi10-mbedTLS-stdout-metadata-sysconfdir:/etc\n",
			ap2:    true,
			shm:    10,
			verSub: "AirPlay2",
		},
		{
			name: "a git-described AirPlay 2 build",
			out:  "4.3.7-27-gabcdef0-AirPlay2-smi11-alac-stdout-metadata\n",
			ap2:  true,
			shm:  11,
		},
		{
			// -V has printed more than one line in some builds, and a
			// diagnostic that breaks on a newline is worse than none.
			name: "extra lines around it",
			out:  "\n  4.3.7-AirPlay2-smi10-stdout  \nsome other note\n",
			ap2:  true,
			shm:  10,
		},
		{
			// The token has to be bounded by hyphens or ends. A build whose
			// name merely CONTAINED the word must not read as AirPlay 2 —
			// that would start a clock daemon for a binary that cannot use it.
			name: "a lookalike token is not a match",
			out:  "4.3.7-NotAirPlay2ish-stdout\n",
			ap2:  false,
			shm:  0,
		},
		{
			name: "empty output",
			out:  "",
			ap2:  false,
			shm:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ParseVersion(tc.out)
			if f.AirPlay2 != tc.ap2 {
				t.Errorf("AirPlay2 = %v, want %v (from %q)", f.AirPlay2, tc.ap2, tc.out)
			}
			if f.ShmVersion != tc.shm {
				t.Errorf("ShmVersion = %d, want %d", f.ShmVersion, tc.shm)
			}
			if tc.verSub != "" && !strings.Contains(f.Version, tc.verSub) {
				t.Errorf("Version = %q, want it to contain %q", f.Version, tc.verSub)
			}
		})
	}
}

// The binary is the authority on what it is, so a failure to ask must not be
// read as an answer. Detect returns an error rather than a zero Flavour that
// says "classic".
func TestDetectFlavourUsesOutputEvenOnANonZeroExit(t *testing.T) {
	f, err := DetectFlavour("/nonexistent", func(string, ...string) ([]byte, error) {
		return []byte("4.3.7-AirPlay2-smi10-stdout\n"), errors.New("exit status 1")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.AirPlay2 || f.ShmVersion != 10 {
		t.Errorf("got %+v, want AirPlay2 with shm 10", f)
	}
}

func TestDetectFlavourFailsWhenItCannotRunAtAll(t *testing.T) {
	_, err := DetectFlavour("/nonexistent", func(string, ...string) ([]byte, error) {
		return nil, errors.New("no such file")
	})
	if err == nil {
		t.Fatal("want an error when the binary cannot be run and said nothing")
	}
}

// The rule this pins is the one that would otherwise be got wrong under
// pressure: an AirPlay 2 binary with no clock daemon still serves CLASSIC
// AirPlay, so a missing nqptp must not stop the receiver.
func TestPlanNqptp(t *testing.T) {
	classic := Flavour{Version: "4.3.7-stdout"}
	ap2 := Flavour{Version: "4.3.7-AirPlay2-smi10", AirPlay2: true, ShmVersion: 10}

	if p := PlanNqptp(classic, false); p.Run {
		t.Error("a classic build must not start a clock daemon")
	}
	if p := PlanNqptp(classic, true); p.Run {
		t.Error("a classic build must not start a clock daemon even if one is installed")
	}

	p := PlanNqptp(ap2, false)
	if p.Run {
		t.Error("nqptp cannot be started when it is not installed")
	}
	if p.Reason != "airplay2_no_nqptp" {
		t.Errorf("reason = %q, want a reason a dashboard can show", p.Reason)
	}

	if p := PlanNqptp(ap2, true); !p.Run || p.Reason != "airplay2" {
		t.Errorf("got %+v, want Run with reason airplay2", p)
	}
}

func TestNqptpAvailableSeparatesMissingFromBroken(t *testing.T) {
	dir := t.TempDir()

	if ok, why := NqptpAvailable(filepath.Join(dir, "nope")); ok || why != "not_installed" {
		t.Errorf("missing: ok=%v why=%q", ok, why)
	}

	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, why := NqptpAvailable(sub); ok || why != "not_a_file" {
		t.Errorf("directory: ok=%v why=%q", ok, why)
	}

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, why := NqptpAvailable(plain); ok || why != "not_executable" {
		t.Errorf("non-executable: ok=%v why=%q", ok, why)
	}

	if err := os.Chmod(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, why := NqptpAvailable(plain); !ok || why != "ok" {
		t.Errorf("executable: ok=%v why=%q", ok, why)
	}
}

func TestNqptpStartRefusesWhenNotInstalled(t *testing.T) {
	n := &Nqptp{Path: filepath.Join(t.TempDir(), "nqptp")}
	err := n.Start()
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, ErrNoNqptp) {
		t.Errorf("err = %v, want it to wrap ErrNoNqptp", err)
	}
	if n.Running() {
		t.Error("a refused start must not leave the supervisor marked running")
	}
}

// Both processes must map the same object or shairport reads a record that
// never changes — which looks like a clock that will not sync, not like a
// path problem. The value is resolved in one place for that reason.
func TestNqptpShmDirPrefersTheExplicitValue(t *testing.T) {
	t.Setenv(ShmDirEnv, "/from/env")

	if got := (&Nqptp{ShmDir: "/explicit"}).shmDir(); got != "/explicit" {
		t.Errorf("shmDir = %q, want the explicit value", got)
	}
	if got := (&Nqptp{}).shmDir(); got != "/from/env" {
		t.Errorf("shmDir = %q, want the environment value", got)
	}

	t.Setenv(ShmDirEnv, "")
	if got := (&Nqptp{}).shmDir(); got != DefaultShmDir {
		t.Errorf("shmDir = %q, want %q", got, DefaultShmDir)
	}
}

func TestNqptpStopIsIdempotent(t *testing.T) {
	n := &Nqptp{Path: filepath.Join(t.TempDir(), "nqptp")}
	n.Stop()
	n.Stop()
	if n.Running() {
		t.Error("Running after Stop")
	}
}
