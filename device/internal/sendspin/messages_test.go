package sendspin

import (
	"encoding/json"
	"strings"
	"testing"
)

// jsonOf marshals and returns the text, for the many assertions below that
// are about the WIRE rather than about Go.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestClientHelloCarriesTheFieldsTheServerMatchesOn(t *testing.T) {
	one := 1
	got := jsonOf(t, ClientHello{
		Name:           "Lounge",
		SupportedRoles: []string{PlayerRole},
		TrustLevel:     TrustNone,
		DeviceInfo:     &DeviceInfo{ProductName: "Echo Dot Gen 2"},
		ClientID:       "abc",
		Version:        &one,
		PlayerSupport: &PlayerSupport{
			SupportedFormats: SupportedFormats(),
			BufferCapacity:   1024,
		},
		UnpairedAccess: UnpairedAccess{Enabled: true},
	})
	for _, key := range []string{
		`"name"`, `"supported_roles"`, `"trust_level"`, `"device_info"`,
		`"client_id"`, `"version"`, `"player@v1_support"`, `"unpaired_access"`,
	} {
		if !strings.Contains(got, key) {
			t.Fatalf("client/hello is missing %s: %s", key, got)
		}
	}
}

func TestThePlayerSupportKeyIsTheAliasedOne(t *testing.T) {
	// It is `player@v1_support`, not `player_support` and not `player`. The
	// role version is part of the key, so a v2 role is a different field
	// rather than a different value — and a server that does not find this
	// key activates no player role at all, which presents as a client that
	// connects fine and never receives audio.
	got := jsonOf(t, ClientHello{PlayerSupport: &PlayerSupport{}})
	if !strings.Contains(got, `"player@v1_support"`) {
		t.Fatalf("support key is wrong: %s", got)
	}
}

func TestUnpairedAccessIsAlwaysSentEvenWhenFalse(t *testing.T) {
	// Absent and false are the same thing to the server here, but sending it
	// explicitly is what makes the setting visible in a capture when
	// somebody is working out why a device will not connect.
	got := jsonOf(t, ClientHello{Name: "x"})
	if !strings.Contains(got, `"unpaired_access"`) {
		t.Fatalf("unpaired_access omitted: %s", got)
	}
}

func TestAnUnsetAvailableIsOmittedRatherThanSentAsFalse(t *testing.T) {
	// The spec forbids available:true before the clock is synced, so there
	// is a real state where the answer is "not yet". Collapsing it to false
	// tells the server this speaker is broken rather than starting up.
	if got := jsonOf(t, ClientState{}); strings.Contains(got, `"available"`) {
		t.Fatalf("unset available reached the wire: %s", got)
	}
	no := false
	if got := jsonOf(t, ClientState{Available: &no}); !strings.Contains(got, `"available":false`) {
		t.Fatalf("explicit false did not reach the wire: %s", got)
	}
}

func TestAPlayerCommandDistinguishesAbsentFromZero(t *testing.T) {
	// A command setting volume alone must not read as "and unmute", and 0 is
	// a legitimate volume.
	zero := 0
	got := jsonOf(t, ServerCommand{Player: &PlayerCommand{Volume: &zero}})
	if !strings.Contains(got, `"volume":0`) {
		t.Fatalf("volume 0 was dropped: %s", got)
	}
	if strings.Contains(got, `"muted"`) {
		t.Fatalf("an unset mute reached the wire: %s", got)
	}
}

