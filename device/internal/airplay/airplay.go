// Package airplay runs an AirPlay receiver on the device itself.
//
// A phone, a Mac or an iPad picks the Echo out of its AirPlay list and plays
// to it directly. Like Sendspin and Spotify Connect, it is a producer of the
// EXISTING music plane — same duck, same mixer, same prime gate, same
// arbiter — and shairport-sync runs as a subprocess for the same reasons
// librespot does: it is C, this is Go, and a receiver that crashes on a
// device sharing 512MB with Android should take nothing with it.
//
// # What is actually achievable here, stated plainly
//
// The user asked for AirPlay 2 and that remains the target. It is worth
// writing down what stands between here and there, because the answer changed
// once the dependency list was read rather than assumed:
//
//   - **AirPlay 2 needs twelve native libraries**, including the ffmpeg trio
//     (libavutil/libavcodec/libavformat) for AAC-ELD, plus libplist,
//     libsodium, libgcrypt, uuid and libsoxr. Every one has to be
//     cross-compiled for bionic at API 22. Classic AirPlay needs three or
//     four, and ALAC is decoded in-tree.
//   - **AirPlay 2 needs Avahi**, which is a D-Bus daemon. Android has no
//     D-Bus and no Avahi. Classic AirPlay can use shairport-sync's bundled
//     tinysvcmdns and needs neither.
//   - **AirPlay 2 needs nqptp**, a second daemon binding UDP 319/320 for PTP.
//     PTP wants timestamps this 2015 MediaTek kernel does not provide in
//     hardware.
//   - **shairport-sync's own stated minimum is a 2018-or-later Linux and "a
//     Raspberry Pi B or better"**. This is a 2015 MT8163 on Android 5.1.
//     Under the minimum is not the same as impossible, and it is not a
//     footing to plan from either.
//
// So the build recipe targets CLASSIC AirPlay first, and this package does
// not care which it gets: both speak the same subprocess interface — PCM on
// stdout — and the only difference that reaches this code is the sample rate.
// AirPlay 2 is 48kHz and needs no conversion; classic is 44.1kHz and goes
// through internal/resample. Nothing here has to change when the AirPlay 2
// build lands.
package airplay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse/internal/endpoint"
	"github.com/wilbowes/EchoMuse/internal/musicplane"
	"github.com/wilbowes/EchoMuse/internal/orphan"
	"github.com/wilbowes/EchoMuse/internal/pcm"
	"github.com/wilbowes/EchoMuse/internal/resample"
)

// BinaryPath is where the controller installs shairport-sync.
const BinaryPath = "/data/local/bin/shairport-sync"

// ConfigPath is shairport-sync's own configuration file. Written by this
// package rather than pushed as a payload: what goes in it is DERIVED from
// this device's own audio pipeline, so a payload would be a second copy of a
// number only the firmware knows.
//
// It was declared and never written for the whole life of this package, which
// is why `audio_backend_latency_offset_in_seconds` — the one setting that can
// take latency out of an AirPlay stream — was unreachable.
const ConfigPath = "/data/local/etc/echomuse/shairport-sync.conf"

const (
	// SourceRate is what classic AirPlay delivers, by definition. AirPlay 2
	// is 48000 and skips the resampler; the rate is DETECTED from the
	// binary's own report rather than assumed, because getting it wrong is
	// a stream that plays 8.8% fast and reads as a broken receiver.
	SourceRate = 44100
	// DeviceRate is what the speaker runs at.
	DeviceRate = 48000

	// stereoBytesPerFrame for shairport-sync's stdout format.
	stereoBytesPerFrame = 4

	// readFrames is how much is taken from the pipe at once. Chosen so the
	// resampled result lands near a whole ALSA period without the caller
	// having to carry a large remainder.
	readFrames = 2048
)

const (
	restartMin = 3 * time.Second
	restartMax = time.Minute
)

// ErrNoBinary means shairport-sync is not installed on this device.
//
// Named, not logged, for the reason the Spotify one is: a toggle that saves,
// reports success and plays nothing is the failure this codebase names most
// often, and "the binary was never pushed" is indistinguishable from "AirPlay
// is broken" from the front of a dashboard.
var ErrNoBinary = errors.New("airplay: shairport-sync is not installed on this device")

