package sendspin

import "encoding/json"

// The control messages, both directions.
//
// Field names are the WIRE names and are matched by string on the far side,
// so a rename during a refactor is a silent protocol break. They are read off
// `aiosendspin` — the reference implementation Music Assistant actually runs —
// rather than from prose, because prose does not say which fields are
// optional and a field sent as `null` where the server expects it absent is a
// different message.
//
// ⚠ TWO SHAPES HERE ARE NOT FROM THE REFERENCE: ClientInit and ServerInit.
// Both belong to the pre-encryption exchange, whose payloads are described in
// the specification but not spelled out, and neither has been confirmed
// against a live server. Everything else in this file has. Treat the
// handshake as unverified until it has completed against Music Assistant
// once; the rest of the protocol is not the risk.

// ── Message type names ────────────────────────────────────────────────────

const (
	MsgClientInit     = "client/init"
	MsgServerInit     = "server/init"
	MsgNoiseHandshake = "noise/handshake"
	MsgServerHello    = "server/hello"
	MsgClientHello    = "client/hello"
	MsgServerActivate = "server/activate"
	MsgClientTime     = "client/time"
	MsgServerTime     = "server/time"
	MsgClientState    = "client/state"
	MsgClientCommand  = "client/command"
	MsgServerState    = "server/state"
	MsgServerCommand  = "server/command"
	MsgStreamStart    = "stream/start"
	MsgStreamClear    = "stream/clear"
	MsgStreamEnd      = "stream/end"
	MsgGroupUpdate    = "group/update"
	MsgServerUnpair   = "server/unpair"
	MsgClientGoodbye  = "client/goodbye"
)

// ── Enumerations ──────────────────────────────────────────────────────────

// TrustLevel: what the user has said about this client. NONE until somebody
// pairs it.
const (
	TrustNone = "none"
	TrustUser = "user"
)

// GoodbyeReason values. This is the whole set the reference defines, and it
// is closed: a reason outside it is not a richer message, it is one the
// server will not understand.
const (
	GoodbyeAnotherServer     = "another_server"
	GoodbyeShutdown          = "shutdown"
	GoodbyeRestart           = "restart"
	GoodbyeUserRequest       = "user_request"
	GoodbyeUnauthorized      = "unauthorized"
	GoodbyePairingRequired   = "pairing_required"
	GoodbyeConcurrentAttempt = "concurrent_attempt"
	GoodbyeUnpaired          = "unpaired"
)

// Activity: what a connection is for. A client holds ONE admitted connection
// and ranks competing servers by activity, playback highest.
const (
	ActivityPlayback   = "playback"
	ActivityPairing    = "pairing"
	ActivityManagement = "management"
)

// PlaybackState, as reported by group/update.
const (
	PlaybackPlaying = "playing"
	PlaybackPaused  = "paused"
	PlaybackStopped = "stopped"
)

// Noise cipher suites. ChaCha because the A53 in this device has no AES
// instructions — AESGCM would be a software AES on the audio path.
const (
	CipherChaChaPoly = "25519_ChaChaPoly_SHA256"
	CipherAESGCM     = "25519_AESGCM_SHA256"
)

// ── Handshake ─────────────────────────────────────────────────────────────

