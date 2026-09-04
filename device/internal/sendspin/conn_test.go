package sendspin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeTransport is a scripted server. The whole handshake is exercised
// through it, which is the point: a state machine that can only be tested by
// playing music in a room is one nobody tests.
type fakeTransport struct {
	mu     sync.Mutex
	sent   [][]byte
	in     chan []byte
	closed bool
}

func newFake() *fakeTransport {
	return &fakeTransport{in: make(chan []byte, 32)}
}

func (f *fakeTransport) Send(frame []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return io.ErrClosedPipe
	}
	f.sent = append(f.sent, append([]byte(nil), frame...))
	return nil
}

func (f *fakeTransport) Recv() ([]byte, error) {
	frame, ok := <-f.in
	if !ok {
		return nil, io.EOF
	}
	return frame, nil
}

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.in)
	}
	return nil
}

// serverSends queues a control message from the scripted server.
func (f *fakeTransport) serverSends(t *testing.T, msgType string, payload any) {
	t.Helper()
	frame, err := EncodeJSON(msgType, payload)
	if err != nil {
		t.Fatal(err)
	}
	f.in <- frame
}

func (f *fakeTransport) serverSendsRaw(frame []byte) { f.in <- frame }

// types returns the message types the client sent, in order.
func (f *fakeTransport) types(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, frame := range f.sent {
		if len(frame) == 0 || frame[0] != TypeJSON {
			out = append(out, "<binary>")
			continue
		}
		env, err := DecodeEnvelope(frame[1:])
		if err != nil {
			t.Fatalf("client sent unparseable JSON: %s", frame[1:])
		}
		out = append(out, env.Type)
	}
	return out
}

// payloadOf returns the payload of the client's nth message of a type.
func (f *fakeTransport) payloadOf(t *testing.T, msgType string, into any) bool {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, frame := range f.sent {
		if len(frame) == 0 || frame[0] != TypeJSON {
			continue
		}
		env, err := DecodeEnvelope(frame[1:])
		if err != nil || env.Type != msgType {
			continue
		}
		if err := json.Unmarshal(env.Payload, into); err != nil {
			t.Fatal(err)
		}
		return true
	}
	return false
}

// recordHandler records every callback.
type recordHandler struct {
	mu       sync.Mutex
	events   []string
	starts   []StreamStart
	chunks   []AudioChunk
	commands []PlayerCommand
	groups   []GroupUpdate
	activate []ServerActivate
}

func (h *recordHandler) note(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, s)
}
func (h *recordHandler) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

// Every field below is written on the connection's read goroutine and read
// from the test's, so each one needs the lock. Reading them directly is a
// data race, and the race detector is the only thing that reports it — the
// tests pass either way.
func (h *recordHandler) gotStreams() []StreamStart {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]StreamStart(nil), h.starts...)
}
func (h *recordHandler) gotChunks() []AudioChunk {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]AudioChunk(nil), h.chunks...)
}
func (h *recordHandler) gotCommands() []PlayerCommand {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]PlayerCommand(nil), h.commands...)
}
func (h *recordHandler) gotGroups() []GroupUpdate {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]GroupUpdate(nil), h.groups...)
}
func (h *recordHandler) gotActivations() []ServerActivate {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ServerActivate(nil), h.activate...)
}
func (h *recordHandler) OnActivate(a ServerActivate) {
	h.mu.Lock()
	h.activate = append(h.activate, a)
	h.mu.Unlock()
	h.note("activate")
}
func (h *recordHandler) OnStreamStart(s StreamStart) {
	h.mu.Lock()
	h.starts = append(h.starts, s)
	h.mu.Unlock()
	h.note("start")
}
func (h *recordHandler) OnAudio(c AudioChunk) {
	h.mu.Lock()
	cp := c
	cp.Data = append([]byte(nil), c.Data...)
	h.chunks = append(h.chunks, cp)
	h.mu.Unlock()
	h.note("audio")
}
func (h *recordHandler) OnStreamClear() { h.note("clear") }
func (h *recordHandler) OnStreamEnd()   { h.note("end") }
func (h *recordHandler) OnCommand(c PlayerCommand) {
	h.mu.Lock()
	h.commands = append(h.commands, c)
	h.mu.Unlock()
	h.note("command")
}
func (h *recordHandler) OnGroupUpdate(g GroupUpdate) {
	h.mu.Lock()
	h.groups = append(h.groups, g)
	h.mu.Unlock()
	h.note("group")
}
func (h *recordHandler) OnSynced() { h.note("synced") }

