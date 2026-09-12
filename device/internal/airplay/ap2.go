package airplay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse/internal/orphan"
)

// NqptpPath is the PTP clock daemon AirPlay 2 times against. A SECOND process
// on the device, beside shairport-sync.
//
// It is a separate program rather than a thread because that is what upstream
// ships: it needs UDP 319 and 320 to ITSELF — shairport-sync cannot hold them
// and neither can anything else — and it publishes the clock through a shared
// memory object that shairport reads. On bionic that object is a file under a
// tmpfs; see device/shairport/compat/android_shm.c.
const NqptpPath = "/data/local/bin/nqptp"

// ShmDirEnv and its default have to agree between the two processes, or each
// maps a different file and shairport reads a record that never changes —
// which presents as AirPlay 2 audio that will not synchronise rather than as
// anything to do with memory. The firmware passes the value explicitly to BOTH
// rather than letting them both fall back to the same default, so a change to
// one cannot silently desync them.
const (
	ShmDirEnv     = "REVOICE_SHM_DIR"
	DefaultShmDir = "/dev/revoice-shm"
)

// Flavour is what an installed shairport-sync binary actually IS.
//
// There is no config key for "enable AirPlay 2" and there should not be: the
// device has one binary at BinaryPath, and whether it speaks AirPlay 2 is a
// property of how that binary was built. A toggle would be a second opinion
// about a question the file already answers — and the two could disagree,
// which is the shape of failure this project keeps naming.
type Flavour struct {
	// Version is the whole string the binary reported, kept for the log and
	// for airplay_status.
	Version string
	// AirPlay2 is true when the build carries AirPlay 2 support.
	AirPlay2 bool
	// ShmVersion is nqptp's shared-memory STRUCTURE version, which the binary
	// states as `-smi<N>`. It is an ABI number, not a release number: a
	// shairport built against one and an nqptp publishing another do not
	// interoperate, and neither says so in a way a user would see.
	ShmVersion int
}

// The version string is assembled by get_version_string() in shairport-sync's
// common.c, which appends one hyphenated token per compiled-in feature —
// "4.3.7-AirPlay2-smi10-alac-stdout-metadata-...". Matching on the tokens
// rather than parsing the whole thing means a build that gains a feature does
// not stop being recognised.
var (
	reAirPlay2 = regexp.MustCompile(`(^|-)AirPlay2(-|$)`)
	reShmVer   = regexp.MustCompile(`(^|-)smi([0-9]+)(-|$)`)
)

// ParseVersion reads `shairport-sync -V` output.
//
// Tolerant of surrounding whitespace and of extra lines, because -V has
// printed more than one line in some builds and a diagnostic that breaks on a
// newline is worse than no diagnostic.
func ParseVersion(out string) Flavour {
	f := Flavour{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if f.Version == "" {
			f.Version = line
		}
		if reAirPlay2.MatchString(line) {
			f.AirPlay2 = true
			f.Version = line
		}
		if m := reShmVer.FindStringSubmatch(line); m != nil {
			if n, err := strconv.Atoi(m[2]); err == nil {
				f.ShmVersion = n
			}
		}
	}
	return f
}

