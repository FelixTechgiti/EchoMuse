package sendspin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// The connection state machine.
//
// It owns the handshake, the time-sync exchange and the stream lifecycle, and
// it owns NO audio: chunks are handed to a Handler with their timestamps
// intact. Decoding and scheduling live above this, which is what lets the
// whole protocol be exercised on the host against a scripted server — the
// alternative is a state machine that can only be tested by playing music in
// a room.
//
// The handshake order is the part most likely to be implemented wrong,
// because the obvious reading is wrong in two places:
//
//	client  ── client/init ──────────────────────────►   (CLEARTEXT)
//	client  ◄─ server/init + noise/handshake #1 ─────
//	client  ── noise/handshake #2 ──────────────────►
//	                  … transport mode …
//	client  ◄─ server/hello ─────────────────────────
//	client  ── client/hello ────────────────────────►
//	client  ◄─ server/activate ──────────────────────
//
// The SERVER is the Noise initiator and this client the responder. And
// `client/hello` — the one-shot carrying the format list — goes out AFTER
// encryption and after the server has introduced itself, not as the opening
// move.

// Transport is one WebSocket connection, in whole frames. Send and Recv are
// each called from a single goroutine, never concurrently with themselves;
// an implementation over gorilla therefore needs no write mutex of its own.
type Transport interface {
	Send(frame []byte) error
	Recv() (frame []byte, err error)
	Close() error
}