func TestServerCommandDecodesAPartialUpdate(t *testing.T) {
	var cmd ServerCommand
	if err := json.Unmarshal([]byte(`{"player":{"muted":true}}`), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.Player == nil || cmd.Player.Muted == nil || !*cmd.Player.Muted {
		t.Fatalf("mute did not decode: %+v", cmd.Player)
	}
	if cmd.Player.Volume != nil {
		t.Fatalf("volume invented a value: %v", *cmd.Player.Volume)
	}
}

func TestAnAbsentRolesListMeansEveryRole(t *testing.T) {
	// The case that would otherwise read as "no roles, ignore this".
	// Ignoring a stream/clear leaves the device playing audio the server has
	// already moved past — a seek that plays the old position.
	if !AppliesToPlayer(nil) {
		t.Fatal("a nil roles list excluded the player")
	}
	if !AppliesToPlayer([]string{}) {
		t.Fatal("an empty roles list excluded the player")
	}
	if !AppliesToPlayer([]string{"metadata", PlayerRole}) {
		t.Fatal("an explicit player role was not matched")
	}
	if !AppliesToPlayer([]string{"player"}) {
		t.Fatal("the unversioned role name was not matched")
	}
	if AppliesToPlayer([]string{"artwork", "metadata"}) {
		t.Fatal("a clear for other roles was applied to the player")
	}
}

func TestTheTimeMessagesUseTheSpecsFieldNames(t *testing.T) {
	// Three of the four timestamps arrive in this message. A misspelled key
	// decodes as zero, which the filter reads as a colossal offset rather
	// than as a missing field.
	var st ServerTime
	err := json.Unmarshal([]byte(
		`{"client_transmitted":1,"server_received":2,"server_transmitted":3}`), &st)
	if err != nil {
		t.Fatal(err)
	}
	if st.ClientTransmitted != 1 || st.ServerReceived != 2 || st.ServerTransmitted != 3 {
		t.Fatalf("server/time decoded as %+v", st)
	}
	if got := jsonOf(t, ClientTime{ClientTransmitted: 7}); got != `{"client_transmitted":7}` {
		t.Fatalf("client/time = %s", got)
	}
}

func TestGoodbyeReasonsAreTheClosedSetTheServerKnows(t *testing.T) {
	// A reason outside this set is not a richer message; it is one the
	// server will not understand, on the one message whose entire purpose is
	// telling it why we left.
	known := map[string]bool{
		GoodbyeAnotherServer: true, GoodbyeShutdown: true, GoodbyeRestart: true,
		GoodbyeUserRequest: true, GoodbyeUnauthorized: true,
		GoodbyePairingRequired: true, GoodbyeConcurrentAttempt: true,
		GoodbyeUnpaired: true,
	}
	if len(known) != 8 {
		t.Fatalf("the reason set changed size: %d", len(known))
	}
	if got := jsonOf(t, ClientGoodbye{Reason: GoodbyeUserRequest}); got != `{"reason":"user_request"}` {
		t.Fatalf("client/goodbye = %s", got)
	}
}

func TestAStreamStartDecodesIntoTheFormatTuple(t *testing.T) {
	var msg StreamStartMsg
	err := json.Unmarshal([]byte(`{"server_transmitted":99,"player":{
		"codec":"flac","sample_rate":48000,"channels":1,"bit_depth":16,
		"codec_header":"ZkxhQw=="}}`), &msg)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ServerTransmitted != 99 || msg.Player == nil {
		t.Fatalf("decoded as %+v", msg)
	}
	if !Supports(msg.Player.Format()) {
		t.Fatalf("a stream/start for %+v did not match anything advertised",
			msg.Player.Format())
	}
	if msg.Player.CodecHeader == "" {
		t.Fatal("the FLAC codec header was dropped — the decoder needs it before the first chunk")
	}
}

func TestAMessageWithNoPayloadIsNotAnError(t *testing.T) {
	// Several messages legitimately carry nothing, and an error there would
	// tear down a healthy connection.
	var hello ServerHello
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("null")} {
		if err := decodePayload(raw, &hello); err != nil {
			t.Fatalf("payload %q: %v", raw, err)
		}
	}
}

func TestChaChaIsTheCipherWeName(t *testing.T) {
	// The A53 in this device has no AES instructions, so AESGCM is a
	// software AES on the audio path. Servers support both.
	if CipherChaChaPoly != "25519_ChaChaPoly_SHA256" {
		t.Fatalf("cipher suite name = %q", CipherChaChaPoly)
	}
}