// DetectFlavour asks the installed binary what it is.
//
// `run` is injected so this is testable without a binary; production passes
// nil and gets exec. A binary that cannot be run at all is not an error the
// caller should act on beyond reporting it — the receiver may still start, and
// deciding otherwise from a failed `-V` would turn a diagnostic into an
// outage.
func DetectFlavour(binary string, run func(name string, args ...string) ([]byte, error)) (Flavour, error) {
	if run == nil {
		run = func(name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	out, err := run(binary, "-V")
	if err != nil && len(out) == 0 {
		return Flavour{}, fmt.Errorf("airplay: cannot run %s -V: %w", binary, err)
	}
	// Output is used even when the process exited non-zero: some builds print
	// the version and then complain about something unrelated.
	return ParseVersion(string(out)), nil
}

// NqptpPlan says what to do about the clock daemon, and why.
//
// Split out from the doing so the decision can be read and tested. The reason
// string is not decoration: every branch here is a state somebody will have to
// diagnose from a dashboard, and "AirPlay 2 is installed but nqptp is not" is
// otherwise indistinguishable from "AirPlay is broken".
type NqptpPlan struct {
	Run    bool
	Reason string
}

// PlanNqptp decides whether to run the clock daemon.
//
// The rule that matters is the last one: an AirPlay 2 binary with no nqptp
// still serves CLASSIC AirPlay perfectly, so a missing clock daemon must not
// stop the receiver. Refusing to start would trade a degraded feature for no
// feature, which is the opposite of what this project's compatibility rule
// asks for.
func PlanNqptp(f Flavour, nqptpInstalled bool) NqptpPlan {
	switch {
	case !f.AirPlay2:
		return NqptpPlan{Run: false, Reason: "classic build — no clock daemon needed"}
	case !nqptpInstalled:
		return NqptpPlan{Run: false, Reason: "airplay2_no_nqptp"}
	default:
		return NqptpPlan{Run: true, Reason: "airplay2"}
	}
}

// Nqptp supervises the clock daemon.
//
// Deliberately its own small supervisor rather than a second Client: it has no
// audio, no music plane, no config file and no metadata. What it shares with
// the receiver is the restart discipline, and that is copied rather than
// abstracted — two supervisors in one package that read the same way are
// cheaper than one that has to be parameterised for both.
type Nqptp struct {
	Path   string
	ShmDir string

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	restarts int
	lastExit string
}

// ErrNoNqptp means the clock daemon is not installed.
var ErrNoNqptp = errors.New("airplay: nqptp is not installed on this device")

// NqptpAvailable reports whether the daemon is present and executable, with
// the same three-way answer Report() gives for shairport-sync: a missing file
// and a broken feature must not look alike.
func NqptpAvailable(path string) (bool, string) {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return false, "not_installed"
	case info.IsDir():
		return false, "not_a_file"
	case info.Mode()&0o111 == 0:
		return false, "not_executable"
	default:
		return true, "ok"
	}
}

// Start brings the daemon up. Idempotent.
func (n *Nqptp) Start() error {
	if n.Path == "" {
		n.Path = NqptpPath
	}
	if ok, why := NqptpAvailable(n.Path); !ok {
		return fmt.Errorf("%w (%s: %s)", ErrNoNqptp, n.Path, why)
	}

	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.running = true
	n.cancel = cancel
	n.restarts = 0
	n.mu.Unlock()

	// An orphan holds 319 and 320, and nothing else on the device can take
	// them back. Exactly the failure internal/orphan was written for — an
	// orphaned shairport-sync held TCP 5000 for two hours while every panel
	// reported the endpoint healthy — and worse here, because nqptp exits
	// immediately when it cannot bind, so the supervisor below would simply
	// loop for ever against a process it cannot see.
	//
	// At START rather than only at shutdown, for that package's reason: a
	// cleanup on the way out cannot run after kill -9, after a panic, or on
	// the supervisor's own restart path.
	if k := orphan.Takeover(n.Path); k > 0 {
		log.Printf("[airplay] stopped %d orphaned nqptp instance(s) left by a previous run", k)
	}

	log.Printf("[airplay] nqptp enabled (shm %s)", n.shmDir())
	go n.supervise(ctx)
	return nil
}

// Stop takes it down. Idempotent.
func (n *Nqptp) Stop() {
	n.mu.Lock()
	cancel := n.cancel
	n.running = false
	n.cancel = nil
	n.mu.Unlock()
	if cancel != nil {
		cancel()
		log.Printf("[airplay] nqptp disabled")
	}
}

func (n *Nqptp) shmDir() string {
	if n.ShmDir != "" {
		return n.ShmDir
	}
	if v := os.Getenv(ShmDirEnv); v != "" {
		return v
	}
	return DefaultShmDir
}

// Running says the supervisor is up — not that a process exists right now.
// The distinction is endpoint.Health's, and it is the one that mattered when
// an orphaned shairport-sync held port 5000 for two hours while every panel
// reported the endpoint fine.
func (n *Nqptp) Running() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.running
}

// Restarts counts exits since Start, so a climbing number separates "briefly
// between sessions" from "failing every few seconds". LastExit carries the
// reason, because `exit status 1` — a port it cannot bind — against
// `signal: killed` is the whole difference.
func (n *Nqptp) Restarts() (int, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.restarts, n.lastExit
}

func (n *Nqptp) supervise(ctx context.Context) {
	backoff := restartMin
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := n.session(ctx)
		if ctx.Err() != nil {
			return
		}

		n.mu.Lock()
		n.restarts++
		if err != nil {
			n.lastExit = err.Error()
		} else {
			n.lastExit = "exited cleanly"
		}
		n.mu.Unlock()

		if err != nil {
			// Worth a line every time rather than once: the failure this is
			// most likely to hit is UDP 319/320 already held, which is
			// permanent until something else lets go, and a single line at
			// the top of an uptime is a line nobody finds.
			log.Printf("[airplay] nqptp exited: %v", err)
		}
		if time.Since(start) > 30*time.Second {
			backoff = restartMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > restartMax {
			backoff = restartMax
		}
	}
}

func (n *Nqptp) session(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, n.Path)
	// The shm directory is passed EXPLICITLY rather than inherited, so the
	// value this process decided on is the value the daemon uses. Both halves
	// of the clock interface must agree on the path; inheriting leaves that to
	// whatever the supervisor's environment happened to hold.
	cmd.Env = append(os.Environ(), ShmDirEnv+"="+n.shmDir())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go relayLog(stdout)
	go relayLog(stderr)
	return cmd.Wait()
}
