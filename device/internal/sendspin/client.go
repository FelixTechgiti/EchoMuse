package sendspin

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// The supervisor: discovery, dialling, and staying connected.
//
// Everything below this is a state machine that assumes it has a working
// socket. This is the part that gets one and gets another when it goes away,
// on a fleet whose links are measured at 4.6–7.1% packet loss — so
// reconnecting is the normal case, not the exceptional one, and it must be
// quiet. A supervisor that logs a stack trace every four seconds is one whose
// logs cannot be read when something is genuinely wrong.

const (
	// reconnectMin / reconnectMax bound the backoff. The floor is short
	// because a Music Assistant restart is the common cause and the group
	// is waiting; the ceiling is long because a server that is simply not
	// installed must not have this device browsing forever.
	reconnectMin = 4 * time.Second
	reconnectMax = 2 * time.Minute

	// discoverTimeout bounds one browse round.
	discoverTimeout = 10 * time.Second
)

// Client runs a Sendspin session for as long as it is enabled.
//
// Start/Stop are driven by the config push, so both are idempotent and safe
// from any goroutine: a controller re-sending the whole config on every
// reconnect calls Start over and over on a client that is already running.
type Client struct {
	opts   Options
	iface  string
	sink   MusicSink
	plane  PlaneOwner
	policy CorrectionPolicy

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	// conn is the live connection, held only so a preemption can send a
	// goodbye on it. Nil between sessions.
	conn *Conn
}

// NewClient wires one. It does not connect until Start.
func NewClient(opts Options, iface string, sink MusicSink, plane PlaneOwner) *Client {
	return &Client{opts: opts, iface: iface, sink: sink, plane: plane,
		policy: DefaultPolicy}
}

// Start begins discovering and connecting. Idempotent.
func (c *Client) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()

	log.Println("[sendspin] enabled — looking for a server")
	go c.supervise(ctx)
}

// Stop ends the session and the loop. Idempotent, and it says goodbye:
// stopping without one leaves the server streaming to a client that has gone
// quiet and the group's view of this device wrong until its own timeout.
func (c *Client) Stop(reason string) {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	cancel := c.cancel
	conn := c.conn
	c.cancel = nil
	c.mu.Unlock()

	if conn != nil {
		if err := conn.Goodbye(reason); err != nil {
			log.Printf("[sendspin] goodbye: %v", err)
		}
	}
	if cancel != nil {
		cancel()
	}
	log.Println("[sendspin] disabled")
}

// Running reports whether the supervisor is up. For the stats report — a
// device that believes it is connected and a server that has never heard of
// it is the state nobody can diagnose from the outside.
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Leave ends the current session because something else took the music
// plane. This is the callback the arbiter holds, and it is the whole of
// "leaving is not ignoring": the connection is dropped so the server stops
// streaming and the group stops counting this device as a member.
//
// It does NOT stop the supervisor. The plane may come back — Home Assistant's
// track ends — and the backoff loop will reconnect. What it must not do is
// rejoin the group by itself; that is the server's decision once this client
// reappears, and a client that reconnects is not a client that rejoins.
func (c *Client) Leave(reason string) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	log.Printf("[sendspin] leaving the group: %s", reason)
	if err := conn.Close(reason); err != nil {
		log.Printf("[sendspin] close: %v", err)
	}
}

func (c *Client) supervise(ctx context.Context) {
	backoff := reconnectMin
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.session(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err == nil:
			// A clean end — the server closed, or we left. Retry promptly:
			// this is what a Music Assistant restart looks like.
			backoff = reconnectMin
		case errors.Is(err, ErrFormatNotAdvertised):
			// Not transient. Reconnecting immediately would loop against a
			// server that will make the same choice, so back off fully and
			// say why once.
			log.Printf("[sendspin] %v — the format list and the decoder disagree", err)
			backoff = reconnectMax
		default:
			log.Printf("[sendspin] session ended: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// session is one discover-dial-run cycle. It always tears down what it built.
func (c *Client) session(ctx context.Context) error {
	dctx, dcancel := context.WithTimeout(ctx, discoverTimeout)
	srv, err := Discover(dctx, c.iface)
	dcancel()
	if err != nil {
		return err
	}

	transport, err := Dial(ctx, srv.URL())
	if err != nil {
		return err
	}

	runtime := NewRuntime(c.sink, c.plane, nil, c.opts.OutputDelayMs)
	opts := c.opts
	opts.Handler = runtime

	conn := NewConn(transport, opts)
	// The runtime reads the clock through the connection it is attached to,
	// and the connection is built after it. Wired here rather than passed
	// in so neither has to be constructed twice.
	runtime.clock = conn.Clock

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	runErr := conn.Run(ctx)

	c.mu.Lock()
	c.conn = nil
	c.mu.Unlock()

	// Whatever ended the session, the plane goes back and the socket
	// closes. Both are idempotent, and both are things the group notices
	// the absence of.
	c.plane.Release()
	if err := conn.Close(GoodbyeShutdown); err != nil {
		log.Printf("[sendspin] close: %v", err)
	}
	return runErr
}
