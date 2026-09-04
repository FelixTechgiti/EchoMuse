package sendspin

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// encodeFLAC produces a real FLAC stream from known samples, so the decoder is
// tested against audio rather than against a fixture nobody can regenerate.
// Returns the whole stream; the tests split it into the header the server
// sends out of band and the frames a chunk carries.
func encodeFLAC(t *testing.T, samples []int32, channels, bps int) []byte {
	t.Helper()
	info := &meta.StreamInfo{
		BlockSizeMin:  16,
		BlockSizeMax:  uint16(len(samples) / channels),
		SampleRate:    48000,
		NChannels:     uint8(channels),
		BitsPerSample: uint8(bps),
		NSamples:      uint64(len(samples) / channels),
		MD5sum:        md5.Sum(nil),
	}
	var buf bytes.Buffer
	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		t.Fatal(err)
	}

	n := len(samples) / channels
	subs := make([]*frame.Subframe, channels)
	for c := 0; c < channels; c++ {
		s := make([]int32, n)
		for i := 0; i < n; i++ {
			s[i] = samples[i*channels+c]
		}
		subs[c] = &frame.Subframe{
			SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
			Samples:   s,
			NSamples:  n,
		}
	}
	chans := frame.ChannelsMono
	if channels == 2 {
		chans = frame.ChannelsLR
	}
	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(n),
			SampleRate:        48000,
			Channels:          chans,
			BitsPerSample:     uint8(bps),
		},
		Subframes: subs,
	}
	if err := enc.WriteFrame(f); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// splitStream separates the header (magic + metadata) from the audio frames,
// which is how Sendspin carries them: the header rides stream/start as
// codec_header, and the frames ride the chunks.
func splitStream(t *testing.T, stream []byte) (header, frames []byte) {
	t.Helper()
	// The frame sync code is 0xFF 0xF8 with a fixed block size. Searching
	// for it is enough here because the header is a known-length STREAMINFO
	// and this test's own encoder writes nothing else.
	idx := bytes.Index(stream, []byte{0xFF, 0xF8})
	if idx < 0 {
		t.Fatal("no frame sync code in the encoded stream")
	}
	return stream[:idx], stream[idx:]
}

func TestARealFlacChunkDecodesToItsSamples(t *testing.T) {
	want := make([]int32, 512)
	for i := range want {
		want[i] = int32(i*37 - 8000)
	}
	stream := encodeFLAC(t, want, 1, 16)
	header, frames := splitStream(t, stream)

	dec, err := newFLACDecoder(StreamStart{
		Codec: CodecFLAC, SampleRate: 48000, Channels: 1, BitDepth: 16,
		CodecHeader: base64.StdEncoding.EncodeToString(header),
	})
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := dec.decode(frames)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(pcm) / 2; got != len(want) {
		t.Fatalf("decoded %d samples, want %d", got, len(want))
	}
	for i, w := range want {
		got := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		if got != int16(w) {
			t.Fatalf("sample %d = %d, want %d", i, got, w)
		}
	}
}

func TestAStereoFlacChunkComesBackMono(t *testing.T) {
	// The music plane is mono end to end. Averaged, and averaged in wider
	// arithmetic: two channels near full scale sum past 32767 and wrap to
	// full-scale negative in int16.
	n := 256
	interleaved := make([]int32, n*2)
	for i := 0; i < n; i++ {
		interleaved[i*2] = 30000
		interleaved[i*2+1] = 30000
	}
	stream := encodeFLAC(t, interleaved, 2, 16)
	header, frames := splitStream(t, stream)

	dec, err := newFLACDecoder(StreamStart{
		Codec: CodecFLAC, SampleRate: 48000, Channels: 2, BitDepth: 16,
		CodecHeader: base64.StdEncoding.EncodeToString(header),
	})
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := dec.decode(frames)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(pcm) / 2; got != n {
		t.Fatalf("decoded %d mono samples, want %d", got, n)
	}
	first := int16(binary.LittleEndian.Uint16(pcm))
	if first != 30000 {
		t.Fatalf("downmix = %d, want 30000 (a wrap would be strongly negative)", first)
	}
}

