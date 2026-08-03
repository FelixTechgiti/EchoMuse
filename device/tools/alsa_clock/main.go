// alsa_clock — does this device's ALSA output report an honest playback
// position, and how steady is it?
//
// This is the question that decides whether sample-synchronised multi-room
// audio (Sendspin and anything like it) is achievable on an Echo Dot, and it
// cannot be answered from a header. `pcm_get_delay` and `pcm_get_htimestamp`
// are DECLARED by tinyalsa on every device; whether the MediaTek driver
// behind them reports a real DMA position or merely software accounting is a
// property of this hardware, discoverable only by running here.
//
// Synchronised playback needs to answer "when will the sample I am writing
// now actually leave the speaker?" to within a millisecond or so. That
// requires the driver to expose where the DMA engine currently is. A driver
// that instead returns bookkeeping — frames written minus frames it believes
// have elapsed — produces a plausible number that is useless for the purpose,
// and looks identical if you only glance at it.
//
// THE DISCRIMINATOR: poll the reported delay repeatedly WITHOUT writing.
//
//	Honest DMA position → delay falls smoothly between writes, tracking the
//	                      hardware draining the ring in real time.
//	Software accounting → delay is constant between writes and steps only
//	                      when a period is submitted, so it is quantised to
//	                      the period (42.7ms here) and useless below that.
//
// Everything else this prints is secondary to that one distinction.
//
// The probe reconstructs playback position as (framesWritten - delay), which
// on an honest driver is a straight line against CLOCK_MONOTONIC with a slope
// of the sample rate. The residual scatter around that line IS the achievable
// synchronisation floor: no scheme can align two devices better than each one
// can locate itself.
//
// Build:  ./tools/build_tools.sh        (adds alsa_clock alongside the others)
//
// RUN IT OVER USB/ADB ONLY. NOT over the controller's shell plane.
//
// The server owns card 0 device 23 and a second opener gets EBUSY, so it has
// to be stopped first — and that is the trap. `stop echomuse` kills the whole
// Android cgroup, and a shell opened through the controller is a CHILD OF THE
// SERVER, so the shell dies at `stop`, the probe never runs, and the `start`
// that would have restored the device never runs either. The Dot is then dead
// until someone power-cycles it, with nothing in any log to explain it — the
// service was stopped cleanly, so the supervisor has nothing to report. This
// is the same cgroup behaviour that made the OTA use `kill $PPID` instead of
// `stop`/`start` (v2.4.4), and the same shape as the 2026-08-01 device that
// never came back.
//
// adbd is independent of the server, so an adb shell survives the stop:
//
//	adb shell "su -c 'stop <service>; /data/local/tmp/alsa_clock; start <service>'"
//
// Read the service name from the device rather than assuming it — it differs
// across builds:  getprop | grep init.svc
//
// `kill`-ing the server instead of stopping it does NOT help: start_server.sh
// restarts it about 3s later and takes the PCM back, and three of those in a
// row is a fast-exit auto-rollback to the other slot.
//
// For a REMOTE measurement, do not use this tool — instrument the real
// speaker path in the firmware and ship it by OTA, which is the transport
// built for changing what a device runs and has auto-rollback behind it.
//
// It writes silence, so it is inaudible beyond the amp clicking on.
package main

/*
#cgo LDFLAGS: -ldl -ltinyalsa
#include <stdlib.h>
#include <time.h>
#include <tinyalsa/asoundlib.h>

// Wrapped rather than called through cgo directly: struct timespec's layout
// differs across ABIs and this is a 32-bit ARM target, so the conversion to a
// single int64 belongs on the C side where the struct is native.
static long long mono_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (long long)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

// Returns 0 on success. tstamp is flattened to ns in the monotonic domain the
// driver reports it in — which is itself worth checking against mono_ns().
static int ht(struct pcm *p, unsigned int *avail, long long *tstamp_ns) {
    struct timespec ts;
    ts.tv_sec = 0; ts.tv_nsec = 0;
    int rc = pcm_get_htimestamp(p, avail, &ts);
    *tstamp_ns = (long long)ts.tv_sec * 1000000000LL + ts.tv_nsec;
    return rc;
}
*/
import "C"

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"unsafe"
)

