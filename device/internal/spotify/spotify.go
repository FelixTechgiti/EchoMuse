// Package spotify runs a Spotify Connect endpoint on the device itself.
//
// The Echo appears in the Spotify app as a speaker and plays from it directly,
// with no Home Assistant and no controller in the audio path. That is the
// point: it keeps playing through a controller restart, and it is what makes
// the device useful to somebody who is not standing in front of a voice
// assistant.
//
// # It is a producer of the existing music plane
//
// Like Sendspin, and for the same reasons: the audio is the same KIND of
// audio, so the duck, the saturating mix, the prime gate and the underrun
// accounting all apply unmodified. No new frame type, no new mixer input, no
// new row in the ownership ladder. Home Assistant still wins, and the
// arbiter in internal/musicplane still decides.
//
// # librespot runs as a subprocess, and that is a deliberate choice
//
// It is Rust, and this is a Go program. The alternative — go-librespot — was
// looked at and is worse on every axis that matters here: it requires Go
// 1.25, above the pinned compiler this firmware is built with, and it needs
// cgo against libogg, libvorbis, flac and mpg123, which is four native
// libraries to cross-compile for Android/bionic instead of none. librespot
// with rustls needs no system libraries at all.
//
// The subprocess boundary also buys isolation worth having on a device with
// 512MB shared with Android: a Spotify client that leaks or crashes takes
// nothing with it, and the supervisor restarts it.
//
// # The pipe backend, at 44.1kHz — and a resampler after it
//
// `--backend pipe --format S16` gives raw stereo 16-bit PCM on stdout. It
// gives it at 44,100 frames a second and there is no way to ask for anything
// else: no released librespot has a `--sample-rate` option, and neither does
// `dev`. This was designed the other way round first, on the assumption that
// librespot could resample for us — it cannot, and passing the flag is not a
// silent fallback, it makes librespot refuse to start.
//
// So the conversion happens here, through internal/resample, the same path
// AirPlay uses. It costs 4-8% of one A53 core while Spotify is playing.
//
// **The pipe backend is famously "too fast", and here that is the feature.**
// librespot writes as fast as the pipe accepts, with no pacing of its own —
// which is a problem when the far end is a file and a virtue when it is a
// buffer that blocks. PumpMusic blocks once the music plane is full, the
// pipe backpressures, and librespot is paced to playback for free. Nothing
// in this package rate-limits anything, deliberately.
//
// # Nothing here is scheduled
//
// Unlike Sendspin, Spotify Connect is one device playing one stream: there is
// no group to stay in step with, no server timestamps, and therefore no clock
// filter and no drift correction. The device plays at its own rate because
// its own rate is the only one that matters.
package spotify

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse/internal/pcm"
	"github.com/wilbowes/EchoMuse/internal/resample"
)

// BinaryPath is where the controller installs librespot.
//
// Beside the firmware rather than in /system: /data/local/bin is writable,
// survives a reboot, and is already where start_server.sh and the A/B server
// slots live — so it is covered by the same backup and the same expectations.
const BinaryPath = "/data/local/bin/librespot"

// CacheDir holds librespot's credential blob, so the Echo stays authorised
// across reboots and the user does not have to re-select it in the app after
// every restart.
const CacheDir = "/data/local/etc/echomuse/spotify"

const (
	// SourceRate is what librespot's pipe backend emits, and it is not
	// negotiable: no released version has a --sample-rate option. DeviceRate
	// is what the speaker runs at, so everything is resampled.
	SourceRate = 44100
	DeviceRate = 48000
	// Channels: librespot's pipe backend is stereo and mono cannot be
	// requested either. The downmix here is one add and a shift per frame.
	Channels = 2

	// bytesPerFrame for stereo 16-bit input.
	bytesPerFrame = Channels * 2

	// readChunkFrames is how much is taken from the pipe at once. A period
	// is 2048 frames; reading in period multiples means the plane is fed
	// whole periods with no remainder to carry.
	readChunkFrames = 2048
)

const (
	// restartMin / restartMax bound the restart backoff. librespot exits on
	// its own for ordinary reasons — a session moved to another device, a
	// network blip, Spotify logging it out — so restarting is the normal
	// case and has to be quiet.
	restartMin = 3 * time.Second
	restartMax = time.Minute
)

// ErrNoBinary means librespot is not installed on this device.
//
// A named error rather than a log line, because the setting has to be able to
// SAY SO. A toggle that saves, reports success and plays nothing is the
// failure this codebase names most often, and "the binary was never pushed"
// is indistinguishable from "Spotify is broken" from the front of a
// dashboard.
var ErrNoBinary = errors.New("spotify: librespot is not installed on this device")

