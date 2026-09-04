package sendspin

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mewkiz/flac"
)

// FLAC decoding.
//
// Worth having on measurement rather than principle: 3.68% of one core
// against 0.97% for PCM, and 929 kbps less on the wire. This fleet's links
// measure 4.6–7.1% packet loss (#139/#140), so trading a link known to be
// marginal against a core with room on it is not a close call.
//
// Pure Go, no cgo. That is why FLAC and not Opus: the codec choice is
// constrained by what can be cross-compiled for armv7a/API 22 without a
// toolchain problem, and the spec's requirement that every SERVER supports
// all three is what makes advertising a subset legal.

// flacDecoder turns Sendspin audio chunks into mono 16-bit PCM.
//
// One per stream. It holds the STREAMINFO the server sent out of band,
// because the chunks themselves carry frames and not a stream header — so
// each chunk has to be prefixed with one before a decoder will look at it.
type flacDecoder struct {
	// header is a complete FLAC stream header: the "fLaC" magic plus a
	// STREAMINFO metadata block, ready to be concatenated with a chunk.
	header []byte
	// channels/bps come from the stream/start, and are what the output is
	// converted TO rather than what the frames are in. A frame carries its
	// own, and they can differ mid-stream.
	channels int
}

var errNoCodecHeader = errors.New("sendspin: a FLAC stream with no codec_header")

// flacMagic opens every FLAC stream.
var flacMagic = []byte("fLaC")

// streamInfoLen is the fixed size of a STREAMINFO metadata block.
const streamInfoLen = 34

// newFLACDecoder prepares a decoder from a stream/start.
//
// THE CODEC HEADER'S SHAPE IS NOT SPECIFIED PRECISELY, so both are accepted:
// a complete stream header beginning with "fLaC", and a bare STREAMINFO block
// that has to be wrapped in one. Guessing one and refusing the other would
// produce a stream that decodes to nothing, with an error naming base64 or a
// sync code rather than the shape — and the shape is a thing only a live
// server can settle. Accepting both costs four bytes of comparison once per
// stream.
func newFLACDecoder(s StreamStart) (*flacDecoder, error) {
	if s.CodecHeader == "" {
		return nil, errNoCodecHeader
	}
	raw, err := base64.StdEncoding.DecodeString(s.CodecHeader)
	if err != nil {
		return nil, fmt.Errorf("sendspin: codec_header is not base64: %w", err)
	}
	ch := s.Channels
	if ch <= 0 {
		ch = 1
	}

	if bytes.HasPrefix(raw, flacMagic) {
		return &flacDecoder{header: raw, channels: ch}, nil
	}
	if len(raw) != streamInfoLen {
		return nil, fmt.Errorf("sendspin: codec_header is %d bytes — neither a "+
			"stream header nor a %d-byte STREAMINFO block", len(raw), streamInfoLen)
	}
	// Wrap the bare block: magic, then a metadata block header with the
	// last-block bit set (0x80), type 0 (STREAMINFO), and a 24-bit length.
	hdr := make([]byte, 0, len(flacMagic)+4+streamInfoLen)
	hdr = append(hdr, flacMagic...)
	hdr = append(hdr, 0x80)
	hdr = append(hdr, byte(streamInfoLen>>16), byte(streamInfoLen>>8), byte(streamInfoLen))
	hdr = append(hdr, raw...)
	return &flacDecoder{header: hdr, channels: ch}, nil
}

// decode turns one chunk into mono 16-bit little-endian PCM.
//
// A FRESH STREAM PER CHUNK, and that is correct rather than lazy. Each chunk
// carries its own timestamp and has to be independently placeable, so it
// contains whole FLAC frames — and a FLAC frame is independently decodable
// given STREAMINFO, which is the format's own design. Carrying one decoder
// across chunks would couple them, and a dropped chunk (which the scheduler
// does, deliberately, when one is too far out of position) would then corrupt
// everything after it.
//
// The cost is one small allocation ~23 times a second for a 42-byte header.
// Measured against the 3.28% of a core the decode itself takes, it does not
// register.
func (d *flacDecoder) decode(chunk []byte) ([]byte, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	stream, err := flac.Parse(io.MultiReader(
		bytes.NewReader(d.header), bytes.NewReader(chunk)))
	if err != nil {
		return nil, fmt.Errorf("sendspin: flac header: %w", err)
	}

	var out []byte
	for {
		f, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A partial frame at the end of a chunk means the server split
			// one across chunks, which the format does not allow for
			// independently-timestamped chunks. Return what decoded rather
			// than nothing: a gap is better than a stream that stops.
			if len(out) > 0 {
				return out, nil
			}
			return nil, fmt.Errorf("sendspin: flac frame: %w", err)
		}
		// The subframes' sample slices, and nothing else: keeping
		// appendMonoInt16 free of the decoder's types is what lets the two
		// conversions below be tested with hand-written numbers rather than
		// with a synthesised FLAC frame.
		chans := make([][]int32, len(f.Subframes))
		for i, sf := range f.Subframes {
			chans[i] = sf.Samples
		}
		out = appendMonoInt16(out, chans, int(f.BitsPerSample))
	}
	return out, nil
}

// appendMonoInt16 converts one frame's subframes to mono 16-bit samples.
//
// Two conversions, and each has a way of being subtly wrong:
//
//   - BIT DEPTH IS SHIFTED, not scaled. A 24-bit sample is >>8, not
//     multiplied by 32767/8388607 — the shift is exact, cheap, and what every
//     other converter does, so a stream that round-trips through this and
//     something else agrees with itself.
//   - CHANNELS ARE AVERAGED IN int32. Two channels near full scale sum past
//     32767 and wrap to full-scale negative in int16, which is a far worse
//     artefact than the clipping it looks like. The same rule the mixer and
//     the PCM downmix already follow.
func appendMonoInt16(out []byte, channels [][]int32, bps int) []byte {
	if len(channels) == 0 || len(channels[0]) == 0 {
		return out
	}
	n := len(channels[0])
	shift := bps - 16
	for i := 0; i < n; i++ {
		var sum int32
		for _, ch := range channels {
			if i >= len(ch) {
				continue
			}
			v := ch[i]
			if shift > 0 {
				v >>= uint(shift)
			} else if shift < 0 {
				v <<= uint(-shift)
			}
			sum += v
		}
		v := sum / int32(len(channels))
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(int16(v)))
	}
	return out
}
