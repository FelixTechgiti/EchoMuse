package spotify

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// A REJECTED CREDENTIAL MUST BE DELETED, NOT RETRIED — otherwise the endpoint
// is dead for ever and the log says why once a minute to nobody.
//
// librespot keeps the credential blob from the last Spotify login in its cache
// directory (`--cache`, and the audio half of that cache is disabled — see
// `args`). On start it logs in with that blob rather than waiting to be picked
// in the app, which is exactly what a speaker should do: it stays authorised
// across reboots.
//
// When the blob stops being accepted — the account's password changed, the
// authorisation was revoked, Spotify expired it — librespot's spirc
// initialisation fails and the process EXITS. Our supervisor restarts it, it
// reads the same dead blob, and it exits again. Measured on hardware
// 2026-09-12: `could not initialize spirc: Invalid state { Login request was
// denied: INVALID_CREDENTIALS }` every 15 to 30 seconds, indefinitely.
//
// **The loop does more than waste restarts: it breaks the discovery login that
// would have repaired it.** Zeroconf sign-in is a two-step exchange — the app
// reads the device's public key from `getInfo`, encrypts its blob against it,
// and POSTs that to `addUser`. The key pair is generated per PROCESS, so a
// restart between those two requests decrypts the blob with a key it was not
// encrypted for, and the device answers `MAC mismatch`. That line was in the
// same log, ten seconds before an exit, and it is the repair path failing
// rather than a second fault.
//
// So the blob is removed after it has been refused, and librespot comes up in
// the state a speaker nobody has used yet is in: advertised, waiting to be
// picked. The cost is that the Echo has to be tapped once in the app again.
// That is the correct trade — the alternative is an endpoint that can never
// recover without somebody opening a root shell on their own hardware.

// credentialsFile is what librespot names the blob inside its cache directory.
const credentialsFile = "credentials.json"

// credentialRejection reports whether a librespot log line says OUR STORED
// credential was refused.
//
// **`MAC mismatch` is deliberately not in this list**, and that is the whole
// subtlety. It comes from `librespot_discovery::server` and means a client's
// blob failed to decrypt — somebody else's credential, arriving over the
// network, at a moment when our key pair had just changed. Treating it as a
// rejection of the stored one would delete a perfectly good credential
// whenever a phone's sign-in raced a restart.
func credentialRejection(line string) bool {
	l := strings.ToLower(line)
	if !strings.Contains(l, "librespot") && !strings.Contains(l, "error") {
		// Cheap guard so an ordinary line mentioning a password cannot
		// trigger this; librespot prefixes its own errors.
		return false
	}
	return strings.Contains(l, "invalid_credentials") ||
		strings.Contains(l, "login request was denied") ||
		strings.Contains(l, "badcredentials")
}

// clearCredentials removes the stored blob and reports whether there was one.
//
// A missing file is the ordinary answer the SECOND time round and is silent:
// the rejection repeats for as long as it takes the next login to arrive, and
// a warning per restart about a file that is already gone would be the same
// noise this exists to end.
func clearCredentials(cacheDir string) bool {
	path := filepath.Join(cacheDir, credentialsFile)
	if err := os.Remove(path); err != nil {
		return false
	}
	log.Printf("[spotify] Spotify refused the stored credential, so it has "+
		"been deleted (%s). librespot will come back up waiting to be picked "+
		"in the app — open Spotify, tap this speaker once, and it stays "+
		"authorised again.", path)
	return true
}