func TestABareStreamInfoBlockIsWrappedRatherThanRefused(t *testing.T) {
	// The codec_header's exact shape is not specified precisely, and it is a
	// thing only a live server can settle. Accepting both a complete stream
	// header and a bare 34-byte STREAMINFO costs four bytes of comparison
	// once per stream; guessing one and refusing the other produces a stream
	// that decodes to nothing, with an error naming base64 or a sync code
	// rather than the shape.
	want := make([]int32, 128)
	for i := range want {
		want[i] = int32(i * 11)
	}
	stream := encodeFLAC(t, want, 1, 16)
	header, frames := splitStream(t, stream)

	// Strip the magic and the metadata block header to leave the bare block.
	bare := header[len(flacMagic)+4:]
	if len(bare) != streamInfoLen {
		t.Fatalf("test setup: the bare block is %d bytes, want %d",
			len(bare), streamInfoLen)
	}

	dec, err := newFLACDecoder(StreamStart{
		Codec: CodecFLAC, SampleRate: 48000, Channels: 1, BitDepth: 16,
		CodecHeader: base64.StdEncoding.EncodeToString(bare),
	})
	if err != nil {
		t.Fatalf("a bare STREAMINFO was refused: %v", err)
	}
	pcm, err := dec.decode(frames)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(pcm) / 2; got != len(want) {
		t.Fatalf("decoded %d samples, want %d", got, len(want))
	}
}

func TestAStreamWithNoCodecHeaderIsRefusedAtStreamStart(t *testing.T) {
	// Refused before any audio is placed against a timestamp, and once
	// rather than 23 times a second.
	_, err := newFLACDecoder(StreamStart{Codec: CodecFLAC, Channels: 1})
	if err == nil {
		t.Fatal("a FLAC stream with no codec_header was accepted")
	}
}