// session starts a connection against a scripted server and returns
// everything the test needs, plus a stop function.
func session(t *testing.T) (*fakeTransport, *recordHandler, *Conn, func()) {
	t.Helper()
	f := newFake()
	h := &recordHandler{}
	c := NewConn(f, Options{
		Identity:      Identity{Name: "Lounge", ClientID: "dev-1"},
		Handler:       h,
		BufferSeconds: 5.46,
		MinBufferMs:   1000,
		// Long enough that the periodic exchange never fires on its own —
		// every test that needs a measurement scripts one.
		TimeSyncInterval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	return f, h, c, func() {
		cancel()
		f.Close()
		<-done
	}
}

// settle lets the connection goroutine drain the queued frames. Crude, and
// the alternative is threading a synchronisation point through the read loop
// purely for tests, which would be a change to the code under test.
func settle() { time.Sleep(30 * time.Millisecond) }

func TestTheFirstThingSentIsClientInit(t *testing.T) {
	f, _, _, stop := session(t)
	defer stop()
	settle()

	got := f.types(t)
	if len(got) == 0 || got[0] != MsgClientInit {
		t.Fatalf("first message = %v, want client/init", got)
	}
	var init ClientInit
	if !f.payloadOf(t, MsgClientInit, &init) {
		t.Fatal("client/init carried no payload")
	}
	if len(init.CipherSuites) == 0 {
		t.Fatal("client/init advertised no cipher suites")
	}
}

func TestClientHelloAnswersServerHelloAndNothingEarlier(t *testing.T) {
	// The ordering mistake the spec's own sequence makes easy. client/hello
	// is the one-shot carrying the format list; sending it before the server
	// has introduced itself loses it, and it cannot be revised without
	// reconnecting.
	f, _, _, stop := session(t)
	defer stop()
	settle()

	f.serverSends(t, MsgServerInit, ServerInit{PSKID: "x", PSKCategory: "sentinel"})
	settle()
	for _, m := range f.types(t) {
		if m == MsgClientHello {
			t.Fatal("client/hello went out before server/hello")
		}
	}

	f.serverSends(t, MsgServerHello, ServerHello{Name: "Music Assistant"})
	settle()

	var hello ClientHello
	if !f.payloadOf(t, MsgClientHello, &hello) {
		t.Fatalf("no client/hello after server/hello; sent %v", f.types(t))
	}
	if hello.Name != "Lounge" || hello.ClientID != "dev-1" {
		t.Fatalf("client/hello identity = %+v", hello)
	}
	if hello.PlayerSupport == nil || len(hello.PlayerSupport.SupportedFormats) == 0 {
		t.Fatal("client/hello advertised no formats")
	}
	if hello.PlayerSupport.BufferCapacity <= 0 {
		t.Fatal("buffer_capacity was not derived")
	}
}

func TestTheFormatListInHelloIsTheWholeAdvertisedSet(t *testing.T) {
	// Not just the one we intend to request. An unadvertised format is
	// unrequestable, and the failure is silent.
	f, _, _, stop := session(t)
	defer stop()
	settle()
	f.serverSends(t, MsgServerHello, ServerHello{Name: "MA"})
	settle()

	var hello ClientHello
	if !f.payloadOf(t, MsgClientHello, &hello) {
		t.Fatal("no client/hello")
	}
	if len(hello.PlayerSupport.SupportedFormats) != len(SupportedFormats()) {
		t.Fatalf("advertised %d formats, want all %d",
			len(hello.PlayerSupport.SupportedFormats), len(SupportedFormats()))
	}
}

func TestStateGoesOutOnActivateWithoutClaimingToBeAvailable(t *testing.T) {
	// The spec forbids available:true before the clock is synced. Absent
	// means "not yet"; false would mean "this speaker will not play", and a
	// group told that moves on without it.
	f, h, _, stop := session(t)
	defer stop()
	settle()
	f.serverSends(t, MsgServerActivate, ServerActivate{
		Activities: []string{ActivityPlayback}, ActiveRoles: []string{PlayerRole}})
	settle()

	if len(h.gotActivations()) != 1 {
		t.Fatalf("activate callbacks = %d", len(h.gotActivations()))
	}
	var raw map[string]any
	if !f.payloadOf(t, MsgClientState, &raw) {
		t.Fatalf("no client/state after activate; sent %v", f.types(t))
	}
	if _, ok := raw["available"]; ok {
		t.Fatalf("claimed availability before syncing: %v", raw)
	}
}

// timeExchange answers the client's most recent client/time with a server
// clock `offsetUs` ahead.
func timeExchange(t *testing.T, f *fakeTransport, c *Conn, offsetUs int64) {
	t.Helper()
	if err := c.sendTimeRequest(); err != nil {
		t.Fatal(err)
	}
	settle()
	f.mu.Lock()
	last := f.sent[len(f.sent)-1]
	f.mu.Unlock()
	env, err := DecodeEnvelope(last[1:])
	if err != nil || env.Type != MsgClientTime {
		t.Fatalf("last sent was %v, want client/time", env.Type)
	}
	var ct ClientTime
	if err := json.Unmarshal(env.Payload, &ct); err != nil {
		t.Fatal(err)
	}
	f.serverSends(t, MsgServerTime, ServerTime{
		ClientTransmitted: ct.ClientTransmitted,
		ServerReceived:    ct.ClientTransmitted + offsetUs + 500,
		ServerTransmitted: ct.ClientTransmitted + offsetUs + 550,
	})
	settle()
}

func TestAvailabilityIsClaimedOnlyOnceTheClockIsSynced(t *testing.T) {
	f, h, c, stop := session(t)
	defer stop()
	settle()

	timeExchange(t, f, c, 1_000_000)
	if got := h.seen(); contains(joinAll(got), "synced") {
		t.Fatal("declared synced after one measurement — drift is still unknown")
	}

	timeExchange(t, f, c, 1_000_000)
	if !contains(joinAll(h.seen()), "synced") {
		t.Fatalf("never synced; events %v", h.seen())
	}

	var raw map[string]any
	f.mu.Lock()
	frames := append([][]byte(nil), f.sent...)
	f.mu.Unlock()
	found := false
	for _, frame := range frames {
		if frame[0] != TypeJSON {
			continue
		}
		env, _ := DecodeEnvelope(frame[1:])
		if env.Type != MsgClientState {
			continue
		}
		raw = nil
		if err := json.Unmarshal(env.Payload, &raw); err != nil {
			t.Fatal(err)
		}
		if v, ok := raw["available"]; ok && v == true {
			found = true
		}
	}
	if !found {
		t.Fatal("never reported available after syncing")
	}
	if !c.Clock().Ready {
		t.Fatal("the clock snapshot is not ready after syncing")
	}
}

func TestAudioOutsideAStreamIsDropped(t *testing.T) {
	// It has no format behind it. Decoding it as whatever the last stream
	// used is how a codec change produces noise instead of a gap.
	f, h, _, stop := session(t)
	defer stop()
	settle()

	f.serverSendsRaw(EncodeAudioChunk(AudioChunk{Timestamp: 1, Data: []byte{1, 2, 3}}))
	settle()
	if len(h.gotChunks()) != 0 {
		t.Fatalf("played %d chunks with no stream open", len(h.gotChunks()))
	}
}

func TestAStreamOpensAndCarriesAudioThenEnds(t *testing.T) {
	f, h, _, stop := session(t)
	defer stop()
	settle()

	fmtWant := PreferredFormat()
	f.serverSends(t, MsgStreamStart, StreamStartMsg{
		ServerTransmitted: 42,
		Player: &StreamStart{Codec: fmtWant.Codec, SampleRate: fmtWant.SampleRate,
			Channels: fmtWant.Channels, BitDepth: fmtWant.BitDepth,
			CodecHeader: "ZkxhQw=="},
	})
	settle()
	f.serverSendsRaw(EncodeAudioChunk(AudioChunk{Timestamp: 777, SendAhead: 5, Data: []byte{9, 9}}))
	settle()
	f.serverSends(t, MsgStreamEnd, StreamEnd{ServerTransmitted: 50})
	settle()

	if len(h.gotStreams()) != 1 || h.gotStreams()[0].CodecHeader != "ZkxhQw==" {
		t.Fatalf("stream/start = %+v", h.gotStreams())
	}
	if len(h.gotChunks()) != 1 || h.gotChunks()[0].Timestamp != 777 {
		t.Fatalf("chunks = %+v", h.gotChunks())
	}
	if got := joinAll(h.seen()); !contains(got, "end") {
		t.Fatalf("no stream end; events %v", h.seen())
	}

	// And audio after the end is dropped again.
	f.serverSendsRaw(EncodeAudioChunk(AudioChunk{Timestamp: 888}))
	settle()
	if len(h.gotChunks()) != 1 {
		t.Fatalf("played audio after the stream ended: %d chunks", len(h.gotChunks()))
	}
}

func TestAStreamInAFormatWeNeverAdvertisedIsAnError(t *testing.T) {
	// The server falls back silently on a mismatch, so this is the only
	// place it can be noticed at all. Decoding it as whatever we last asked
	// for turns a log line into noise from the speaker.
	f := newFake()
	h := &recordHandler{}
	c := NewConn(f, Options{Handler: h, BufferSeconds: 5, TimeSyncInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx) }()

	f.serverSends(t, MsgStreamStart, StreamStartMsg{
		Player: &StreamStart{Codec: CodecFLAC, SampleRate: 44100, Channels: 1, BitDepth: 16},
	})

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrFormatNotAdvertised) {
			t.Fatalf("err = %v, want ErrFormatNotAdvertised", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an unadvertised format was accepted")
	}
}

func TestAClearIsNotAnEnd(t *testing.T) {
	// A seek. Treating it as an end leaves the device silent for the rest of
	// the track.
	f, h, _, stop := session(t)
	defer stop()
	settle()

	fw := PreferredFormat()
	f.serverSends(t, MsgStreamStart, StreamStartMsg{Player: &StreamStart{
		Codec: fw.Codec, SampleRate: fw.SampleRate, Channels: fw.Channels, BitDepth: fw.BitDepth}})
	settle()
	f.serverSends(t, MsgStreamClear, StreamClear{ServerTransmitted: 1})
	settle()
	f.serverSendsRaw(EncodeAudioChunk(AudioChunk{Timestamp: 5}))
	settle()

	if got := joinAll(h.seen()); !contains(got, "clear") {
		t.Fatalf("no clear; events %v", h.seen())
	}
	if len(h.gotChunks()) != 1 {
		t.Fatalf("the stream did not survive the clear: %d chunks", len(h.gotChunks()))
	}
}

func TestAClearForOtherRolesIsIgnored(t *testing.T) {
	f, h, _, stop := session(t)
	defer stop()
	settle()
	f.serverSends(t, MsgStreamClear, StreamClear{Roles: []string{"artwork"}})
	settle()
	if contains(joinAll(h.seen()), "clear") {
		t.Fatalf("an artwork clear reached the player: %v", h.seen())
	}
}

func TestCommandsAndGroupUpdatesReachTheHandler(t *testing.T) {
	f, h, _, stop := session(t)
	defer stop()
	settle()

	vol := 42
	f.serverSends(t, MsgServerCommand, ServerCommand{Player: &PlayerCommand{Volume: &vol}})
	f.serverSends(t, MsgGroupUpdate, GroupUpdate{
		PlaybackState: PlaybackPlaying, GroupName: "Downstairs"})
	settle()

	if len(h.gotCommands()) != 1 || h.gotCommands()[0].Volume == nil || *h.gotCommands()[0].Volume != 42 {
		t.Fatalf("commands = %+v", h.gotCommands())
	}
	if len(h.gotGroups()) != 1 || h.gotGroups()[0].GroupName != "Downstairs" {
		t.Fatalf("groups = %+v", h.gotGroups())
	}
}

func TestAnUnknownMessageIsIgnoredRatherThanFatal(t *testing.T) {
	// What lets a newer server talk to firmware that shipped before the
	// message existed.
	f, h, _, stop := session(t)
	defer stop()
	settle()

	f.serverSends(t, "server/invented-later", map[string]any{"whatever": 1})
	f.serverSendsRaw([]byte{99, 1, 2, 3}) // a role we did not activate
	f.serverSends(t, MsgGroupUpdate, GroupUpdate{GroupName: "still here"})
	settle()

	if len(h.gotGroups()) != 1 {
		t.Fatalf("the connection stopped processing: groups = %d", len(h.gotGroups()))
	}
}

func TestGoodbyeIsSentOnceAndCarriesItsReason(t *testing.T) {
	// The paths that call it overlap — a preemption by Home Assistant and
	// the connection's own teardown both end here — and a second goodbye on
	// a closed transport is an error nobody can act on.
	f, _, c, stop := session(t)
	defer stop()
	settle()

	if err := c.Goodbye(GoodbyeUserRequest); err != nil {
		t.Fatal(err)
	}
	if err := c.Goodbye(GoodbyeShutdown); err != nil {
		t.Fatalf("the second goodbye errored: %v", err)
	}

	n := 0
	for _, m := range f.types(t) {
		if m == MsgClientGoodbye {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("sent %d goodbyes, want 1", n)
	}
	var bye ClientGoodbye
	if !f.payloadOf(t, MsgClientGoodbye, &bye) || bye.Reason != GoodbyeUserRequest {
		t.Fatalf("goodbye = %+v", bye)
	}
}

func TestNoCommandsAreClaimedThatThisDeviceDoesNotImplement(t *testing.T) {
	// Volume and mute here are owned by the controller and the button.
	// Claiming a command obliges this client to carry its state field, and a
	// control the server offers that the device ignores is the failure the
	// capability rules exist to prevent.
	f, _, _, stop := session(t)
	defer stop()
	settle()
	f.serverSends(t, MsgServerActivate, ServerActivate{Activities: []string{ActivityPlayback}})
	settle()

	var state ClientState
	if !f.payloadOf(t, MsgClientState, &state) {
		t.Fatal("no client/state")
	}
	if state.Player == nil || len(state.Player.SupportedCommands) != 0 {
		t.Fatalf("claimed commands: %+v", state.Player)
	}
}

func TestAFragmentedControlMessageIsReassembled(t *testing.T) {
	f, h, _, stop := session(t)
	defer stop()
	settle()

	big := make([]byte, FragmentThreshold+500)
	for i := range big {
		big[i] = 'a'
	}
	frame, err := EncodeJSON(MsgGroupUpdate, GroupUpdate{GroupName: string(big)})
	if err != nil {
		t.Fatal(err)
	}
	frames := Fragment(frame)
	if len(frames) < 2 {
		t.Fatal("test setup: the message was not fragmented")
	}
	for _, fr := range frames {
		f.serverSendsRaw(fr)
	}
	settle()

	if len(h.gotGroups()) != 1 || len(h.gotGroups()[0].GroupName) != len(big) {
		t.Fatalf("fragmented message did not reassemble: %d groups", len(h.gotGroups()))
	}
}

func joinAll(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s + ","
	}
	return out
}