// MusicSink is the device's music plane, as much of it as this needs.
type MusicSink interface {
	PumpMusic(data []byte) error
	EndMusicStream()
	FlushMusic()
}

// PlaneOwner is the arbitration. Same shape as the Sendspin client's, and
// deliberately not a shared type: these are two producers that happen to need
// the same three verbs, and coupling them would make one's requirements the
// other's.
type PlaneOwner interface {
	Claim() bool
	Release()
	MayWrite() bool
}

// Options configure the endpoint.
type Options struct {
	// Name is what appears in the Spotify app. Defaults to the device id.
	Name string
	// Binary overrides BinaryPath, for tests.
	Binary string
	// CacheDir overrides CacheDir, for tests.
	CacheDir string
	// Bitrate is 96, 160 or 320. Spotify's own default is 160; 320 is
	// offered because the wire is a LAN and the constraint on this device is
	// CPU rather than bandwidth.
	Bitrate int
	// SourceRate is the rate the binary emits. Zero means librespot's own
	// 44,100. Present so a future librespot that CAN be asked for 48kHz
	// needs a config value rather than a code change — the same seam
	// internal/airplay has for AirPlay 2.
	SourceRate int
	// ExtraArgs are appended verbatim, for a device that needs something
	// this package does not model.
	ExtraArgs []string
}

// Client supervises one librespot process.
type Client struct {
	opts  Options
	sink  MusicSink
	plane PlaneOwner

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	// proc is the live process, held so a preemption can end it.
	proc *os.Process
}

// New wires a client. It starts nothing.
func New(opts Options, sink MusicSink, plane PlaneOwner) *Client {
	if opts.Binary == "" {
		opts.Binary = BinaryPath
	}
	if opts.CacheDir == "" {
		opts.CacheDir = CacheDir
	}
	if opts.Bitrate == 0 {
		opts.Bitrate = 160
	}
	if opts.SourceRate == 0 {
		opts.SourceRate = SourceRate
	}
	return &Client{opts: opts, sink: sink, plane: plane}
}

// Available reports whether librespot is installed, and why not when it is
// not. Both halves are needed: the dashboard has to disable the toggle AND
// say what would make it work.
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

// Start brings the endpoint up. Idempotent, because the controller re-sends
// the whole config on every reconnect.
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
	c.mu.Unlock()

	log.Printf("[spotify] enabled as %q", c.name())
	go c.supervise(ctx)
	return nil
}

// Stop ends the endpoint. Idempotent.
//
// Killing the process is what makes the Echo DISAPPEAR from the Spotify app,
// and that is the right outcome rather than an unfortunate side effect: a
// speaker that is listed, selected, and silent is worse than one that is not
// listed. Same rule as Sendspin's goodbye, reached by the only mechanism
// Spotify Connect offers from outside the client.
func (c *Client) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.kill()
	log.Println("[spotify] disabled")
}

// SetName changes the name shown in the Spotify app.
//
// IT RESTARTS A RUNNING SESSION, because the name is a command-line argument
// and librespot reads it once. Without the restart, renaming the device in
// Home Assistant would save, report success, and change nothing until the
// next reboot — a control that appears to work, which is the failure this
// codebase names most often.
//
// Guarded on the name actually changing, or the controller re-sending the
// whole config on every reconnect would kill the session on every reconnect.
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
		log.Printf("[spotify] renamed to %q — restarting the endpoint", name)
		// Killing the process is enough: the supervisor restarts it with
		// the new arguments after the backoff.
		c.kill()
	}
}

