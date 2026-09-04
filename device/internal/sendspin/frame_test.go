package sendspin

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestAnAudioChunkRoundTrips(t *testing.T) {
	in := AudioChunk{Timestamp: 1_700_000_000_123_456, SendAhead: 250_000,
		Data: []byte{0x0f, 0x43, 0x00, 0x99}}
	out, err := DecodeAudioChunk(EncodeAudioChunk(in))
	if err != nil {
		t.Fatal(err)
	}
	if out.Timestamp != in.Timestamp || out.SendAhead != in.SendAhead ||
		!bytes.Equal(out.Data, in.Data) {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

func TestTheAudioHeaderIsBigEndianAtTheSpecsOffsets(t *testing.T) {
	// Pinned as bytes, not as a round trip: a round trip agrees with itself
	// whatever the layout, and the whole point is agreeing with a server
	// written by somebody else. Byte 0 type, 1–8 timestamp, 9–12 send_ahead.
	frame := EncodeAudioChunk(AudioChunk{Timestamp: 1, SendAhead: 1, Data: []byte{0xAA}})
	want := []byte{
		TypePlayerAudio,
		0, 0, 0, 0, 0, 0, 0, 1,
		0, 0, 0, 1,
		0xAA,
	}
	if !bytes.Equal(frame, want) {
		t.Fatalf("frame = % x, want % x", frame, want)
	}
}

func TestATimestampNearTheEndOfTheRangeSurvives(t *testing.T) {
	// It is a signed 64-bit microsecond count and is read back through
	// uint64. A sign-losing conversion would put playback ~584,000 years
	// out, which the scheduler would report as a chunk far in the future.
	for _, ts := range []int64{math.MaxInt64, math.MinInt64, -1, 0} {
		got, err := DecodeAudioChunk(EncodeAudioChunk(AudioChunk{Timestamp: ts}))
		if err != nil {
			t.Fatal(err)
		}
		if got.Timestamp != ts {
			t.Fatalf("timestamp %d came back %d", ts, got.Timestamp)
		}
	}
}

func TestATruncatedAudioFrameIsAnErrorNotAPanic(t *testing.T) {
	full := EncodeAudioChunk(AudioChunk{Timestamp: 5, SendAhead: 5, Data: []byte{1, 2}})
	for n := 0; n < audioHeaderLen; n++ {
		if _, err := DecodeAudioChunk(full[:n]); !errors.Is(err, ErrShortFrame) {
			t.Fatalf("length %d: err = %v, want ErrShortFrame", n, err)
		}
	}
	// Exactly the header and no audio is legal — an empty chunk is a chunk.
	if _, err := DecodeAudioChunk(full[:audioHeaderLen]); err != nil {
		t.Fatalf("header-only chunk rejected: %v", err)
	}
}

func TestAControlMessageRoundTrips(t *testing.T) {
	frame, err := EncodeJSON("client/time", map[string]any{"client_transmitted": 42})
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != TypeJSON {
		t.Fatalf("type byte = %d, want %d", frame[0], TypeJSON)
	}
	env, err := DecodeEnvelope(frame[1:])
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "client/time" {
		t.Fatalf("type = %q", env.Type)
	}
	var p struct {
		ClientTransmitted int64 `json:"client_transmitted"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.ClientTransmitted != 42 {
		t.Fatalf("client_transmitted = %d", p.ClientTransmitted)
	}
}

func TestAMessageWithNoPayloadOmitsTheFieldRatherThanSendingNull(t *testing.T) {
	frame, err := EncodeJSON("client/goodbye", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(frame, []byte("null")) {
		t.Fatalf("frame carries a null payload: %s", frame[1:])
	}
}

func TestAnUnknownPayloadFieldIsIgnored(t *testing.T) {
	// The spec requires it, and it is what lets a newer server talk to this
	// client at all rather than failing the handshake on a field added after
	// the firmware shipped.
	env, err := DecodeEnvelope([]byte(
		`{"type":"server/time","payload":{"server_received":7,"invented_later":true},"also_new":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "server/time" {
		t.Fatalf("type = %q", env.Type)
	}
}

func TestAMessageThatFitsIsNotFragmented(t *testing.T) {
	// Wrapping everything would cost two bytes on every audio chunk to
	// serve the rare case.
	frame := EncodeAudioChunk(AudioChunk{Data: make([]byte, 1000)})
	got := Fragment(frame)
	if len(got) != 1 || &got[0][0] != &frame[0] {
		t.Fatalf("a short frame was rewritten into %d fragments", len(got))
	}
}

func TestFragmentationRoundTripsThroughReassembly(t *testing.T) {
	for _, size := range []int{
		FragmentThreshold,     // exactly at the limit — one frame
		FragmentThreshold + 1, // one byte over — two
		FragmentThreshold * 3,
		FragmentThreshold*4 + 17,
	} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i * 7)
		}
		orig := append([]byte{TypePlayerAudio}, payload...)

		var r Reassembler
		var out []byte
		frames := Fragment(orig)
		for i, f := range frames {
			if len(f) > FragmentThreshold {
				t.Fatalf("size %d: fragment %d is %d bytes, over the threshold",
					size, i, len(f))
			}
			got, err := r.Push(f)
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
			if got != nil {
				if i != len(frames)-1 {
					t.Fatalf("size %d: completed at fragment %d of %d",
						size, i, len(frames))
				}
				out = got
			}
		}
		if !bytes.Equal(out, orig) {
			t.Fatalf("size %d: reassembled %d bytes, want %d", size, len(out), len(orig))
		}
		if r.InProgress() {
			t.Fatalf("size %d: still assembling after the last fragment", size)
		}
	}
}

func TestTheOriginalTypeSurvivesFragmentation(t *testing.T) {
	// The type byte is carried once, in the first fragment. Losing it means
	// a reassembled audio chunk dispatched as a control message.
	orig := append([]byte{TypePlayerAudio}, make([]byte, FragmentThreshold*2)...)
	var r Reassembler
	var out []byte
	for _, f := range Fragment(orig) {
		got, err := r.Push(f)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			out = got
		}
	}
	if out[0] != TypePlayerAudio {
		t.Fatalf("reassembled type = %d, want %d", out[0], TypePlayerAudio)
	}
}

