package mcast

import (
	"log"
	"time"
)

// Defaults for the watcher. Deliberately unhurried: the fault this repairs
// lasts until something restarts the endpoints, so minutes of it are an
// annoyance and a tight loop against it would be a fault of its own.
const (
	// DefaultInterval is how often the membership is read. One small procfs
	// read, so the cost is the wake-up rather than the work.
	DefaultInterval = 30 * time.Second

	// DefaultMisses is how many consecutive reads must agree before anything
	// is restarted. A re-association is exactly when the membership is
	// legitimately absent for a moment AND exactly when the endpoints are
	// least able to do anything useful, so acting on the first read would
	// restart them into a radio that has not settled.
	DefaultMisses = 2

	// DefaultBackoff / MaxBackoff bound the repair. A rejoin that does not
	// take means restarting two subprocesses every interval for the life of
	// the process, on a board sharing 512MB with Android — the same shape as
	// the speaker's `stop media` nudge, which was budgeted only after it
	// started running in a loop.
	DefaultBackoff = 2 * time.Minute
	MaxBackoff     = 30 * time.Minute
)

// Watcher re-joins the mDNS group by restarting whatever holds it.
//
// It does not open a socket of its own. Joining from here would put the
// membership on a socket the responders do not own — the group would read as
// present in /proc/net/igmp, this watcher would fall silent, and librespot and
// shairport-sync would still never see a query. That is a worse outcome than
// the bug: it removes the symptom and the instrument together while leaving
// the device exactly as invisible.
type Watcher struct {
	// Read returns the contents of /proc/net/igmp.
	Read func() (string, error)
	// Active reports whether anything that should be a member is running.
	// Without it, a device with both endpoints switched off looks permanently
	// broken and gets restarted for ever — the group is correctly absent when
	// nobody has joined it.
	Active func() bool
	// Rejoin restarts the endpoints. Called at most once per backoff window.
	Rejoin func()

	Iface    string
	Group    string
	Misses   int
	Backoff  time.Duration
	MaxDelay time.Duration

	misses    int
	nextAllow time.Time
	delay     time.Duration
	repairs   int
}

func (w *Watcher) defaults() {
	if w.Iface == "" {
		w.Iface = "wlan0"
	}
	if w.Group == "" {
		w.Group = Group
	}
	if w.Misses <= 0 {
		w.Misses = DefaultMisses
	}
	if w.Backoff <= 0 {
		w.Backoff = DefaultBackoff
	}
	if w.MaxDelay <= 0 {
		w.MaxDelay = MaxBackoff
	}
}

// Tick performs one check and reports whether it rejoined. Takes the clock so
// the backoff is testable without waiting it out.
func (w *Watcher) Tick(now time.Time) bool {
	w.defaults()
	if w.Read == nil || w.Rejoin == nil {
		return false
	}
	if w.Active != nil && !w.Active() {
		// Nothing should be a member, so absence is correct. Reset, or the
		// misses counted while the endpoints were off would fire the instant
		// somebody switched one on.
		w.misses = 0
		return false
	}
	proc, err := w.Read()
	if err != nil {
		// Failure to look is not evidence of absence. Same rule the wake-word
		// reconcile follows: only a successful read that lacks the group
		// counts, or a procfs that moved would restart the endpoints for ever.
		return false
	}
	if Joined(proc, w.Iface, w.Group) {
		if w.misses > 0 {
			w.misses = 0
		}
		// A window that has held is what earns the backoff back. Reset only
		// here, so a device flapping every few minutes does not get the full
		// cadence again on each brief recovery.
		if w.delay > 0 && now.After(w.nextAllow) {
			w.delay = 0
		}
		return false
	}
	w.misses++
	if w.misses < w.Misses {
		return false
	}
	if now.Before(w.nextAllow) {
		return false
	}
	w.misses = 0
	w.repairs++
	if w.delay == 0 {
		w.delay = w.Backoff
	} else if w.delay < w.MaxDelay {
		w.delay *= 2
		if w.delay > w.MaxDelay {
			w.delay = w.MaxDelay
		}
	}
	w.nextAllow = now.Add(w.delay)
	// Relayed to the controller: "could not" and "failed" are what the log
	// relay forwards, and this line has to reach somebody who is looking at a
	// device that is silently absent from every picker. It is also the only
	// record of HOW OFTEN the membership is being lost, which is the
	// measurement the unknown trigger needs.
	log.Printf("[mcast] %s is not joined to %s — the endpoints are running "+
		"but no mDNS query can reach them, so this device is invisible on "+
		"the network. Restarting them to re-join (repair #%d, next no sooner "+
		"than %s)", w.Iface, w.Group, w.repairs, w.delay)
	w.Rejoin()
	return true
}

// Repairs is how many times this watcher has acted. Rides the stats report so
// a device that is flapping can be told from one that has been steady.
func (w *Watcher) Repairs() int { return w.repairs }

// Run ticks until ctx is done. Split from Tick so the whole decision is
// host-testable and only the ticker is not.
func (w *Watcher) Run(done <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			w.Tick(now)
		}
	}
}
