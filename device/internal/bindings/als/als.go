// Package als reads the Echo Dot's ambient light sensor.
//
// The Dot carries an ams TSL2540 on i2c, which Amazon's Android layer does
// not expose AT ALL: `dumpsys sensorservice` reports an empty sensor list,
// there is nothing under /sys/class/sensors or /sys/bus/iio, and no ALS input
// device. It is visible only on the raw i2c bus — the same shape as the mute
// LED sitting on a different GPIO than the vendor HAL believed.
//
// Verified on hardware: covering the sensor by hand takes it from 309 lux to
// 0 and back to 308 within a second, with both raw channels tracking.
//
// There is a SECOND ALS on the bus (tsl2584tsv at 0x29) whose sysfs directory
// carries nothing beyond name/modalias — enumerated but not driven, so it is
// unusable without writing a driver. The 2540 is the one with a real
// interface, hence matching that name specifically rather than "tsl".
//
// This is its own package because two callers need it at different moments:
// the register message needs to know whether the sensor EXISTS, to declare
// the capability; the stats tick needs to READ it.
package als

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// driverName is the sysfs `name` of the sensor that has a usable interface.
const driverName = "tsl2540"

// RetryInterval bounds how often an unresolved sensor is looked for again.
// The scan is a glob plus a handful of small sysfs reads, so this is about
// not doing it every second for the life of a device that genuinely has no
// sensor, not about the cost of any single scan.
const RetryInterval = 30 * time.Second

var (
	mu       sync.Mutex
	path     string    // absolute path to als_lux; empty when unresolved
	lastScan time.Time // when we last looked, for the retry interval
	reported bool      // absence logged once, not every retry
)

// resolve finds the sensor BY NAME, not by i2c address.
//
// 0-0039 is where it sits on every device measured, but an address is an
// enumeration accident: hardcoding it would work here and silently read
// nothing on a device that enumerated differently. Same reasoning as
// resolving thermal zones by type rather than by index.
//
// A NEGATIVE RESULT IS NOT CACHED. This used to be a sync.Once, which meant
// one failed lookup disabled the sensor for the entire life of the process —
// and the first lookup happens at registration, moments after a cold boot,
// which is precisely when sysfs is least likely to be complete. The device
// then reported no `ambient_light` capability until something restarted it,
// with nothing in the log to say the answer had been frozen. Absence must
// stay re-checkable; a device that gains the sensor picks it up on the next
// scan and declares the capability at its next registration.
func resolve() string {
	mu.Lock()
	defer mu.Unlock()
	if path != "" {
		return path
	}
	if !lastScan.IsZero() && time.Since(lastScan) < RetryInterval {
		return ""
	}
	lastScan = time.Now()

	names, err := filepath.Glob("/sys/bus/i2c/devices/*/name")
	if err != nil {
		return ""
	}
	// Record what IS on the bus, so an absence can be diagnosed remotely
	// from a log line instead of a round trip asking someone to run i2c
	// commands on a device they just flashed.
	seen := make([]string, 0, len(names))
	nameMatched := false
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			continue
		}
		got := strings.TrimSpace(string(b))
		seen = append(seen, got)
		if got != driverName {
			continue
		}
		nameMatched = true
		p := filepath.Join(filepath.Dir(n), "als_lux")
		// A matching name is not enough — the 2584 on this same bus has
		// a name and no readable attributes at all.
		if _, err := os.Stat(p); err != nil {
			continue
		}
		path = p
		log.Printf("[als] ambient light sensor at %s", path)
		return path
	}
	if !reported {
		reported = true
		// The two failures need opposite fixes and look identical from the
		// controller, so name which one it is: no chip on the bus at all,
		// versus the chip present with no driver attribute bound to it.
		if nameMatched {
			log.Printf("[als] %s found but no als_lux attribute — driver not bound; "+
				"ambient light unavailable", driverName)
		} else {
			log.Printf("[als] no %s on i2c (saw: %s) — ambient light unavailable, "+
				"rechecking every %s", driverName, strings.Join(seen, ","), RetryInterval)
		}
	}
	return ""
}

// Present reports whether this device has a readable ambient light sensor.
//
// Used to decide whether to declare the capability at registration, so the
// controller never advertises an HA entity that could not produce a reading.
func Present() bool { return resolve() != "" }

// Lux returns the ambient light level, or nil when there is no sensor or the
// read fails.
//
// nil, never 0: a covered sensor reads a genuine 0 lux, so reporting 0 for
// "absent" would make a dark room and a device without the hardware
// indistinguishable — the same NULL-not-zero rule the playback and shadow
// counters follow.
func Lux() *int {
	p := resolve()
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return nil
	}
	return &n
}

// ── Change watching ──────────────────────────────────────────────────────────
//
// The stats tick reports lux every ~30s, which is fine for a baseline and
// useless for "someone turned a light on": up to 30s late, and a brief change
// can fall entirely between samples.
//
// So the same split shadow mode uses for wake-word crossings — a significant
// change goes IMMEDIATELY because its whole value is the timing, and the
// steady state rides the existing summary. Nothing per-frame either way.
const (
	// PollInterval bounds how fast a change can be noticed. Reading faster
	// than the chip's 346ms integration time buys nothing but syscalls.
	PollInterval = time.Second

	// MinRatio is how much the level must change, RELATIVE to the level it
	// changed from. An absolute threshold cannot work across the range: 50
	// lux is a transformation in a dark room and invisible in daylight, and
	// perceived brightness is roughly logarithmic anyway.
	//
	// Measured noise on a still room is about ±1.5% (309/311/313/308/312 on
	// consecutive reads), so 25% is far clear of it while still catching a
	// lamp being switched on.
	MinRatio = 0.25

	// MinAbsolute stops near-darkness generating infinite ratios — 0 -> 2 lux
	// is a 2x change and not a room lighting up.
	MinAbsolute = 10

	// MinInterval keeps a flickering or dimming light from flooding the
	// control plane. A real lighting change is a step, not a stream.
	MinInterval = 2 * time.Second
)

// Significant reports whether `now` differs enough from `baseline` to be worth
// telling anyone about. Pure, so the threshold policy is testable without a
// sensor.
func Significant(baseline, now int) bool {
	d := now - baseline
	if d < 0 {
		d = -d
	}
	if d < MinAbsolute {
		return false
	}
	ref := baseline
	if ref < MinAbsolute {
		ref = MinAbsolute // near zero, compare against the floor not against 0
	}
	return float64(d)/float64(ref) >= MinRatio
}

// Watch polls the sensor and calls onChange when the level moves significantly.
//
// onChange is called from this goroutine and must not block: it is a control
// -plane send, and a stalled send must not wedge the watcher into reporting a
// stale baseline forever.
func Watch(ctx context.Context, onChange func(lux int)) {
	if !Present() {
		return
	}
	baseline := -1
	last := time.Time{}
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p := Lux()
			if p == nil {
				continue
			}
			v := *p
			if baseline < 0 {
				baseline = v // first reading seeds, never reports
				continue
			}
			if !Significant(baseline, v) {
				continue
			}
			if time.Since(last) < MinInterval {
				continue
			}
			baseline, last = v, time.Now()
			onChange(v)
		}
	}
}