// MusicSink is the device's music plane.
type MusicSink interface {
	PumpMusic(data []byte) error
	EndMusicStream()
	FlushMusic()
}

// PlaneOwner is the arbitration.
type PlaneOwner interface {
	Claim() bool
	Release()
	MayWrite() bool
}

// Options configure the receiver.
type Options struct {
	// Name is what appears in the AirPlay list.
	Name string
	// Binary overrides BinaryPath, for tests.
	Binary string
	// SourceRate is the rate the binary emits. 44100 for classic AirPlay,
	// 48000 for AirPlay 2. Zero means classic.
	SourceRate int
	// BackendDelaySec is how long OUR side holds a sample between taking it
	// off the pipe and the speaker emitting it — the music plane's prime
	// depth. shairport-sync plays each packet at the instant the sender
	// stamped it, so it has to be told what sits behind it or every packet
	// lands that much late; see renderConfig for the sign.
	//
	// Zero writes no offset at all rather than writing 0.0, so a caller that
	// does not know its own delay does not assert that there is none.
	BackendDelaySec float64
	// ConfigPath overrides ConfigPath, for tests.
	ConfigPath string
	// ExtraArgs are appended verbatim.
	ExtraArgs []string
	// OnVolume, when set, receives the AirPlay volume in dB whenever a phone
	// moves its slider — and setting it is what turns metadata on at all.
	// Change it live with SetVolumeHandler; this field is only the initial
	// value, and a caller whose setting arrives from a controller should use
	// that instead of relying on this.
	//
	// The gate is the callback rather than a flag: metadata costs a pipe, a
	// goroutine and a config block, and a nil callback means nothing would
	// read them. One thing to be true rather than two that can disagree.
	OnVolume func(db float64)
	// MetadataPipe is where shairport-sync writes metadata. Overridable for
	// tests; MetadataPipePath otherwise.
	MetadataPipe string
}

// MetadataPipePath is the FIFO shairport-sync writes metadata to.
//
// Under /data rather than /tmp, unlike most scratch on this device: /tmp is
// RAM-backed and cleared at boot, which is fine for a FIFO, but the directory
// itself has to exist before shairport-sync opens it and /data is where every
// other path this firmware owns already lives. It is a named pipe, so it
// stores nothing and costs no flash writes.
const MetadataPipePath = "/data/local/etc/echomuse/airplay-metadata"

// Client supervises one shairport-sync process.
type Client struct {
	opts  Options
	sink  MusicSink
	plane PlaneOwner

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc

	// What the endpoint has actually been doing, for endpoint.Health.
	// Under mu with the rest: they are read together and a torn pair is a
	// report that contradicts itself.
	restarts  int
	startedAt time.Time
	lastExit  string
	proc      *os.Process

	// metaStop closes the metadata reader. Non-nil exactly while one runs,
	// which is what syncMetadataReader reconciles against.
	metaStop chan struct{}
}

// New wires a client. It starts nothing.
func New(opts Options, sink MusicSink, plane PlaneOwner) *Client {
	if opts.Binary == "" {
		opts.Binary = BinaryPath
	}
	if opts.SourceRate == 0 {
		opts.SourceRate = SourceRate
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = ConfigPath
	}
	return &Client{opts: opts, sink: sink, plane: plane}
}

// Available reports whether shairport-sync is installed, and why not when it
// is not.
func (c *Client) Available() (bool, error) {
	info, err := os.Stat(c.opts.Binary)
	if err != nil {
		return false, fmt.Errorf("%w (%s)", ErrNoBinary, c.opts.Binary)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return false, fmt.Errorf("%s exists but is not executable", c.opts.Binary)
	}
	return true, nil
}

