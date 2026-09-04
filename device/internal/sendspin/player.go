package sendspin

import "math"

// The player role, v1.
//
// These are the wire shapes, and the one decision in this file that is not a
// transcription is which formats to advertise. That decision has a trap in it
// worth stating before the code:
//
// ⚠ A server's format request must EXACTLY match something the client
// advertised in client/hello, and when it does not the failure is SILENT.
// The server filters the request against the advertised list, logs a warning
// ON THE SERVER, and falls back to the base format. The client is told
// nothing and simply keeps receiving what it had. So a client that advertises
// mono and later asks for stereo — on a jack insert, say — appears to work,
// changes nothing, and leaves no trace on the device.
//
// client/hello is a ONE-SHOT at connection setup, so the list cannot be
// extended later without reconnecting. Everything this device might ever ask
// for therefore goes in the first hello, whether or not it can render it
// today: an advertised format costs a few bytes, and an unadvertised one
// costs a silent failure on somebody's hardware.

// Codec names, as they appear on the wire.
const (
	CodecPCM  = "pcm"
	CodecFLAC = "flac"
	CodecOpus = "opus"
)

// PlayerRole is the role identifier this client activates.
const PlayerRole = "player@v1"

// Format is one entry of supported_formats, and the tuple the server matches
// a request against. All four fields participate in the match.
type Format struct {
	Codec      string `json:"codec"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   int    `json:"bit_depth"`
}

// PlayerSupport is the player@v1 object in client/hello.
type PlayerSupport struct {
	// SupportedFormats is in PRIORITY ORDER — the server picks from it, so
	// the first entry is the one to prefer.
	SupportedFormats []Format `json:"supported_formats"`
	// BufferCapacity is the most COMPRESSED audio, in bytes, this client
	// will hold un-played. The server's pacing is built against it.
	BufferCapacity int `json:"buffer_capacity"`
}

// The device's audio parameters. 48kHz because that is what the codec runs at
// and what every other plane already carries; 16-bit because that is what the
// ALSA write takes and a 24-bit stream would only be truncated.
const (
	SampleRate = 48000
	BitDepth   = 16
)

// SupportedFormats returns what this device advertises, in priority order.
//
// FLAC first, and that is a measurement rather than a preference. On this
// hardware, per second of audio: FLAC decode 3.28% of one core, ChaCha20-
// Poly1305 at the FLAC rate 0.40%, total 3.68% — against 0.97% for PCM, which
// costs 929 kbps more on the wire. This fleet's links are measured at 4.6–7.1%
// packet loss (#139/#140), and trading a link known to be marginal against a
// core with 3.68% of room on it is not a close call.
//
// PCM is advertised as a fallback for the same reason both channel counts are:
// unadvertised is unrequestable, and the failure is silent.
//
// Opus is deliberately absent. Servers MUST support all three, so a client may
// advertise a subset — and the only Opus decoders worth having are cgo, on a
// platform (armv7a, API 22) where every cgo dependency is a cross-compilation
// problem. It is a bandwidth optimisation to take later, not an entry cost.
//
// STEREO IS ADVERTISED THOUGH NOTHING REQUESTS IT YET. The music plane is mono
// end to end — PumpMusic takes mono periods and the ALSA write duplicates L=R,
// which is an I2S constraint rather than a wire one — so this client asks for
// mono. Stereo becomes worth asking for when a plug appears in the jack, and
// mid-stream renegotiation is implemented server-side, so the request will be
// honoured. It would be honoured silently and wrongly if the format were not
// already in this list, and by then the hello is long gone.
func SupportedFormats() []Format {
	return []Format{
		{Codec: CodecFLAC, Channels: 1, SampleRate: SampleRate, BitDepth: BitDepth},
		{Codec: CodecFLAC, Channels: 2, SampleRate: SampleRate, BitDepth: BitDepth},
		{Codec: CodecPCM, Channels: 1, SampleRate: SampleRate, BitDepth: BitDepth},
		{Codec: CodecPCM, Channels: 2, SampleRate: SampleRate, BitDepth: BitDepth},
	}
}

// PreferredFormat is the one this client asks for while the speaker is the
// only output: mono FLAC. It must be present in SupportedFormats, and a test
// pins that rather than trusting the two to be edited together.
func PreferredFormat() Format { return SupportedFormats()[0] }

// Supports reports whether a format is one this client advertised — the same
// four-field match the server performs. Used to check a stream/start before
// acting on it: the server falling back silently is its behaviour on a
// mismatch, and a client that decodes whatever arrives as whatever it last
// asked for turns that into noise instead of a log line.
func Supports(f Format) bool {
	for _, s := range SupportedFormats() {
		if s == f {
			return true
		}
	}
	return false
}

// BufferCapacity states, in bytes, how much un-played compressed audio this
// client will hold. The server's pacing is built against it: too small and it
// starves us, too large and it overruns the buffer.
//
// Derived from the device's own buffer rather than picked. The music plane
// holds audioChanDepth periods — ~5.46s — and the honest conversion to
// "compressed bytes" is at the WORST-CASE compression ratio, which for a
// lossless codec is 1:1. FLAC on quiet or simple material runs at 40% of PCM
// and on dense loud material approaches PCM exactly, so sizing against the
// measured average would leave the server free to overrun us on precisely the
// content people turn up.
//
// bufferSeconds is passed in rather than read from the speaker package: this
// package is untagged and host-testable, and importing a cgo binding to learn
// one constant would end that.
func BufferCapacity(bufferSeconds float64, channels int) int {
	if bufferSeconds <= 0 || channels <= 0 {
		return 0
	}
	bytesPerSecond := float64(SampleRate) * float64(channels) * float64(BitDepth/8)
	return int(bufferSeconds * bytesPerSecond)
}

// PlayerState is the player object in client/state.
type PlayerState struct {
	Volume *int  `json:"volume,omitempty"`
	Muted  *bool `json:"muted,omitempty"`
	// OutputDelayMs (0–5000) is the client's own write-to-ear latency, which
	// the server subtracts when scheduling. It is not a fudge factor for
	// clock error — that is the time filter's job — and using it as one
	// makes this device play early or late relative to a group that is
	// otherwise correct.
	OutputDelayMs int `json:"output_delay_ms"`
	// RequiredLeadTimeMs is how far ahead of playback a chunk must arrive.
	RequiredLeadTimeMs int `json:"required_lead_time_ms"`
	// MinBufferMs is how much audio must be queued before playback starts —
	// the same prime gate the 0x04 plane already has, expressed to the
	// server so it can fill it.
	MinBufferMs int `json:"min_buffer_ms"`
	// SupportedCommands: 'volume', 'mute', 'set_output_delay'. A command
	// listed here MUST have its state field populated, and the two are
	// built together by PlayerStateFor for that reason.
	SupportedCommands []string `json:"supported_commands"`
	Format            *Format  `json:"format,omitempty"`
}

// Command names for supported_commands.
const (
	CommandVolume         = "volume"
	CommandMute           = "mute"
	CommandSetOutputDelay = "set_output_delay"
)

// StreamStart is the player object in a stream/start message.
type StreamStart struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	BitDepth   int    `json:"bit_depth"`
	// CodecHeader is base64 and FLAC-only: the STREAMINFO the decoder needs
	// before the first chunk, because the chunks themselves carry frames
	// rather than a stream header.
	CodecHeader string `json:"codec_header,omitempty"`
}

// Format returns the tuple to match against what was advertised.
func (s StreamStart) Format() Format {
	return Format{Codec: s.Codec, Channels: s.Channels,
		SampleRate: s.SampleRate, BitDepth: s.BitDepth}
}

// VolumeAmplitude converts a Sendspin volume (0–100) to a linear amplitude.
//
// The scaling is perceptual and specified: amplitude = (volume/100)^1.5. It
// is in the spec rather than left to the client so that one volume value
// sounds the same on every speaker in a group — a client applying its own
// curve is a room that is audibly louder than the others at the same setting.
func VolumeAmplitude(volume int) float64 {
	if volume <= 0 {
		return 0
	}
	if volume >= 100 {
		return 1
	}
	// Written as math.Pow with the exponent from the spec, rather than as
	// v*sqrt(v), so the line can be checked against the specification
	// without doing algebra.
	return math.Pow(float64(volume)/100, 1.5)
}