func TestAnUnfragmentedFramePassesStraightThrough(t *testing.T) {
	var r Reassembler
	frame := EncodeAudioChunk(AudioChunk{Timestamp: 9})
	got, err := r.Push(frame)
	if err != nil || !bytes.Equal(got, frame) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestReservedFlagBitsAreRejectedRatherThanMaskedAway(t *testing.T) {
	// A reserved bit that starts carrying meaning is exactly the change a
	// client must not silently misread as a plain fragment.
	var r Reassembler
	_, err := r.Push([]byte{TypeFragment, flagFirst | 0x80, TypePlayerAudio, 1, 2})
	if !errors.Is(err, ErrReservedFlags) {
		t.Fatalf("err = %v, want ErrReservedFlags", err)
	}
}

func TestAContinuationWithNoFirstFragmentIsRejected(t *testing.T) {
	var r Reassembler
	if _, err := r.Push([]byte{TypeFragment, flagLast, 1, 2}); !errors.Is(err, ErrFragmentOrder) {
		t.Fatalf("err = %v, want ErrFragmentOrder", err)
	}
}

func TestASecondFirstFragmentMidAssemblyIsRejected(t *testing.T) {
	var r Reassembler
	if _, err := r.Push([]byte{TypeFragment, flagFirst, TypePlayerAudio, 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Push([]byte{TypeFragment, flagFirst, TypePlayerAudio, 2}); !errors.Is(err, ErrFragmentOrder) {
		t.Fatalf("err = %v, want ErrFragmentOrder", err)
	}
	if r.InProgress() {
		t.Fatal("the broken assembly was kept")
	}
}

func TestReassemblyIsBounded(t *testing.T) {
	// Unbounded by construction: a peer that never sets the last flag grows
	// this buffer until the device dies, and there is 512MB shared with
	// Android on the other side of that.
	r := Reassembler{Max: 4096}
	if _, err := r.Push([]byte{TypeFragment, flagFirst, TypePlayerAudio}); err != nil {
		t.Fatal(err)
	}
	var err error
	for i := 0; i < 100 && err == nil; i++ {
		_, err = r.Push(append([]byte{TypeFragment, 0}, make([]byte, 1000)...))
	}
	if !errors.Is(err, ErrFragmentTooLarge) {
		t.Fatalf("err = %v, want ErrFragmentTooLarge", err)
	}
	if r.InProgress() {
		t.Fatal("the oversized assembly was kept")
	}
}

func TestATruncatedFragmentHeaderIsAnError(t *testing.T) {
	var r Reassembler
	for _, f := range [][]byte{
		{},
		{TypeFragment},
		{TypeFragment, flagFirst}, // first fragment with no original type
	} {
		if _, err := r.Push(f); !errors.Is(err, ErrShortFrame) {
			t.Fatalf("% x: err = %v, want ErrShortFrame", f, err)
		}
	}
}

func TestTheFragmentThresholdLeavesRoomForTheNoiseTag(t *testing.T) {
	// Fragmentation happens before encryption, so a frame that only
	// overflows once its 16-byte tag is appended is a frame nobody can send
	// — and the failure would be at the far end of a working handshake.
	if FragmentThreshold+16 != 65534 {
		t.Fatalf("threshold %d + 16 = %d, want 65534",
			FragmentThreshold, FragmentThreshold+16)
	}
}