// Report says whether AirPlay can run on this device, and why not when it
// cannot. Rides the register message beside spotify_status, for that field's
// reason: a capability says what the FIRMWARE can do, and when the answer to
// "why is this off" is a missing file, nobody can tell that from a broken
// feature without a shell session on the user's own hardware.
func Report() map[string]any {
	rep := map[string]any{"binary": BinaryPath}
	info, err := os.Stat(BinaryPath)
	switch {
	case err != nil:
		rep["ok"] = false
		rep["reason"] = "not_installed"
	case info.IsDir():
		rep["ok"] = false
		rep["reason"] = "not_a_file"
	case info.Mode()&0o111 == 0:
		rep["ok"] = false
		rep["reason"] = "not_executable"
	default:
		rep["ok"] = true
		rep["size"] = info.Size()
	}
	return rep
}

// Start brings the receiver up. Idempotent.
func (c *Client) Start() error {
	if ok, err := c.Available(); !ok {
		return err
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	// Counted from the moment this was turned on, so "restarts" answers "since
	// you enabled it" rather than "since the device booted". A toggle off and
	// on is a fresh question, and carrying the old count into it would make a
	// deliberate restart look like a fault.
	c.restarts = 0
	c.lastExit = ""
	c.mu.Unlock()

	// Take the ports over from a previous instance before starting our own.
	// A firmware restart does not take its children with it — they are
	// reparented to init and keep holding the ports their protocol is defined
	// on, so the new process cannot bind and exits immediately, for ever. Seen
	// on a device 2026-09-10 and diagnosed there; see internal/orphan.
	//
	// At START rather than only at shutdown, because a cleanup on the way out
	// cannot run after `kill -9`, after a panic, or on the supervisor's own
	// restart path, and does nothing for a device already looping — which on a
	// fielded fleet is every device that has ever been updated.
	if n := orphan.Takeover(c.opts.Binary); n > 0 {
		log.Printf("[airplay] stopped %d orphaned instance(s) left by a previous run", n)
	}

	// Before supervise, deliberately: see syncMetadataReader.
	c.syncMetadataReader()

	log.Printf("[airplay] enabled as %q (source %dHz)", c.name(), c.opts.SourceRate)
	go c.supervise(ctx)
	return nil
}

// Stop ends the receiver. Idempotent.
//
// Killing the process removes the Echo from every AirPlay list on the
// network, which is the right outcome: a receiver that is listed, selected
// and silent is worse than one that is not listed. The same reasoning as
// Spotify's, and reached the same way, because AirPlay offers no goodbye
// from outside the receiver either.
func (c *Client) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	cancel := c.cancel
	c.cancel = nil
	// The reader is stopped HERE rather than off ctx, so the one thing that
	// owns its lifetime is syncMetadataReader's `metaStop != nil` — two
	// owners is how a goroutine ends up outliving the thing it reads for.
	if c.metaStop != nil {
		close(c.metaStop)
		c.metaStop = nil
	}
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.kill()
	log.Println("[airplay] disabled")
}