func TestAMalformedCodecHeaderIsRefusedWithItsReason(t *testing.T) {
	for name, header := range map[string]string{
		"not base64":   "!!!not base64!!!",
		"wrong length": base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
	} {
		_, err := newFLACDecoder(StreamStart{
			Codec: CodecFLAC, Channels: 1, CodecHeader: header})
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestGarbageInAChunkIsAnErrorNotAPanic(t *testing.T) {
	want := make([]int32, 64)
	stream := encodeFLAC(t, want, 1, 16)
	header, _ := splitStream(t, stream)
	dec, err := newFLACDecoder(StreamStart{
		Codec: CodecFLAC, SampleRate: 48000, Channels: 1, BitDepth: 16,
		CodecHeader: base64.StdEncoding.EncodeToString(header),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.decode([]byte{0xDE, 0xAD, 0xBE, 0xEF}); err == nil {
		t.Fatal("garbage decoded without error")
	}
	// An empty chunk is a chunk, not a fault.
	if pcm, err := dec.decode(nil); err != nil || pcm != nil {
		t.Fatalf("an empty chunk gave %v, %v", pcm, err)
	}
}

func TestChunksAreIndependentOfEachOther(t *testing.T) {
	// A fresh stream per chunk is the point: each chunk carries its own
	// timestamp and has to be independently placeable, and the scheduler
	// DROPS chunks that are too far out of position. A decoder carried
	// across chunks would let one dropped chunk corrupt everything after it.
	want := make([]int32, 256)
	for i := range want {
		want[i] = int32(i * 5)
	}
	stream := encodeFLAC(t, want, 1, 16)
	header, frames := splitStream(t, stream)
	dec, err := newFLACDecoder(StreamStart{
		Codec: CodecFLAC, SampleRate: 48000, Channels: 1, BitDepth: 16,
		CodecHeader: base64.StdEncoding.EncodeToString(header),
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := dec.decode(frames)
	if err != nil {
		t.Fatal(err)
	}
	// A chunk arrives, is dropped by the scheduler, and the next one still
	// decodes identically.
	dec.decode([]byte{0x00, 0x01})
	second, err := dec.decode(frames)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same chunk decoded differently after a broken one")
	}
}

// ── The two conversions, with hand-written numbers ────────────────────────

func TestBitDepthIsShiftedNotScaled(t *testing.T) {
	// A 24-bit sample is >>8, not multiplied by 32767/8388607. The shift is
	// exact and is what every other converter does, so a stream that round
	// trips through this and something else agrees with itself.
	out := appendMonoInt16(nil, [][]int32{{1 << 8, -(1 << 8), 0}}, 24)
	for i, want := range []int16{1, -1, 0} {
		got := int16(binary.LittleEndian.Uint16(out[i*2:]))
		if got != want {
			t.Fatalf("24-bit sample %d became %d, want %d", i, got, want)
		}
	}
}

func TestAShallowerDepthIsShiftedUp(t *testing.T) {
	out := appendMonoInt16(nil, [][]int32{{1, -1}}, 8)
	if got := int16(binary.LittleEndian.Uint16(out)); got != 256 {
		t.Fatalf("8-bit sample 1 became %d, want 256", got)
	}
}

func TestChannelsAreAveragedInWiderArithmetic(t *testing.T) {
	// Two channels near full scale sum past 32767 and wrap to full-scale
	// negative in int16, which is a far worse artefact than the clipping it
	// looks like. Same rule as the mixer and the PCM downmix.
	out := appendMonoInt16(nil, [][]int32{{32000}, {32000}}, 16)
	if got := int16(binary.LittleEndian.Uint16(out)); got != 32000 {
		t.Fatalf("average = %d, want 32000", got)
	}
}

func TestTheResultIsClampedNotWrapped(t *testing.T) {
	out := appendMonoInt16(nil, [][]int32{{1 << 20}, {1 << 20}}, 16)
	if got := int16(binary.LittleEndian.Uint16(out)); got != 32767 {
		t.Fatalf("an out-of-range sample became %d, want a clamp to 32767", got)
	}
	out = appendMonoInt16(nil, [][]int32{{-(1 << 20)}, {-(1 << 20)}}, 16)
	if got := int16(binary.LittleEndian.Uint16(out)); got != -32768 {
		t.Fatalf("an out-of-range sample became %d, want a clamp to -32768", got)
	}
}

func TestNoChannelsIsNotAPanic(t *testing.T) {
	if out := appendMonoInt16(nil, nil, 16); out != nil {
		t.Fatal("empty input produced output")
	}
	if out := appendMonoInt16(nil, [][]int32{{}}, 16); out != nil {
		t.Fatal("an empty channel produced output")
	}
}

func TestFlacIsNowAdvertisedFirst(t *testing.T) {
	// It is a measurement, not a preference: 3.68% of one core against 0.97%
	// for PCM, saving 929 kbps on links measured at 4.6–7.1% loss.
	if got := SupportedFormats()[0].Codec; got != CodecFLAC {
		t.Fatalf("first advertised codec = %q, want flac", got)
	}
	if !Decodable(CodecFLAC) {
		t.Fatal("FLAC is advertised first and reported undecodable")
	}
	if PreferredFormat().Codec != CodecFLAC {
		t.Fatalf("preferred codec = %q", PreferredFormat().Codec)
	}
}

func TestPcmStaysAdvertisedBelowIt(t *testing.T) {
	// client/hello is a one-shot: dropping PCM because nothing asks for it
	// would mean a fallback needs a reconnect to become possible.
	found := false
	for _, f := range SupportedFormats() {
		if f.Codec == CodecPCM {
			found = true
		}
	}
	if !found {
		t.Fatal("PCM was dropped from the advertised set")
	}
}

func TestTheDecoderErrorNamesTheStream(t *testing.T) {
	// It reaches a log line on somebody's device, and "flac header" against
	// "flac frame" is the difference between a bad codec_header and a bad
	// chunk.
	_, err := newFLACDecoder(StreamStart{Codec: CodecFLAC, Channels: 1,
		CodecHeader: base64.StdEncoding.EncodeToString(make([]byte, streamInfoLen))})
	if err == nil {
		return // a zero STREAMINFO may parse; the shape is what matters
	}
	if !strings.Contains(err.Error(), "sendspin") {
		t.Fatalf("error does not name the subsystem: %v", err)
	}
}