// DeviceInfo is what the server shows a user about this client. Every field
// is optional; they are filled in because a speaker that appears in Music
// Assistant as a bare name and nothing else is one nobody can identify in a
// house with three of them.
type DeviceInfo struct {
	ProductName     string `json:"product_name,omitempty"`
	Manufacturer    string `json:"manufacturer,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
	MACAddress      string `json:"mac_address,omitempty"`
}

// UnpairedAccess declares whether this client will accept a Sentinel-PSK
// connection — one where the "shared secret" is a published constant, so the
// encryption is real and the authentication is not.
//
// Enabled, because the alternative is a device that cannot be set up without
// a pairing flow this firmware does not have yet, and the server operator
// still has to approve the client explicitly. Revisit when pairing lands: an
// unpaired-but-approved client is a weaker statement than a paired one, and
// the server's UI is what distinguishes them.
type UnpairedAccess struct {
	Enabled bool `json:"enabled"`
}

// ClientInit opens the connection, in CLEARTEXT, before any encryption
// exists. It announces which cipher suites this client can respond with.
//
// ⚠ Shape not confirmed against the reference implementation — see the file
// comment.
type ClientInit struct {
	CipherSuites []string `json:"cipher_suites"`
}

// ServerInit answers it and names the PSK the server intends to use, so the
// client can look it up before the Noise exchange begins.
//
// ⚠ Shape not confirmed against the reference implementation.
type ServerInit struct {
	PSKID       string `json:"psk_id"`
	PSKCategory string `json:"psk_category"`
	CipherSuite string `json:"cipher_suite,omitempty"`
}

// NoiseHandshake carries one Noise message, base64. The SERVER is the
// initiator and this client the responder, which is backwards from the usual
// reading of "the client connects" and is the detail most likely to be
// implemented the wrong way round.
type NoiseHandshake struct {
	Message string `json:"message"`
}

// ServerHello introduces the server once the transport is encrypted. The name
// here is authoritative and beats the mDNS TXT record.
type ServerHello struct {
	Name string `json:"name"`
}

// ClientHello is this device's introduction, and the ONE-SHOT that carries
// the format list. It cannot be revised without reconnecting, and a format
// absent from it can never be requested — the server falls back silently and
// the client is told nothing. See SupportedFormats.
type ClientHello struct {
	Name string `json:"name"`
	// SupportedRoles in priority order. This device takes the player role
	// and nothing else: it has no display for artwork or metadata, and the
	// source role would put its microphone on somebody else's speakers,
	// which is a decision rather than a feature.
	SupportedRoles []string       `json:"supported_roles"`
	TrustLevel     string         `json:"trust_level,omitempty"`
	DeviceInfo     *DeviceInfo    `json:"device_info,omitempty"`
	ClientID       string         `json:"client_id,omitempty"`
	Version        *int           `json:"version,omitempty"`
	PlayerSupport  *PlayerSupport `json:"player@v1_support,omitempty"`
	UnpairedAccess UnpairedAccess `json:"unpaired_access"`
}

// ServerActivate ends the handshake by saying what this connection is for and
// which roles the server actually took. A role we offered and it did not
// activate is one to stop reporting state for.
type ServerActivate struct {
	Activities  []string `json:"activities"`
	ActiveRoles []string `json:"active_roles,omitempty"`
}

// ClientGoodbye is the clean-leave path, and the reason is the whole point of
// the message: a server told why a client left keeps its group view correct,
// where one that merely stopped hearing from it does not.
type ClientGoodbye struct {
	Reason string `json:"reason"`
}

// ── Time ──────────────────────────────────────────────────────────────────

// ClientTime opens a time exchange. Only t1 goes out; the other three come
// back with the reply.
type ClientTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
}

// ServerTime answers it. Together with the local arrival time these are the
// four timestamps the filter needs.
type ServerTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
	ServerReceived    int64 `json:"server_received"`
	ServerTransmitted int64 `json:"server_transmitted"`
}

// ── State ─────────────────────────────────────────────────────────────────

// ClientState reports what this client can do and is doing.
//
// Available is a POINTER because absent and false are different messages, and
// because the spec forbids reporting available:true before the clock is
// synced — so there is a real state where the answer is "not yet" rather than
// "no".
type ClientState struct {
	Available *bool        `json:"available,omitempty"`
	Player    *PlayerState `json:"player,omitempty"`
}

// ── Streams ───────────────────────────────────────────────────────────────

// StreamStartMsg opens a stream. ServerTransmitted stamps when the server
// sent it, in server time, so a client can tell a fresh instruction from one
// that queued behind a stall.
type StreamStartMsg struct {
	ServerTransmitted int64        `json:"server_transmitted"`
	Player            *StreamStart `json:"player,omitempty"`
}

// StreamClear discards what is buffered — a seek or a skip. It is not a stop:
// the stream continues, and treating it as an end leaves the device silent
// for the rest of the track.
type StreamClear struct {
	ServerTransmitted int64    `json:"server_transmitted"`
	Roles             []string `json:"roles,omitempty"`
}

// StreamEnd closes it.
type StreamEnd struct {
	ServerTransmitted int64    `json:"server_transmitted"`
	Roles             []string `json:"roles,omitempty"`
}

// GroupUpdate carries the group's playback state and identity.
type GroupUpdate struct {
	PlaybackState string `json:"playback_state,omitempty"`
	GroupID       string `json:"group_id,omitempty"`
	GroupName     string `json:"group_name,omitempty"`
}

// ServerCommand asks the client to change something about itself — volume and
// mute, for the player role.
type ServerCommand struct {
	Player *PlayerCommand `json:"player,omitempty"`
}

// PlayerCommand is the player half of a server/command. Both fields are
// pointers: a command setting volume alone must not read as "and unmute", and
// zero is a legitimate volume.
type PlayerCommand struct {
	Volume        *int  `json:"volume,omitempty"`
	Muted         *bool `json:"muted,omitempty"`
	OutputDelayMs *int  `json:"output_delay_ms,omitempty"`
}

// AppliesToPlayer reports whether a roles list concerns the player role.
//
// An ABSENT roles list means every active role, which is the case that would
// otherwise be read as "no roles, ignore this" — and ignoring a stream/clear
// leaves the device playing audio the server has already moved past.
func AppliesToPlayer(roles []string) bool {
	if len(roles) == 0 {
		return true
	}
	for _, r := range roles {
		if r == PlayerRole || r == "player" {
			return true
		}
	}
	return false
}

// decodePayload is the one place a payload is unmarshalled, so a message with
// no payload at all reads as the zero value rather than an error. Several
// messages legitimately carry nothing.
func decodePayload(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, into)
}