// Running reports whether the supervisor is up.
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// SetName changes the name shown in the AirPlay list, restarting a running
// receiver — the name is a command-line argument shairport-sync reads once,
// and without the restart a rename in Home Assistant would save, report
// success and change nothing until the next reboot.
// syncMetadataReader makes the reader goroutine match the handler: running
// when there is somebody to call, stopped when there is not.
//
// **The dangerous direction is turning it ON while the endpoint runs.** The
// config file tells shairport-sync to write metadata down a FIFO; a FIFO with
// no reader fills at 64KB and then BLOCKS the writer — which is the process
// decoding the audio. So the reader must exist before the config that asks
// for it, and it must not be left behind either: the reader outliving the
// handler is only a leaked goroutine, but it is one that lives until Stop.
//
// The reader is deliberately NOT tied to one shairport-sync process. The pipe
// survives a restart of the writer, so tying this to `session` would tear the
// reader down and rebuild it on every crash-restart.
//
// Called with c.mu NOT held.
func (c *Client) syncMetadataReader() {
	pipe := c.metadataPipe()

	c.mu.Lock()
	want := pipe != ""
	have := c.metaStop != nil
	if want == have {
		c.mu.Unlock()
		return
	}
	if !want {
		close(c.metaStop)
		c.metaStop = nil
		c.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	c.metaStop = stop
	fn := c.opts.OnVolume
	c.mu.Unlock()

	go startMetadataReader(pipe, stop, fn)
}

// SetVolumeHandler installs (or removes) the AirPlay volume callback, live.
//
// **The handler cannot be fixed at New(), and assuming it could was a bug in
// this feature's first draft.** The setting arrives on a config push, long
// after the client is wired, so a callback resolved once at startup meant
// turning the setting on did nothing until the firmware restarted — while the
// dashboard said it took effect at the next AirPlay start.
//
// Whether the handler is nil decides whether shairport-sync is asked for
// metadata at all, and that decision is written into its CONFIG FILE, which
// is produced when the process starts. So a change from nil to non-nil (or
// back) has to restart the receiver, exactly as a rename does — otherwise the
// running process keeps the config it was launched with and the pipe stays
// empty. A change that does not cross that boundary restarts nothing.
func (c *Client) SetVolumeHandler(fn func(db float64)) {
	c.mu.Lock()
	was := c.opts.OnVolume != nil
	now := fn != nil
	c.opts.OnVolume = fn
	running := c.running
	c.mu.Unlock()

	if was == now || !running {
		return
	}
	// Reader first, THEN the restart that rewrites the config: the new
	// process must never be the one waiting for a reader to appear.
	c.syncMetadataReader()
	log.Printf("[airplay] volume control %s — restarting the receiver",
		map[bool]string{true: "on", false: "off"}[now])
	c.kill()
}

func (c *Client) SetName(name string) {
	c.mu.Lock()
	if name == "" || name == c.opts.Name {
		c.mu.Unlock()
		return
	}
	c.opts.Name = name
	running := c.running
	c.mu.Unlock()
	if running {
		log.Printf("[airplay] renamed to %q — restarting the receiver", name)
		c.kill()
	}
}

// Health is what this endpoint can say about itself right now.
//
// Enabled and Alive are two questions and the gap between them is the whole
// point: a supervisor that is up while nothing is running, restart after
// restart, is a binary that cannot start — which every other report on this
// device renders as healthy, because the file is present and executable.
func (c *Client) Health() endpoint.Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := endpoint.Health{
		Enabled:  c.running,
		Alive:    c.proc != nil,
		Restarts: c.restarts,
		LastExit: c.lastExit,
	}
	// Only meaningful while something is running, and reporting the age of a
	// process that has exited would read as uptime it does not have.
	if c.proc != nil && !c.startedAt.IsZero() {
		h.UptimeS = int(time.Since(c.startedAt).Seconds())
	}
	return h
}

// Leave ends the current session because something else took the music plane.
// The supervisor stays up, so the Echo comes back as an AirPlay target once
// the plane is free. It does not resume: reappearing as a receiver is not the
// same as taking back a stream somebody moved elsewhere.
func (c *Client) Leave(reason string) {
	c.mu.Lock()
	proc, running := c.proc, c.running
	c.mu.Unlock()
	if proc == nil || !running {
		return
	}
	log.Printf("[airplay] ending the session: %s", reason)
	c.kill()
}

func (c *Client) kill() {
	c.mu.Lock()
	proc := c.proc
	c.proc = nil
	c.mu.Unlock()
	if proc != nil {
		_ = proc.Kill()
	}
}

func (c *Client) name() string {
	if c.opts.Name != "" {
		return c.opts.Name
	}
	return "EchoMuse"
}

// args builds shairport-sync's command line.
//
//   - `-o stdout` sends PCM to stdout. No ALSA in shairport-sync at all: the
//     device already owns the speaker, and two things opening it is the #80
//     failure — a blocking open with no timeout and eighteen minutes of a
//     stranded device.
//   - `-a <name>` is what appears in the AirPlay list.
//   - `--` separates the backend's own options, and `-d` under stdout would
//     mean something else entirely; nothing is passed after it by default.
//   - `-c <file>` is the config written by writeConfig, carrying the one
//     setting that cannot go on the command line. Passed only when the file
//     was actually written: shairport-sync REFUSES TO START on a missing
//     config file, so a failed write must cost the latency compensation and
//     not the receiver.
func (c *Client) args(cfg string) []string {
	a := []string{"-a", c.name(), "-o", "stdout"}
	if cfg != "" {
		a = append(a, "-c", cfg)
	}
	return append(a, c.opts.ExtraArgs...)
}