// Running reports whether the supervisor is up.
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Leave ends the current session because something else took the music plane.
//
// The supervisor stays up and will start librespot again, so the Echo comes
// back as a Spotify target once the plane is free. It does NOT resume what
// was playing: the session is gone, and reappearing as a speaker is not the
// same as taking over playback somebody moved elsewhere.
func (c *Client) Leave(reason string) {
	c.mu.Lock()
	proc := c.proc
	running := c.running
	c.mu.Unlock()
	if proc == nil || !running {
		return
	}
	log.Printf("[spotify] ending the session: %s", reason)
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

// args builds librespot's command line.
//
// Written out rather than assembled from a config struct, because every one
// of these is a decision:
//
//   - `--backend pipe` sends PCM to stdout. No ALSA in librespot at all: the
//     device already owns the speaker, and two things opening it is the #80
//     failure (a blocking open with no timeout, eighteen minutes of a
//     stranded device).
//   - THERE IS NO `--sample-rate`. It was designed in on the assumption that
//     librespot could resample for us; it cannot. No released version has the
//     option and neither does `dev` — the resampling pull request was never
//     merged. The pipe backend emits 44,100 frames a second, so the
//     conversion happens here, through internal/resample, exactly as it does
//     for AirPlay. Passing the flag anyway is not a silent fallback: librespot
//     rejects an unknown option and refuses to start.
//   - `--format S16` matches the plane. The default is also S16; naming it
//     means a librespot that changes its default does not silently start
//     sending 32-bit floats into a 16-bit mixer.
//   - `--disable-audio-cache` because the device has an 8GB eMMC shared with
//     Android and a cache of decoded audio is the fastest way to fill it. The
//     CREDENTIAL cache is separate and is kept — that is what stops the user
//     re-authorising after every reboot.
//   - `--disable-discovery` is deliberately NOT passed: zeroconf discovery is
//     how the speaker appears in the app without a login.
func (c *Client) args() []string {
	a := []string{
		"--name", c.name(),
		"--backend", "pipe",
		"--format", "S16",
		"--bitrate", fmt.Sprint(c.opts.Bitrate),
		"--cache", c.opts.CacheDir,
		"--disable-audio-cache",
	}
	return append(a, c.opts.ExtraArgs...)
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
			log.Printf("[spotify] librespot exited: %v", err)
		}
		// A process that ran for a while and then exited is the ordinary
		// case — a session moved to another device, a network blip — and
		// deserves a prompt restart. One that dies immediately is a
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

// session runs librespot once and pumps its output until it exits.
func (c *Client) session(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.opts.Binary, c.args()...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// librespot's own logging goes to stderr and is relayed at debug volume:
	// it is the only thing that says why an authentication failed, and a
	// device that will not appear in the app is otherwise undiagnosable.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	c.mu.Lock()
	c.proc = cmd.Process
	c.mu.Unlock()

	go relayLog(stderr)
	c.pump(stdout)

	err = cmd.Wait()
	c.mu.Lock()
	c.proc = nil
	c.mu.Unlock()

	// Whatever ended it, the plane goes back. Idempotent, and the thing the
	// mixer notices the absence of.
	c.sink.EndMusicStream()
	c.plane.Release()
	return err
}

// pump reads PCM from librespot and feeds the music plane.
//
// Returns when the pipe closes, which is when librespot exits.
func (c *Client) pump(r io.Reader) {
	br := bufio.NewReaderSize(r, readChunkFrames*bytesPerFrame)
	buf := make([]byte, readChunkFrames*bytesPerFrame)
	// One converter per SESSION: it carries filter history, and a fresh
	// instance mid-stream restarts from silence and clicks.
	conv := resample.NewStreamConverter(c.opts.SourceRate)
	claimed := false

	for {
		n, err := io.ReadFull(br, buf)
		if n > 0 {
			// The plane is claimed on the FIRST audio rather than at
			// process start: librespot runs continuously so it can appear
			// in the app, and it is silent until somebody selects it.
			// Claiming at start would take the plane from Home Assistant
			// for a speaker nobody is playing to.
			if !claimed {
				if !c.plane.Claim() {
					// Home Assistant holds it. The audio is dropped and
					// the session carries on: the user's phone shows the
					// Echo playing, which is wrong, and the alternative is
					// killing a session they may want back in ten seconds.
					// Bounded by HA releasing the plane, and the next
					// chunk claims again.
					continue
				}
				claimed = true
			}
			if !c.plane.MayWrite() {
				claimed = false
				continue
			}
			out := conv.Convert(buf[:n-n%bytesPerFrame], pcm.DownmixStereo)
			if len(out) > 0 {
				if perr := c.sink.PumpMusic(out); perr != nil {
					log.Printf("[spotify] PumpMusic: %v", perr)
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func relayLog(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		log.Printf("[librespot] %s", s.Text())
	}
}

// Report says whether Spotify Connect can run on this device, and why not
// when it cannot.
//
// It rides the register message next to ambient_light_status, and for the
// same reason that one exists: a capability list says WHAT the firmware can
// do, and when the answer to "why is this switch off" is a missing file,
// nobody can tell that from a broken feature without a shell session on the
// user's own hardware — which is exactly where #90 got stuck, twice.
//
// A package-level function rather than a method, because it is called at
// registration, before any Client exists and whether or not the setting is
// on.
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