// Crypto is the Noise layer, as a layer that can be switched on rather than
// an assumption baked through the transport.
//
// It is an interface for a reason that has already been observed once:
// Music Assistant's own server shipped implementing no encryption at all,
// while the specification calls it mandatory. Both have to work, and a client
// that assumed either one would have to be rewritten to meet the other.
type Crypto interface {
	// Suites is what client/init advertises.
	Suites() []string
	// Begin is called with the server's init payload before the first Noise
	// message, so an implementation can resolve the PSK it names.
	Begin(init ServerInit) error
	// Respond consumes one Noise message from the server and returns ours.
	// done reports that the transport is established; out may be nil.
	Respond(in []byte) (out []byte, done bool, err error)
	// Seal and Open protect a frame once done. Before that they are not
	// called.
	Seal(plain []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

// Plaintext is a Crypto that establishes immediately and protects nothing.
//
// It exists for two real situations, not as a stub: a server that implements
// no encryption (which is what shipped), and the host tests, where a scripted
// server's frames have to be readable. It advertises the real suite names
// because a server choosing between them still needs an answer.
type Plaintext struct{}

func (Plaintext) Suites() []string                     { return []string{CipherChaChaPoly} }
func (Plaintext) Begin(ServerInit) error               { return nil }
func (Plaintext) Respond([]byte) ([]byte, bool, error) { return nil, true, nil }
func (Plaintext) Seal(p []byte) ([]byte, error)        { return p, nil }
func (Plaintext) Open(c []byte) ([]byte, error)        { return c, nil }

// Handler receives everything the connection decides is worth acting on.
//
// Every method is called from the connection's read goroutine, in order, and
// must not block for long: the next audio chunk is behind it. Anything slow
// belongs on its own goroutine, and the audio path already has one.
type Handler interface {
	// OnActivate: the handshake finished and the server said what this
	// connection is for. The player role may or may not be among the active
	// ones; a role the server did not activate is one to stop reporting.
	OnActivate(ServerActivate)
	// OnStreamStart: a new stream, with the format the server chose. The
	// connection has already checked it against what was advertised.
	OnStreamStart(StreamStart)
	// OnAudio: one chunk. Data aliases the read buffer and is invalid once
	// this returns — copy it or consume it.
	OnAudio(AudioChunk)
	// OnStreamClear: discard what is buffered. NOT a stop; the stream
	// continues, and treating it as an end leaves the device silent for the
	// rest of the track.
	OnStreamClear()
	// OnStreamEnd: the stream is over.
	OnStreamEnd()
	// OnCommand: the server is setting volume, mute or output delay.
	OnCommand(PlayerCommand)
	// OnGroupUpdate: playback state and group identity, for reporting.
	OnGroupUpdate(GroupUpdate)
	// OnSynced: the clock filter reached the point where timestamps can be
	// converted. Until then the spec forbids reporting available.
	OnSynced()
}

// Identity is what this device tells the server about itself.
type Identity struct {
	Name       string
	ClientID   string
	DeviceInfo DeviceInfo
}

// Options configure a connection.
type Options struct {
	Identity Identity
	Crypto   Crypto
	Handler  Handler
	// BufferSeconds is the device's own music buffer, used to derive
	// buffer_capacity. Passed in rather than read from the speaker package
	// so this stays host-testable.
	BufferSeconds float64
	// MinBufferMs and RequiredLeadTimeMs are reported in client/state so the
	// server can pace against them.
	MinBufferMs        int
	RequiredLeadTimeMs int
	// OutputDelayMs is this device's write-to-ear latency, which the server
	// subtracts when scheduling. It is NOT a fudge factor for clock error —
	// that is the filter's job, and using it as one makes this speaker play
	// early or late against a group that is otherwise correct.
	OutputDelayMs int
	// TimeSyncInterval between client/time exchanges. Zero uses the default.
	TimeSyncInterval time.Duration
	// Now is the local clock, for tests. Zero uses time.Now.
	Now func() time.Time
}

// DefaultTimeSyncInterval. Fast enough that the filter converges in the first
// few seconds of a session — before which the spec says we may not report
// available — and slow enough to be invisible next to the audio.
const DefaultTimeSyncInterval = time.Second

var (
	// ErrHandshake: the server did something the handshake does not allow.
	ErrHandshake = errors.New("sendspin: handshake failed")
	// ErrFormatNotAdvertised: the server opened a stream in a format this
	// client never offered. It is the server's own fallback behaviour on a
	// mismatch, and it must be reported rather than decoded as whatever we
	// last asked for — that turns a log line into noise from the speaker.
	ErrFormatNotAdvertised = errors.New("sendspin: stream format was never advertised")
)

// Conn is one Sendspin session.
type Conn struct {
	t    Transport
	opt  Options
	now  func() time.Time
	rasm Reassembler

	// filter is written only by the read goroutine.
	filter Filter

	mu       sync.Mutex
	ready    bool // transport mode established
	streamOn bool
	synced   bool
	closed   bool
	// snapshot is the filter copy the audio side reads. Guarded because it
	// crosses goroutines; the filter itself never does.
	snapshot Snapshot
}

// NewConn wires a connection. It does not talk to the transport until Run.
func NewConn(t Transport, opt Options) *Conn {
	if opt.Crypto == nil {
		opt.Crypto = Plaintext{}
	}
	if opt.TimeSyncInterval <= 0 {
		opt.TimeSyncInterval = DefaultTimeSyncInterval
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	return &Conn{t: t, opt: opt, now: now}
}

// Clock returns the current server-time conversion. Safe from any goroutine;
// the audio path calls it per chunk.
func (c *Conn) Clock() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot
}

func (c *Conn) micros() int64 { return c.now().UnixMicro() }

// send marshals, seals if the transport is up, fragments, and writes.
//
// Sealing is decided by `ready` rather than by the message: every frame after
// transport mode is protected, including the ones that look like handshake
// messages. Deciding per message type is how a single unprotected frame gets
// out.
func (c *Conn) send(msgType string, payload any) error {
	frame, err := EncodeJSON(msgType, payload)
	if err != nil {
		return err
	}
	return c.sendFrame(frame)
}

func (c *Conn) sendFrame(frame []byte) error {
	c.mu.Lock()
	ready := c.ready
	c.mu.Unlock()

	if ready {
		sealed, err := c.opt.Crypto.Seal(frame)
		if err != nil {
			return err
		}
		frame = sealed
	}
	for _, f := range Fragment(frame) {
		if err := c.t.Send(f); err != nil {
			return err
		}
	}
	return nil
}

// Run drives the session until the context is cancelled or the transport
// fails. It always attempts a goodbye on the way out — see Close.
func (c *Conn) Run(ctx context.Context) error {
	if err := c.send(MsgClientInit, ClientInit{CipherSuites: c.opt.Crypto.Suites()}); err != nil {
		return fmt.Errorf("sendspin: client/init: %w", err)
	}

	frames := make(chan []byte, 8)
	errs := make(chan error, 1)
	go func() {
		for {
			f, err := c.t.Recv()
			if err != nil {
				errs <- err
				close(frames)
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				close(frames)
				return
			}
		}
	}()

	ticker := time.NewTicker(c.opt.TimeSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			return err
		case <-ticker.C:
			// A failed time exchange is not fatal on its own — the filter
			// carries its estimate — so it is logged and the session
			// continues. A transport that is genuinely gone surfaces on the
			// read side.
			if err := c.sendTimeRequest(); err != nil {
				log.Printf("[sendspin] time request: %v", err)
			}
		case raw, ok := <-frames:
			if !ok {
				return <-errs
			}
			if err := c.handleFrame(raw); err != nil {
				return err
			}
		}
	}
}

func (c *Conn) sendTimeRequest() error {
	return c.send(MsgClientTime, ClientTime{ClientTransmitted: c.micros()})
}

func (c *Conn) handleFrame(raw []byte) error {
	c.mu.Lock()
	ready := c.ready
	c.mu.Unlock()

	if ready {
		plain, err := c.opt.Crypto.Open(raw)
		if err != nil {
			return fmt.Errorf("sendspin: decrypt: %w", err)
		}
		raw = plain
	}

	frame, err := c.rasm.Push(raw)
	if err != nil {
		return err
	}
	if frame == nil {
		return nil // still assembling
	}

	switch frame[0] {
	case TypeJSON:
		env, err := DecodeEnvelope(frame[1:])
		if err != nil {
			return err
		}
		return c.handleControl(env)
	case TypePlayerAudio:
		return c.handleAudio(frame)
	default:
		// A type this client does not implement belongs to a role it did
		// not activate. Ignored rather than treated as a fault: the protocol
		// grows by adding roles, and erroring here breaks on the next one.
		return nil
	}
}

func (c *Conn) handleAudio(frame []byte) error {
	chunk, err := DecodeAudioChunk(frame)
	if err != nil {
		return err
	}
	c.mu.Lock()
	on := c.streamOn
	c.mu.Unlock()
	if !on {
		// Audio outside a stream has no format behind it. Decoding it as
		// whatever the last stream used is how a codec change produces
		// noise instead of a gap.
		return nil
	}
	c.opt.Handler.OnAudio(chunk)
	return nil
}

func (c *Conn) handleControl(env Envelope) error {
	switch env.Type {
	case MsgServerInit:
		var init ServerInit
		if err := decodePayload(env.Payload, &init); err != nil {
			return err
		}
		return c.opt.Crypto.Begin(init)

	case MsgNoiseHandshake:
		var hs NoiseHandshake
		if err := decodePayload(env.Payload, &hs); err != nil {
			return err
		}
		in, err := base64.StdEncoding.DecodeString(hs.Message)
		if err != nil {
			return fmt.Errorf("%w: noise message is not base64: %v", ErrHandshake, err)
		}
		out, done, err := c.opt.Crypto.Respond(in)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrHandshake, err)
		}
		if out != nil {
			// Sent BEFORE the ready flag flips: this frame is part of the
			// handshake and is not itself protected by the transport it
			// establishes.
			if err := c.send(MsgNoiseHandshake, NoiseHandshake{
				Message: base64.StdEncoding.EncodeToString(out),
			}); err != nil {
				return err
			}
		}
		if done {
			c.mu.Lock()
			c.ready = true
			c.mu.Unlock()
		}
		return nil

	case MsgServerHello:
		// client/hello answers server/hello, and only server/hello. Sending
		// it earlier is the ordering mistake the spec's own sequence makes
		// easy, and it costs the format list — the one-shot that cannot be
		// revised without reconnecting.
		var hello ServerHello
		if err := decodePayload(env.Payload, &hello); err != nil {
			return err
		}
		if hello.Name != "" {
			log.Printf("[sendspin] server: %s", hello.Name)
		}
		return c.sendClientHello()

	case MsgServerActivate:
		var act ServerActivate
		if err := decodePayload(env.Payload, &act); err != nil {
			return err
		}
		c.opt.Handler.OnActivate(act)
		// State goes out immediately so the server knows what this client
		// can do, with available deliberately absent — the clock is not
		// synced yet and the spec forbids claiming it is.
		return c.SendState()

	case MsgServerTime:
		var st ServerTime
		if err := decodePayload(env.Payload, &st); err != nil {
			return err
		}
		return c.applyTime(st)

	case MsgStreamStart:
		var msg StreamStartMsg
		if err := decodePayload(env.Payload, &msg); err != nil {
			return err
		}
		if msg.Player == nil {
			return nil // a stream for a role we do not take
		}
		if !Supports(msg.Player.Format()) {
			// The server falls back silently on a mismatch, so this is the
			// only place the mismatch can be noticed at all.
			return fmt.Errorf("%w: %+v", ErrFormatNotAdvertised, msg.Player.Format())
		}
		c.mu.Lock()
		c.streamOn = true
		c.mu.Unlock()
		c.opt.Handler.OnStreamStart(*msg.Player)
		return nil

	case MsgStreamClear:
		var msg StreamClear
		if err := decodePayload(env.Payload, &msg); err != nil {
			return err
		}
		if AppliesToPlayer(msg.Roles) {
			c.opt.Handler.OnStreamClear()
		}
		return nil

	case MsgStreamEnd:
		var msg StreamEnd
		if err := decodePayload(env.Payload, &msg); err != nil {
			return err
		}
		if AppliesToPlayer(msg.Roles) {
			c.mu.Lock()
			c.streamOn = false
			c.mu.Unlock()
			c.opt.Handler.OnStreamEnd()
		}
		return nil

	case MsgServerCommand:
		var cmd ServerCommand
		if err := decodePayload(env.Payload, &cmd); err != nil {
			return err
		}
		if cmd.Player != nil {
			c.opt.Handler.OnCommand(*cmd.Player)
		}
		return nil

	case MsgGroupUpdate:
		var g GroupUpdate
		if err := decodePayload(env.Payload, &g); err != nil {
			return err
		}
		c.opt.Handler.OnGroupUpdate(g)
		return nil

	default:
		// Unrecognised control messages are ignored, for the reason unknown
		// payload fields are: it is what lets a newer server talk to
		// firmware that shipped before the message existed.
		return nil
	}
}

