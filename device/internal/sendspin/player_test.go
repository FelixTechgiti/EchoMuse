package sendspin

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEveryFormatWeMightAskForIsAdvertised(t *testing.T) {
	// The rule whose failure is silent. A request that does not match an
	// advertised tuple exactly makes the server log a warning on ITS side
	// and fall back to the base format; the client is told nothing and
	// keeps receiving what it had. client/hello is a one-shot, so a format
	// missing here cannot be added without reconnecting.
	//
	// Stereo is the case this pins. Nothing requests it today — the music
	// plane is mono end to end — and it becomes worth requesting the moment
	// there is a plug in the jack. Dropping it from the list because
	// "nothing uses it" is the change that makes a later jack insert appear
	// to work and change nothing.
	want := map[Format]bool{
		{Codec: CodecFLAC, Channels: 1, SampleRate: 48000, BitDepth: 16}: false,
		{Codec: CodecFLAC, Channels: 2, SampleRate: 48000, BitDepth: 16}: false,
		{Codec: CodecPCM, Channels: 1, SampleRate: 48000, BitDepth: 16}:  false,
		{Codec: CodecPCM, Channels: 2, SampleRate: 48000, BitDepth: 16}:  false,
	}
	for _, f := range SupportedFormats() {
		if _, ok := want[f]; !ok {
			t.Fatalf("advertised a format nothing can render: %+v", f)
		}
		want[f] = true
	}
	for f, seen := range want {
		if !seen {
			t.Fatalf("not advertised: %+v", f)
		}
	}
}

func TestFlacIsAdvertisedFirst(t *testing.T) {
	// Priority order, and it is a measurement: 3.68% of one core against
	// 0.97% for PCM, saving 929 kbps on links measured at 4.6–7.1% loss.
	if got := SupportedFormats()[0].Codec; got != CodecFLAC {
		t.Fatalf("first advertised codec = %q, want flac", got)
	}
}

func TestThePreferredFormatIsOneWeAdvertised(t *testing.T) {
	if !Supports(PreferredFormat()) {
		t.Fatalf("we would request %+v without advertising it", PreferredFormat())
	}
	if PreferredFormat().Channels != 1 {
		t.Fatalf("preferred channels = %d, want mono — the music plane is "+
			"mono end to end", PreferredFormat().Channels)
	}
}

func TestSupportsMatchesOnAllFourFieldsAtOnce(t *testing.T) {
	// The server's own match is the whole tuple. A client that checked only
	// the codec would accept a 44.1kHz stream, resample nothing, and play
	// everything 8.8% fast.
	base := PreferredFormat()
	for _, bad := range []Format{
		{Codec: base.Codec, Channels: base.Channels, SampleRate: 44100, BitDepth: base.BitDepth},
		{Codec: base.Codec, Channels: base.Channels, SampleRate: base.SampleRate, BitDepth: 24},
		{Codec: CodecOpus, Channels: base.Channels, SampleRate: base.SampleRate, BitDepth: base.BitDepth},
		{Codec: base.Codec, Channels: 6, SampleRate: base.SampleRate, BitDepth: base.BitDepth},
	} {
		if Supports(bad) {
			t.Fatalf("accepted a format we never advertised: %+v", bad)
		}
	}
}

func TestOpusIsNotAdvertised(t *testing.T) {
	// Deliberate: servers must support all three, so a subset is legal, and
	// the decoders worth having are cgo on a platform where every cgo
	// dependency is a cross-compilation problem. If this ever changes it
	// should be because someone decided to, not because a list grew.
	for _, f := range SupportedFormats() {
		if f.Codec == CodecOpus {
			t.Fatal("opus advertised — there is no decoder behind it")
		}
	}
}

func TestAStreamStartConvertsToTheTupleTheServerMatchedOn(t *testing.T) {
	s := StreamStart{Codec: CodecFLAC, SampleRate: 48000, Channels: 1, BitDepth: 16}
	if !Supports(s.Format()) {
		t.Fatalf("a stream/start for our own preferred format did not match")
	}
}