const (
	card        = 0
	device      = 23
	channels    = 2
	sampleRate  = 48000
	periodSize  = 2048 // frames — matches internal/bindings/speaker
	periodCount = 4
	bytesFrame  = channels * 2 // S16_LE stereo
)

type sample struct {
	monoNs     int64
	written    int64 // frames handed to ALSA so far
	delay      int64 // frames the driver says are still queued
	htAvail    uint32
	htNs       int64
	htRC       int
	afterWrite bool // taken immediately after a period was submitted
}

func main() {
	seconds := flag.Int("seconds", 12, "how long to observe")
	pollMs := flag.Int("poll", 4, "delay-poll interval in ms (must be well under the 42.7ms period)")
	verbose := flag.Bool("v", false, "dump every sample")
	flag.Parse()

	fmt.Printf("alsa_clock — card %d device %d, %dch %dHz, period %d frames (%.1fms), %d periods\n",
		card, device, channels, sampleRate, periodSize,
		1000.0*periodSize/sampleRate, periodCount)

	cfg := C.struct_pcm_config{}
	cfg.channels = C.uint(channels)
	cfg.rate = C.uint(sampleRate)
	cfg.period_size = C.uint(periodSize)
	cfg.period_count = C.uint(periodCount)
	cfg.format = C.PCM_FORMAT_S16_LE
	// These three are unsigned long, not unsigned int, unlike the fields
	// above — the struct mixes widths and 32-bit ARM will not convert silently.
	cfg.start_threshold = C.ulong(periodSize)
	cfg.stop_threshold = C.ulong(periodSize * periodCount)
	cfg.silence_threshold = 0

	p := C.pcm_open(C.uint(card), C.uint(device), C.PCM_OUT, &cfg)
	if p == nil {
		fatal("pcm_open returned nil — is the server still running? (stop echomuse)")
	}
	defer C.pcm_close(p)
	if C.pcm_is_ready(p) == 0 {
		fatal("pcm not ready: %s", C.GoString(C.pcm_get_error(p)))
	}

	// Silence, so this is inaudible. One period's worth, reused.
	buf := make([]byte, periodSize*bytesFrame)
	bufPtr := unsafe.Pointer(&buf[0])

	// Prime the ring so playback is actually running before we measure. A
	// delay reading taken before the stream starts describes nothing.
	var written int64
	for i := 0; i < periodCount; i++ {
		if rc := C.pcm_write(p, bufPtr, C.uint(len(buf))); rc != 0 {
			fatal("pcm_write during prime: %s", C.GoString(C.pcm_get_error(p)))
		}
		written += periodSize
	}

	totalPolls := (*seconds * 1000) / *pollMs
	samples := make([]sample, 0, totalPolls)
	start := int64(C.mono_ns())
	deadline := start + int64(*seconds)*1e9

	// The loop deliberately does NOT sleep-then-write in lockstep. It polls on
	// a fast timer and writes only when the ring has room, so that most
	// samples fall BETWEEN writes — which is the only place the honest and
	// dishonest cases look different.
	for int64(C.mono_ns()) < deadline {
		var avail C.uint
		var htNs C.longlong
		rc := int(C.ht(p, &avail, &htNs))
		delay := int64(C.pcm_get_delay(p))
		now := int64(C.mono_ns())

		wrote := false
		// Room for another period? pcm_get_delay is in frames; the ring holds
		// periodSize*periodCount.
		if delay >= 0 && delay <= int64(periodSize*(periodCount-1)) {
			if r := C.pcm_write(p, bufPtr, C.uint(len(buf))); r == 0 {
				written += periodSize
				wrote = true
			}
		}

		samples = append(samples, sample{
			monoNs: now, written: written, delay: delay,
			htAvail: uint32(avail), htNs: int64(htNs), htRC: rc,
			afterWrite: wrote,
		})

		sleepNs(int64(*pollMs) * 1e6)
	}

	report(samples, *verbose)
}