// renderConfig builds the shairport-sync configuration.
//
// It carries ONE setting. The name stays on the command line where it already
// was — it is what SetName restarts the receiver for, and a second copy in a
// file is a second thing to keep in step.
//
// **The sign.** shairport-sync's own sample puts it plainly: "if the output
// device delays by 100 ms, set this to -0.1". Our music plane holds
// `localPrimePeriods` before it starts, so the delay behind shairport is that
// depth, and the compensation is its negative — shairport then hands the audio
// over that much earlier and the sound leaves the speaker at the instant the
// sender scheduled it, rather than a buffer-length late.
//
// Zero delay writes NO offset rather than `0.0`. The two are the same number
// and not the same statement: absent means nobody measured, and a caller that
// does not know its own delay should not assert there is none.
func renderConfig(delaySec float64, metadataPipe string) string {
	general := "general = {\n"
	if delaySec != 0 {
		general += fmt.Sprintf(
			"  audio_backend_latency_offset_in_seconds = %.4f;\n", -delaySec)
	}
	general += "};\n"
	if metadataPipe == "" {
		return general
	}
	// Cover art is refused explicitly rather than left to the default. It is
	// megabytes per track down a pipe whose only reader wants twenty bytes of
	// volume, on a device sharing 512MB with Android — and the scanner would
	// have to walk past every byte of it. `include_cover_art` defaults to
	// "no", so this asserts what we are relying on rather than changing it:
	// a future default flip would otherwise arrive as a memory problem with
	// no obvious cause.
	return general + fmt.Sprintf(
		"metadata = {\n"+
			"  enabled = \"yes\";\n"+
			"  include_cover_art = \"no\";\n"+
			"  pipe_name = \"%s\";\n"+
			"};\n", metadataPipe)
}

// metadataPipe is the pipe to ask shairport-sync for metadata on, or "" when
// nothing is listening.
//
// Driven by OnVolume rather than by a separate flag: a config block telling
// shairport to write down a pipe nobody opens would fill its buffer and
// eventually block the process that is playing the music.
func (c *Client) metadataPipe() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opts.OnVolume == nil {
		return ""
	}
	if c.opts.MetadataPipe != "" {
		return c.opts.MetadataPipe
	}
	return MetadataPipePath
}

// writeConfig puts the configuration where shairport-sync will read it, and
// returns the path — or "" if it could not be written.
//
// Temp file and rename, so a half-written config can never be read:
// shairport-sync refuses to start on a config it cannot parse, and the
// observable of that is a receiver that never appears, with the reason on a
// stderr nobody is reading yet.
//
// A failure returns "" rather than an error the caller must decide about,
// because there is only one sensible decision: start WITHOUT the config. The
// compensation is worth ~171ms; the receiver is worth AirPlay working at all.
func (c *Client) writeConfig() string {
	path := c.opts.ConfigPath
	if path == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[airplay] cannot create %s: %v — starting without the "+
			"latency offset", filepath.Dir(path), err)
		return ""
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, []byte(renderConfig(c.opts.BackendDelaySec, c.metadataPipe())), 0o644); err != nil {
		log.Printf("[airplay] cannot write %s: %v — starting without the "+
			"latency offset", tmp, err)
		return ""
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[airplay] cannot install %s: %v — starting without the "+
			"latency offset", path, err)
		_ = os.Remove(tmp)
		return ""
	}
	return path
}

func (c *Client) supervise(ctx context.Context) {
	backoff := restartMin
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[airplay] shairport-sync exited: %v", err)
		}
		// A receiver that ran and then exited is ordinary — a sender
		// disconnected, the network blipped. One that dies immediately is a
		// configuration or binary problem, and backing off is what stops it
		// filling the log.
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