func TestBufferCapacityIsSizedAtWorstCaseCompression(t *testing.T) {
	// A lossless codec's worst case is 1:1, and FLAC approaches it on dense
	// loud material — precisely the content people turn up. Sizing against
	// the measured 40% average would leave the server free to overrun the
	// buffer exactly then.
	got := BufferCapacity(5.46, 1)
	want := int(5.46 * 48000 * 1 * 2)
	if got != want {
		t.Fatalf("BufferCapacity = %d, want %d", got, want)
	}
	if stereo := BufferCapacity(5.46, 2); stereo != 2*want {
		t.Fatalf("stereo capacity = %d, want %d", stereo, 2*want)
	}
}

func TestBufferCapacityRefusesNonsenseRatherThanReturningIt(t *testing.T) {
	for _, tc := range []struct {
		secs float64
		ch   int
	}{
		{0, 1}, {-1, 1}, {5, 0}, {5, -2},
	} {
		if got := BufferCapacity(tc.secs, tc.ch); got != 0 {
			t.Fatalf("BufferCapacity(%v, %d) = %d, want 0", tc.secs, tc.ch, got)
		}
	}
}

func TestVolumeIsThePerceptualCurveFromTheSpec(t *testing.T) {
	// In the spec rather than left to the client so one volume value sounds
	// the same in every room. A client applying its own curve is a speaker
	// audibly louder than the rest of the group at the same setting.
	for _, v := range []int{1, 10, 25, 50, 75, 99} {
		want := math.Pow(float64(v)/100, 1.5)
		if got := VolumeAmplitude(v); math.Abs(got-want) > 1e-12 {
			t.Fatalf("VolumeAmplitude(%d) = %v, want %v", v, got, want)
		}
	}
	if VolumeAmplitude(0) != 0 {
		t.Fatal("volume 0 is not silent")
	}
	if VolumeAmplitude(100) != 1 {
		t.Fatal("volume 100 is not unity")
	}
	if VolumeAmplitude(-5) != 0 || VolumeAmplitude(150) != 1 {
		t.Fatal("out-of-range volume was not clamped")
	}
}

func TestVolumeIsMonotonic(t *testing.T) {
	prev := -1.0
	for v := 0; v <= 100; v++ {
		got := VolumeAmplitude(v)
		if got < prev {
			t.Fatalf("volume %d is quieter than %d", v, v-1)
		}
		prev = got
	}
}

func TestTheWireNamesAreTheSpecsNames(t *testing.T) {
	// Every one of these is matched by string on the far side, so a rename
	// during a refactor is a silent protocol break.
	raw, err := json.Marshal(PlayerSupport{
		SupportedFormats: []Format{PreferredFormat()},
		BufferCapacity:   1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"supported_formats"`, `"buffer_capacity"`,
		`"codec"`, `"channels"`, `"sample_rate"`, `"bit_depth"`,
	} {
		if !contains(string(raw), key) {
			t.Fatalf("client/hello support object is missing %s: %s", key, raw)
		}
	}

	state, err := json.Marshal(PlayerState{SupportedCommands: []string{CommandVolume}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"output_delay_ms"`, `"required_lead_time_ms"`, `"min_buffer_ms"`,
		`"supported_commands"`,
	} {
		if !contains(string(state), key) {
			t.Fatalf("client/state player object is missing %s: %s", key, state)
		}
	}
}

func TestAnUnsetVolumeIsOmittedRatherThanSentAsZero(t *testing.T) {
	// volume is required only when 'volume' is in supported_commands, and a
	// zero sent by accident is a speaker the group believes is silent.
	raw, err := json.Marshal(PlayerState{})
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(raw), `"volume"`) || contains(string(raw), `"muted"`) {
		t.Fatalf("unset volume/mute reached the wire: %s", raw)
	}
}

func TestTheCodecHeaderIsOmittedForNonFlacStreams(t *testing.T) {
	raw, err := json.Marshal(StreamStart{Codec: CodecPCM, SampleRate: 48000,
		Channels: 1, BitDepth: 16})
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(raw), "codec_header") {
		t.Fatalf("a PCM stream/start carries a codec header: %s", raw)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
