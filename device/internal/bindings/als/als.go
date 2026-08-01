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
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// driverName is the sysfs `name` of the sensor that has a usable interface.
const driverName = "tsl2540"

var (
	once sync.Once
	path string // absolute path to als_lux; empty when there is no sensor
)

// resolve finds the sensor BY NAME, not by i2c address.
//
// 0-0039 is where it sits on every device measured, but an address is an
// enumeration accident: hardcoding it would work here and silently read
// nothing on a device that enumerated differently. Same reasoning as
// resolving thermal zones by type rather than by index.
func resolve() string {
	once.Do(func() {
		names, err := filepath.Glob("/sys/bus/i2c/devices/*/name")
		if err != nil {
			return
		}
		for _, n := range names {
			b, err := os.ReadFile(n)
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(b)) != driverName {
				continue
			}
			p := filepath.Join(filepath.Dir(n), "als_lux")
			// A matching name is not enough — the 2584 on this same bus has
			// a name and no readable attributes at all.
			if _, err := os.Stat(p); err != nil {
				continue
			}
			path = p
			log.Printf("[als] ambient light sensor at %s", path)
			return
		}
		log.Printf("[als] no ambient light sensor found")
	})
	return path
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
