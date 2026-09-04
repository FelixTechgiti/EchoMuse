package sendspin

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Sendspin's binary framing.
//
// Every WebSocket binary message begins with one type byte. Type 0 carries a
// JSON control message; type 1 is a fragment of a larger message; the rest are
// per-role and the player's audio chunks are type 4. There is no length field
// and no envelope around the type byte — the WebSocket frame IS the message
// boundary, which is why a partial read must never be dispatched.
//
// Untagged and dependency-free: this is parsing, and parsing is where a wrong
// byte offset produces audio that decodes to noise rather than an error. All
// of it belongs in the host test suite.

// Message type identifiers. The ranges are assigned per role, so a type this
// client does not implement is a message for a role it did not activate and
// is ignored rather than treated as a fault — the protocol grows by adding
// roles, and a client that errors on an unknown type breaks on the next one.
const (
	TypeJSON     = 0 // UTF-8 JSON control message
	TypeFragment = 1 // a piece of a message too large for one frame
	TypePairing  = 2
	// 4–7 are the player role. 4 is an audio chunk; the rest are unassigned
	// in v1 and are ignored.
	TypePlayerAudio = 4
)

// FragmentThreshold is the largest payload that fits in one frame. The 16
// bytes below 64KiB are Noise's authentication tag: the fragmenter has to
// leave room for it because fragmentation happens BEFORE encryption, and a
// frame that only overflows once encrypted is a frame nobody can send.
const FragmentThreshold = 65518

// Fragment flag bits. Bit 1 marks the first fragment, bit 0 the last. Bits
// 2–7 are reserved and MUST be zero — checked rather than masked away,
// because a reserved bit that starts carrying meaning is exactly the kind of
// change a client must not silently misread as a plain fragment.
const (
	flagLast  = 1 << 0
	flagFirst = 1 << 1
	flagsMask = flagLast | flagFirst
)

var (
	// ErrShortFrame: the frame ended inside a header. Distinct from an
	// unknown type, which is ordinary.
	ErrShortFrame = errors.New("sendspin: frame is shorter than its header")
	// ErrReservedFlags: a fragment set a reserved bit.
	ErrReservedFlags = errors.New("sendspin: fragment sets reserved flag bits")
	// ErrFragmentOrder: a continuation arrived with no first fragment, or a
	// first fragment arrived mid-reassembly.
	ErrFragmentOrder = errors.New("sendspin: fragment out of order")
	// ErrFragmentTooLarge: reassembly exceeded the cap.
	ErrFragmentTooLarge = errors.New("sendspin: reassembled message exceeds the limit")
)

// Envelope is the JSON control message shape: every one of them, both
// directions. Payload is left raw so a message can be routed on its type
// without knowing what it carries, and so an unrecognised payload field is
// ignored — which the spec requires and which is what lets a newer server
// talk to this client at all.
type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// DecodeEnvelope parses a type-0 frame's body.
func DecodeEnvelope(body []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return Envelope{}, fmt.Errorf("sendspin: bad control message: %w", err)
	}
	return e, nil
}

// EncodeJSON builds a complete type-0 frame from a message type and payload.
// A nil payload is encoded as an absent field rather than `null`: some
// messages carry nothing, and `"payload": null` is a third shape for a
// receiver to have to handle.
func EncodeJSON(msgType string, payload any) ([]byte, error) {
	e := Envelope{Type: msgType}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		e.Payload = raw
	}
	body, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append([]byte{TypeJSON}, body...), nil
}

// AudioChunk is one type-4 player frame.
//
// Timestamp is when the first sample must LEAVE THE SPEAKER, in server time,
// microseconds. It is not an arrival time, a decode deadline or a sequence
// number, and treating it as any of those produces a group that plays the
// right audio at the wrong moment — which is the only failure this protocol
// has that sounds like a different bug entirely.
type AudioChunk struct {
	Timestamp int64  // server clock, µs — when sample 0 hits the air
	SendAhead uint32 // µs the server sent this ahead of that moment
	Data      []byte // encoded audio, codec per the stream/start that opened it
}

// audioHeaderLen is the type byte plus an int64 and a uint32, both big-endian.
const audioHeaderLen = 1 + 8 + 4

// DecodeAudioChunk parses a type-4 frame.
//
// Data aliases the caller's buffer rather than copying it: this runs once per
// 15–150ms of audio and the decoder consumes it immediately. A caller that
// keeps the chunk past the read loop's next iteration must copy — said here
// because the alternative is an allocation per chunk forever to protect a
// caller that does not exist.
func DecodeAudioChunk(frame []byte) (AudioChunk, error) {
	if len(frame) < audioHeaderLen {
		return AudioChunk{}, ErrShortFrame
	}
	return AudioChunk{
		Timestamp: int64(binary.BigEndian.Uint64(frame[1:9])),
		SendAhead: binary.BigEndian.Uint32(frame[9:13]),
		Data:      frame[audioHeaderLen:],
	}, nil
}