func sleepNs(ns int64) {
	ts := C.struct_timespec{tv_sec: C.long(ns / 1e9), tv_nsec: C.long(ns % 1e9)}
	C.nanosleep(&ts, nil)
}

func report(s []sample, verbose bool) {
	if len(s) < 20 {
		fatal("only %d samples — too few to conclude anything", len(s))
	}
	fmt.Printf("\n%d samples over %.1fs\n\n", len(s),
		float64(s[len(s)-1].monoNs-s[0].monoNs)/1e9)

	// ── 1. Does pcm_get_delay work at all? ──────────────────────────────────
	bad := 0
	for _, x := range s {
		if x.delay < 0 {
			bad++
		}
	}
	if bad == len(s) {
		fmt.Println("pcm_get_delay: FAILS on every call — no position information at all.")
		fmt.Println("\nVERDICT: sample-synchronised playback is NOT achievable on this path.")
		return
	}
	if bad > 0 {
		fmt.Printf("pcm_get_delay: %d/%d calls failed\n", bad, len(s))
	}

	// ── 2. THE DISCRIMINATOR ────────────────────────────────────────────────
	// Between two consecutive polls with no write in between, an honest DMA
	// position must fall by roughly the elapsed time. Software accounting
	// holds it constant until the next period is submitted.
	var moved, still int
	var driftErr []float64
	for i := 1; i < len(s); i++ {
		if s[i].afterWrite || s[i-1].afterWrite || s[i].delay < 0 || s[i-1].delay < 0 {
			continue
		}
		if s[i].written != s[i-1].written {
			continue // a write landed between them
		}
		dtNs := s[i].monoNs - s[i-1].monoNs
		if dtNs <= 0 {
			continue
		}
		dDelay := s[i-1].delay - s[i].delay // frames drained
		if dDelay == 0 {
			still++
			continue
		}
		moved++
		expected := float64(dtNs) * sampleRate / 1e9
		driftErr = append(driftErr, float64(dDelay)-expected)
	}
	interPoll := moved + still
	fmt.Printf("Between-write behaviour (%d intervals with no write):\n", interPoll)
	if interPoll == 0 {
		fmt.Println("  inconclusive — every poll coincided with a write; lower -poll")
	} else {
		pct := 100.0 * float64(moved) / float64(interPoll)
		fmt.Printf("  delay moved: %d (%.1f%%)   delay frozen: %d (%.1f%%)\n",
			moved, pct, still, 100.0-pct)
		if pct < 20 {
			fmt.Println("  → looks like SOFTWARE ACCOUNTING: the position only changes when")
			fmt.Println("    a period is submitted, so it is quantised to the period.")
		} else if pct > 70 {
			fmt.Println("  → looks like a REAL DMA POSITION: it advances continuously with")
			fmt.Println("    wall time, independently of writes.")
		} else {
			fmt.Println("  → ambiguous; treat the residual figure below as the real answer.")
		}
	}

	// ── 3. Granularity ──────────────────────────────────────────────────────
	uniq := map[int64]int{}
	for _, x := range s {
		if x.delay >= 0 {
			uniq[x.delay]++
		}
	}
	onPeriod := 0
	for v := range uniq {
		if v%periodSize == 0 {
			onPeriod++
		}
	}
	fmt.Printf("\nGranularity: %d distinct delay values", len(uniq))
	if len(uniq) > 0 {
		fmt.Printf(", %d of them exact period multiples\n", onPeriod)
		if len(uniq) <= periodCount+2 {
			fmt.Printf("  → only ~%d values: position is period-quantised (%.1fms steps).\n",
				len(uniq), 1000.0*periodSize/sampleRate)
		}
	} else {
		fmt.Println()
	}

	// ── 4. The number that matters: residual scatter ────────────────────────
	// position = written - delay should be linear in time at exactly the
	// sample rate. Fit it and measure how far reality departs.
	var n, sx, sy, sxx, sxy float64
	for _, x := range s {
		if x.delay < 0 {
			continue
		}
		t := float64(x.monoNs-s[0].monoNs) / 1e9
		pos := float64(x.written - x.delay)
		n++
		sx += t
		sy += pos
		sxx += t * t
		sxy += t * pos
	}
	slope := (n*sxy - sx*sy) / (n*sxx - sx*sx)
	intercept := (sy - slope*sx) / n

	var resid []float64
	for _, x := range s {
		if x.delay < 0 {
			continue
		}
		t := float64(x.monoNs-s[0].monoNs) / 1e9
		pos := float64(x.written - x.delay)
		resid = append(resid, (pos-(slope*t+intercept))/sampleRate*1000.0) // ms
	}
	sort.Float64s(resid)
	var mean, sd float64
	for _, r := range resid {
		mean += r
	}
	mean /= float64(len(resid))
	for _, r := range resid {
		sd += (r - mean) * (r - mean)
	}
	sd = math.Sqrt(sd / float64(len(resid)))

	ppm := (slope - sampleRate) / sampleRate * 1e6
	fmt.Printf("\nReconstructed playback clock:\n")
	fmt.Printf("  measured rate : %.2f Hz  (nominal %d, offset %+.0f ppm)\n", slope, sampleRate, ppm)
	fmt.Printf("  residual σ    : %.3f ms\n", sd)
	fmt.Printf("  residual p95  : %.3f ms   p99: %.3f ms   max|.|: %.3f ms\n",
		resid[len(resid)*95/100], resid[len(resid)*99/100],
		math.Max(math.Abs(resid[0]), math.Abs(resid[len(resid)-1])))

	// ── 5. pcm_get_htimestamp ───────────────────────────────────────────────
	htOK, htMono := 0, 0
	for i, x := range s {
		if x.htRC == 0 {
			htOK++
			if x.htNs > 0 && i > 0 && x.htNs >= s[i-1].htNs {
				htMono++
			}
		}
	}
	fmt.Printf("\npcm_get_htimestamp: %d/%d succeeded", htOK, len(s))
	if htOK > 0 {
		fmt.Printf(", %d monotonic and non-zero\n", htMono)
		if htMono < htOK/2 {
			fmt.Println("  → returns success but no usable timestamp; rely on pcm_get_delay.")
		}
	} else {
		fmt.Println("\n  → unavailable; rely on pcm_get_delay.")
	}

	// ── 6. Verdict ──────────────────────────────────────────────────────────
	// Thresholds are about what a listener notices, not what looks tidy.
	// Same-room needs ~<5ms before comb filtering is audible; room-to-room
	// tolerates ~20ms (Haas). The residual is a floor, not the achieved
	// result — a real implementation adds network and drift correction on top.
	fmt.Printf("\nVERDICT: ")
	switch {
	case sd <= 1.0:
		fmt.Printf("position is reported to sub-millisecond precision (σ=%.2fms).\n", sd)
		fmt.Println("Sample-synchronised multi-room is achievable on this hardware;")
		fmt.Println("the remaining work is clock discipline and drift correction, not")
		fmt.Println("a hardware limit.")
	case sd <= 5.0:
		fmt.Printf("position is good to a few milliseconds (σ=%.2fms).\n", sd)
		fmt.Println("Room-to-room sync is comfortable. Two Dots in the SAME room may")
		fmt.Println("comb-filter audibly; worth a listening test before promising it.")
	case sd <= 25.0:
		fmt.Printf("position is coarse (σ=%.2fms) — about period granularity.\n", sd)
		fmt.Println("Room-to-room is probably acceptable; same-room is not. Getting")
		fmt.Println("below this would need the driver to expose a real DMA pointer.")
	default:
		fmt.Printf("position is too coarse to synchronise against (σ=%.2fms).\n", sd)
		fmt.Println("Sample-accurate multi-room is NOT achievable on this path as-is.")
	}

	if verbose {
		fmt.Println("\n  t_ms  written    delay  ht_rc  ht_avail  wrote")
		for _, x := range s {
			fmt.Printf("%7.1f %8d %8d %6d %9d  %v\n",
				float64(x.monoNs-s[0].monoNs)/1e6, x.written, x.delay,
				x.htRC, x.htAvail, x.afterWrite)
		}
	}
}

func fatal(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "alsa_clock: "+f+"\n", a...)
	os.Exit(1)
}
