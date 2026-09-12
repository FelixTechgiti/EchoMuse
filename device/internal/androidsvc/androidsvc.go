// Package androidsvc stops Amazon's own services, and does nothing at all on a
// base that has none.
//
// # Why this is a package rather than six call sites
//
// The firmware takes hardware away from Android on the way up: `stop mixer`
// before opening the mic, `stop media` before the speaker, `stop ledcontroller`
// before the ring, `stop acebutton` before the buttons, `stop smarthomewifid`
// at startup. Every one of those is a request to Amazon's init, made through a
// property it sets — and under emOS there is no Android init, no property
// service, and nothing holding the hardware in the first place.
//
// **One of them could have stopped the firmware from starting at all.**
// `buttons.NewButtonController` returns whatever `stop acebutton` returns and
// `cmd/server.go` calls `log.Fatalf` on it. Whether that is fatal under emOS
// rests on what Amazon's toolbox `stop` does when `property_set` has no socket
// to write to — it happens to ignore the failure and exit 0, so the device
// boots. That is a load-bearing assumption about a vendor binary's exit code,
// read out of somebody else's source, on the path that decides whether an
// Echo comes up. Gating removes the question instead of answering it.
//
// The rest is quieter and still worth removing: two wasted fork/execs per
// start per site, and `internal/bindings/mic` logs its failure, so an emOS
// device would print a line about a service that does not exist on every
// boot.
//
// # What it is NOT
//
// It is not a general Android shim, and it should not grow into one. The
// project's direction (root CLAUDE.md) is to prefer the Linux interface and
// ISOLATE the Android one where it is unavoidable; this isolates the single
// largest group of those call sites — ten of the roughly twenty — and the
// others stay where they are with their own reasons written beside them.
package androidsvc

import (
	"log"
	"os/exec"

	"github.com/wilbowes/EchoMuse/internal/platform"
)

// Runner executes one `stop` invocation. Injected so the decision is testable
// from a host that is neither platform.
type Runner func(service string) error

// Base reports which userspace the firmware booted. Injected for the same
// reason as Runner.
type Base func() string

// Stop asks Android's init to stop one of its services.
//
// Reports whether anything was attempted, so a caller can tell "stopped" from
// "there was nothing to stop" without inspecting the platform itself. An
// error from the command is returned as-is: what to do about it belongs to
// the caller, which is the half that knows whether the hardware it wants is
// now free.
func Stop(service string) (attempted bool, err error) {
	return stop(platform.Base, execStop, service)
}

// execStop is the real runner. `stop` is an Android toolbox applet that sets
// the `ctl.stop` property; it lives at /system/bin, which is on PATH under
// both bases, so the name is left bare exactly as the other Android call
// sites in this tree leave `tinymix` and `getprop`.
func execStop(service string) error {
	return exec.Command("stop", service).Run()
}

// stop is the pure half.
//
// **Unknown counts as Android**, matching every other reader of this value:
// firmware that cannot tell must behave as the existing fleet does, and the
// cost of being wrong in that direction is two failed execs rather than
// hardware nobody asked Android to release.
func stop(base Base, run Runner, service string) (bool, error) {
	if base() == platform.EmOS {
		return false, nil
	}
	return true, run(service)
}

// StopQuietly is Stop for the callers that have nothing to do about a
// failure, and is the shape most of them want: Amazon's toolbox `stop` exits
// 0 whether or not the service existed, so a non-nil error here means the
// command could not be RUN, which on FireOS is already strange enough to say
// once.
func StopQuietly(service string) {
	attempted, err := Stop(service)
	if attempted && err != nil {
		log.Printf("[androidsvc] stop %s: %v (continuing)", service, err)
	}
}