func (c *Client) session(ctx context.Context) error {
	// Rewritten per session rather than once at Start: the delay it describes
	// is a property of this device's pipeline, and a session is the only
	// moment that is certain to be before shairport reads it.
	cmd := exec.CommandContext(ctx, c.opts.Binary, c.args(c.writeConfig())...)
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

	c.mu.Lock()
	c.proc = cmd.Process
	c.startedAt = time.Now()
	c.restarts++
	c.mu.Unlock()

	go relayLog(stderr)
	c.pump(stdout)

	err = cmd.Wait()
	c.mu.Lock()
	c.proc = nil
	// Kept whatever it says, including nil: "exited cleanly" and "exit status
	// 1 every minute for two hours" are both answers, and blanking the field
	// on a clean exit would make the second one look like the first between
	// restarts.
	if err != nil {
		c.lastExit = err.Error()
	} else {
		c.lastExit = "exited cleanly"
	}
	c.mu.Unlock()

	c.sink.EndMusicStream()
	c.plane.Release()
	return err
}

// pump reads PCM from shairport-sync, converts it, and feeds the music plane.
//
// **shairport-sync writes at realtime, not as fast as the pipe accepts**, and
// that is the difference from librespot worth knowing: it has its own clock
// and paces itself, so the backpressure from a full music plane is a fallback
// rather than the pacing mechanism. It still matters — a device whose plane
// backs up must not have this goroutine spin — but nothing here depends on it.
func (c *Client) pump(r io.Reader) {
	br := bufio.NewReaderSize(r, readFrames*stereoBytesPerFrame)
	buf := make([]byte, readFrames*stereoBytesPerFrame)

	// One converter per SESSION, not per chunk: it carries filter history,
	// and a fresh instance mid-stream restarts from silence and clicks. The
	// filter is skipped entirely at 48kHz, which is what AirPlay 2 delivers.
	conv := resample.NewStreamConverter(c.opts.SourceRate)
	// Whole periods only — see pcm.PeriodWriter. The music plane is not a
	// byte stream: the ALSA loop takes one item off the channel per
	// iteration and hands it to the hardware as a PERIOD, so a short buffer
	// becomes a short period — a glitch, counted in the instrumentation as
	// a whole one. Nothing about a resampled read lands on a boundary.
	pw := pcm.NewPeriodWriter(pcm.MusicPeriodBytes, c.sink.PumpMusic)
	// Claimed on the FIRST AUDIO rather than at process start: shairport-sync
	// runs continuously so it can appear in the AirPlay list, and it is silent
	// until somebody selects it. Claiming at start would take the plane from
	// Home Assistant for a receiver nobody is playing to.
	//
	// And GIVEN BACK when the audio stops, which is the half that was missing
	// (2026-09-10). Release used to sit after cmd.Wait, so the plane was held
	// until the PROCESS exited — and the process is a daemon that outlives
	// every session by design. A phone that disconnected left this device
	// reported as playing AirPlay until it rebooted.
	claim := musicplane.NewIdleClaim(c.plane, musicplane.DefaultIdle)
	defer claim.Stop()

	for {
		n, err := io.ReadFull(br, buf)
		if n >= stereoBytesPerFrame {
			if !claim.Feed() {
				continue
			}
			out := c.convert(conv, buf[:n-n%stereoBytesPerFrame])
			if perr := pw.Write(out); perr != nil {
				log.Printf("[airplay] PumpMusic: %v", perr)
				return
			}
		}
		if err != nil {
			// The tail IS padded: the last milliseconds of a track are
			// inaudible as a gap and obvious as a click if the period is
			// left half full.
			if perr := pw.Flush(); perr != nil {
				log.Printf("[airplay] final period: %v", perr)
			}
			return
		}
	}
}

// convert turns stereo 16-bit at the source rate into mono 16-bit at 48kHz.
//
// The five steps live in resample.StreamConverter now, because Spotify needs
// exactly the same ones: no released librespot has a --sample-rate option
// either, so both sources arrive at 44.1kHz and both have to be converted
// here. Three copies of "downmix, resample, clamp" was one too many.
func (c *Client) convert(conv *resample.StreamConverter, stereo []byte) []byte {
	return conv.Convert(stereo, pcm.DownmixStereo)
}

func relayLog(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		log.Printf("[shairport] %s", s.Text())
	}
}
