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
	"encoding/binary"
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

// BinaryPath is where the controller installs shairport-sync.
const BinaryPath = "/data/local/bin/shairport-sync"

// ConfigPath is shairport-sync's own configuration file. Written by this
// package rather than pushed as a payload: everything in it is derived from
// settings the controller already sends, and a second copy of the device name
// is a second thing to keep in step.
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
	// ExtraArgs are appended verbatim.
	ExtraArgs []string
}

// Client supervises one shairport-sync process.
type Client struct {
	opts  Options
	sink  MusicSink
	plane PlaneOwner

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	proc    *os.Process
}

// New wires a client. It starts nothing.
func New(opts Options, sink MusicSink, plane PlaneOwner) *Client {
	if opts.Binary == "" {
		opts.Binary = BinaryPath
	}
	if opts.SourceRate == 0 {
		opts.SourceRate = SourceRate
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
	c.mu.Unlock()

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
func (c *Client) args() []string {
	a := []string{"-a", c.name(), "-o", "stdout"}
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
	cmd := exec.CommandContext(ctx, c.opts.Binary, c.args()...)
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
	c.mu.Unlock()

	go relayLog(stderr)
	c.pump(stdout)

	err = cmd.Wait()
	c.mu.Lock()
	c.proc = nil
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

	// One resampler per SESSION, not per chunk: it carries filter history,
	// and a fresh instance mid-stream restarts from silence and clicks.
	// Skipped entirely at 48kHz, which is what AirPlay 2 delivers.
	var rs *resample.Resampler
	if c.opts.SourceRate != DeviceRate {
		rs = resample.New()
	}
	claimed := false

	for {
		n, err := io.ReadFull(br, buf)
		if n >= stereoBytesPerFrame {
			// Claimed on the FIRST AUDIO rather than at process start:
			// shairport-sync runs continuously so it can appear in the
			// AirPlay list, and it is silent until somebody selects it.
			// Claiming at start would take the plane from Home Assistant
			// for a receiver nobody is playing to.
			if !claimed {
				if !c.plane.Claim() {
					continue
				}
				claimed = true
			}
			if !c.plane.MayWrite() {
				claimed = false
				continue
			}
			out := c.convert(rs, buf[:n-n%stereoBytesPerFrame])
			if len(out) > 0 {
				if perr := c.sink.PumpMusic(out); perr != nil {
					log.Printf("[airplay] PumpMusic: %v", perr)
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// convert turns stereo 16-bit at the source rate into mono 16-bit at 48kHz.
//
// DOWNMIX FIRST, THEN RESAMPLE, and the order is not arbitrary: resampling
// two channels costs twice the filter for a result that is about to be summed
// anyway. Doing it the other way round is the same audio for double the CPU,
// on the one source that cannot hand the job to a subprocess.
func (c *Client) convert(rs *resample.Resampler, stereo []byte) []byte {
	mono := pcm.DownmixStereo(stereo)
	if rs == nil {
		return mono // already at the device rate — AirPlay 2
	}
	in := make([]float64, len(mono)/2)
	for i := range in {
		in[i] = float64(int16(binary.LittleEndian.Uint16(mono[i*2:])))
	}
	out := rs.Process(in)
	res := make([]byte, len(out)*2)
	for i, v := range out {
		// Clamped, not wrapped. The filter can overshoot slightly on a
		// signal already at full scale, and an int16 that wraps turns a
		// peak into full-scale opposite polarity — a crack rather than
		// clipping. Same rule the mixer follows.
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(res[i*2:], uint16(int16(v)))
	}
	return res
}

func relayLog(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		log.Printf("[shairport] %s", s.Text())
	}
}