// EncodeAudioChunk builds a type-4 frame. Present for the tests and for a
// device that one day takes the `source` role; the player only ever decodes.
func EncodeAudioChunk(c AudioChunk) []byte {
	out := make([]byte, audioHeaderLen+len(c.Data))
	out[0] = TypePlayerAudio
	binary.BigEndian.PutUint64(out[1:9], uint64(c.Timestamp))
	binary.BigEndian.PutUint32(out[9:13], c.SendAhead)
	copy(out[audioHeaderLen:], c.Data)
	return out
}

// Fragment splits a complete frame into wire frames, fragmenting only when it
// has to. A message that fits is returned unchanged — one frame, its own type
// byte, no fragment header — because wrapping everything would cost two bytes
// on every audio chunk to serve the rare case.
func Fragment(frame []byte) [][]byte {
	if len(frame) <= FragmentThreshold {
		return [][]byte{frame}
	}
	origType := frame[0]
	body := frame[1:]
	// Each fragment spends its own header: the type byte, the flags, and on
	// the first one the original type as well.
	const contHeader = 2
	firstCap := FragmentThreshold - 3
	contCap := FragmentThreshold - contHeader

	var out [][]byte
	first := body
	if len(first) > firstCap {
		first = first[:firstCap]
	}
	body = body[len(first):]

	flags := byte(flagFirst)
	if len(body) == 0 {
		flags |= flagLast
	}
	f := make([]byte, 0, 3+len(first))
	f = append(f, TypeFragment, flags, origType)
	out = append(out, append(f, first...))

	for len(body) > 0 {
		n := len(body)
		if n > contCap {
			n = contCap
		}
		flags := byte(0)
		if n == len(body) {
			flags = flagLast
		}
		f := make([]byte, 0, contHeader+n)
		f = append(f, TypeFragment, flags)
		out = append(out, append(f, body[:n]...))
		body = body[n:]
	}
	return out
}

// Reassembler rebuilds fragmented messages.
//
// One per connection, driven by the read loop, so it needs no locking. The
// cap exists because reassembly is unbounded by construction — a peer that
// never sets the last flag would grow this buffer until the device dies, and
// on 512MB shared with Android that is not a distant hypothetical.
type Reassembler struct {
	buf      []byte
	origType byte
	active   bool
	// Max is the largest message that may be reassembled. Zero means the
	// default.
	Max int
}

// DefaultMaxMessage bounds reassembly. Generously above anything the player
// role produces (an audio chunk is at most ~150ms of FLAC) and far below what
// would hurt: artwork is the role that legitimately sends large messages, and
// this client does not take it.
const DefaultMaxMessage = 4 << 20

// Push feeds one wire frame. It returns a complete frame — with its original
// type byte restored — once one is available, and nil while a message is
// still being assembled.
//
// A non-fragment frame passes straight through, and does NOT reset an
// assembly in progress: the spec interleaves nothing here, but a defensive
// reset would silently discard a message on a peer that does, and losing the
// message is worse than holding it.
func (r *Reassembler) Push(frame []byte) ([]byte, error) {
	if len(frame) == 0 {
		return nil, ErrShortFrame
	}
	if frame[0] != TypeFragment {
		return frame, nil
	}
	if len(frame) < 2 {
		return nil, ErrShortFrame
	}
	flags := frame[1]
	if flags&^flagsMask != 0 {
		return nil, ErrReservedFlags
	}
	first := flags&flagFirst != 0
	last := flags&flagLast != 0

	max := r.Max
	if max <= 0 {
		max = DefaultMaxMessage
	}

	if first {
		if r.active {
			r.reset()
			return nil, ErrFragmentOrder
		}
		if len(frame) < 3 {
			return nil, ErrShortFrame
		}
		r.active = true
		r.origType = frame[2]
		r.buf = append(r.buf[:0], frame[3:]...)
	} else {
		if !r.active {
			return nil, ErrFragmentOrder
		}
		if len(r.buf)+len(frame)-2 > max {
			r.reset()
			return nil, ErrFragmentTooLarge
		}
		r.buf = append(r.buf, frame[2:]...)
	}

	if len(r.buf) > max {
		r.reset()
		return nil, ErrFragmentTooLarge
	}
	if !last {
		return nil, nil
	}

	out := make([]byte, 0, 1+len(r.buf))
	out = append(out, r.origType)
	out = append(out, r.buf...)
	r.reset()
	return out, nil
}

func (r *Reassembler) reset() {
	r.active = false
	r.buf = r.buf[:0]
	r.origType = 0
}

// InProgress reports whether a message is partially assembled — for the
// connection teardown, which must not report a clean close while a message is
// half-delivered.
func (r *Reassembler) InProgress() bool { return r.active }