func (c *Conn) applyTime(st ServerTime) error {
	c.filter.Measure(st.ClientTransmitted, st.ServerReceived,
		st.ServerTransmitted, c.micros())
	snap := c.filter.Snapshot()

	c.mu.Lock()
	c.snapshot = snap
	first := snap.Ready && !c.synced
	if first {
		c.synced = true
	}
	c.mu.Unlock()

	if first {
		c.opt.Handler.OnSynced()
		// Only now may this client report itself available. Reporting it
		// earlier puts an unsynced player in a group, which plays at the
		// wrong time and sounds like a fault in the group rather than in
		// the member that just joined.
		return c.SendState()
	}
	return nil
}

func (c *Conn) sendClientHello() error {
	f := PreferredFormat()
	return c.send(MsgClientHello, ClientHello{
		Name:           c.opt.Identity.Name,
		SupportedRoles: []string{PlayerRole},
		TrustLevel:     TrustNone,
		DeviceInfo:     &c.opt.Identity.DeviceInfo,
		ClientID:       c.opt.Identity.ClientID,
		PlayerSupport: &PlayerSupport{
			SupportedFormats: SupportedFormats(),
			BufferCapacity:   BufferCapacity(c.opt.BufferSeconds, f.Channels),
		},
		UnpairedAccess: UnpairedAccess{Enabled: true},
	})
}

// SendState reports this client's player state.
//
// available is sent as true only once the clock is synced, and is OMITTED
// before that rather than sent as false: absent means "not yet", false means
// "this speaker will not play", and a group told the second one moves on
// without it.
func (c *Conn) SendState() error {
	c.mu.Lock()
	synced := c.synced
	c.mu.Unlock()

	state := ClientState{
		Player: &PlayerState{
			OutputDelayMs:      c.opt.OutputDelayMs,
			RequiredLeadTimeMs: c.opt.RequiredLeadTimeMs,
			MinBufferMs:        c.opt.MinBufferMs,
			// No commands are claimed: volume and mute on this device are
			// owned by the controller and the button, and claiming a
			// command obliges this client to carry its state field. A
			// control the server offers and this device ignores is exactly
			// the failure the capability rules elsewhere exist to prevent.
			SupportedCommands: []string{},
		},
	}
	if synced {
		yes := true
		state.Available = &yes
	}
	return c.send(MsgClientState, state)
}

// Goodbye ends the session cleanly. It is the whole of "leaving is not
// ignoring": a server told why a client left keeps its group view correct,
// where one that merely stopped hearing from it does not.
//
// Idempotent, because the paths that call it overlap — a preemption by Home
// Assistant and the connection's own teardown both end here — and a second
// goodbye on a closed transport is an error nobody can act on.
func (c *Conn) Goodbye(reason string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	return c.send(MsgClientGoodbye, ClientGoodbye{Reason: reason})
}

// Close sends a goodbye and shuts the transport down. The goodbye's failure
// is not returned: the transport is going away either way, and a caller that
// treats "could not say goodbye" as a failure to close will retry a close.
func (c *Conn) Close(reason string) error {
	if err := c.Goodbye(reason); err != nil {
		log.Printf("[sendspin] goodbye: %v", err)
	}
	return c.t.Close()
}
