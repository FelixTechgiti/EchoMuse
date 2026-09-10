# Die Schnittstelle zwischen Gerät und Controller

Das ist der Vertrag, den ein Geräte-Binary umsetzt, um vom
Revoice-Controller angesteuert zu werden. Er existiert, damit ein **neues
Board** — der Echo Show 8 (`crown`) ist das erste nach dem Echo Dot
(`biscuit`) — gegen eine geschriebene Spezifikation gebaut werden kann statt
durch Lesen des Dot-Quelltexts. Der Controller ist bereits boardunabhängig;
ein neues Board ist fast vollständig ein frischer Satz
Hardware-**Bindings** hinter demselben Wire-Protokoll.

Begriffe in **fett** sind in [CONTEXT.md](../CONTEXT.md) definiert. Maßgeblich
für jede Nachricht und jedes Feld ist der inline zitierte Code; wo dieses
Dokument und der Code sich widersprechen, gewinnt der Code, und dieses
Dokument ist der Fehler.

## Die zwei Regeln, denen alles andere dient

Beide werden von `controller/tests/test_capabilities.py` bewacht. Ein Board,
das sie einhält, kann sich mit jeder Controller-Version paaren und umgekehrt.

- **Nach Fähigkeit aushandeln, nicht nach Version.** Das Gerät kündigt in
  seiner `register`-Nachricht an, was es umsetzt; der Controller liest
  `Device.capabilities` und macht jede Funktion von der passenden Zeichenkette
  abhängig. Niemals Versionszeichenketten vergleichen — das packt
  Release-Geschichte in den Controller und schätzt Dev-Builds falsch ein. Ein
  Bedienelement, dessen Fähigkeit dem Gerät fehlt, wird **deaktiviert mit
  Begründung** angezeigt, nie als Element, das stillschweigend nichts tut.
- **Auf altes Verhalten zurückfallen, nie auf eine falsche Antwort.**
  Unbekannte JSON-Felder und Nachrichtentypen werden in beide Richtungen
  ignoriert. Wo ein neues Feld eine Messung festhält, wird Abwesenheit als
  **NULL gespeichert, nicht als 0** — ein Gerät, das etwas nicht melden kann,
  darf nicht so gelesen werden, als hätte es null gemeldet.

## Die drei Ebenen

Jedes Gerät öffnet **drei** WebSocket-Verbindungen zum Controller. Der
Controller wird per mDNS gefunden (`_emcontroller._tcp.local`); das Gerät
wählt alle drei nach außen.

| Pfad | Nutzlast | Richtung | Zweck |
|------|---------|-----------|---------|
| `/control` | JSON-Text | bidirektional | Registrierung, LEDs, Mikrofon an/aus, Tasten- und Zustandsereignisse, Config-Push, WLAN, Shell-Steuerung |
| `/data` | binär | bidirektional | Mikrofon-PCM hinein; Lautsprecher- und Musik-PCM hinaus |
| `/shell/{device_id}` | roh binär | vom Gerät bei Bedarf gewählt | Root-Shell-Proxy, erst nach einem `shell_open`-Befehl geöffnet |

Alle drei gibt es in einfacher (`ws://`) und TLS-Form (`wss://`); siehe
[Link-Authentifizierung und TLS](#link-authentifizierung-und-tls). Die
`/shell`-Ebene wird nicht gewählt, bevor der Controller darum bittet.

## Registrierung und Fähigkeiten

Direkt nach dem Öffnen des `/control`-Sockets sendet das Gerät eine einzelne
`register`-Nachricht (`device/internal/client/control.go`):

```json
{
  "type": "register",
  "device_id": "<stabile ID>",
  "version": "<Firmware-Version, aus den Build-ldflags>",
  "capabilities": ["mic", "speaker", ...],
  "ip": "<lokale IP, weggelassen bei 127.0.0.1 oder unauflösbar>",
  "ambient_light_status": { "...": "..." }
}
```

`capabilities` ist das Aushandlungssignal. Der Dot kündigt sechzehn
bedingungslos an, dazu eine bedingte (`capabilities()` in `control.go`):

| Fähigkeit | Bedingung | Bedeutung |
|------------|-----------|---------|
| `mic` | immer | Streamt Mikrofon-PCM auf `/data` |
| `speaker` | immer | Spielt Lautsprecher-PCM von `/data` |
| `leds` | immer | Nimmt `leds`-Frames an |
| `led_anim` | immer | Zeichnet Animationen lokal aus einer `led_anim`-Vorgabe je Zustandswechsel (statt vom Controller gestreamter Einzelbilder) |
| `buttons` | immer | Gibt `button`-Ereignisse aus |
| `oww_shadow` | immer | Kann das Wakeword lokal bewerten und **melden** (nie handeln) |
| `oww_trigger` | immer | Kann auf eigene Wake-Erkennung **handeln** — bewusst von `oww_shadow` getrennt (siehe unten) |
| `button_hold` | immer | Gibt langes Drücken aus (`heldMs`) |
| `audio_mix` | immer | Hält Musik auf eigenen Frame-Typen und mischt sie unter Sprache, statt zu pausieren |
| `aec_hw_ref` | immer | Kann die AEC-Fernreferenz aus einem Wiedergabe-Loopback in der Mikrofonaufnahme selbst nehmen und fällt auf den Software-Abgriff am ALSA-Schreibvorgang zurück, wenn das Board keinen hat |
| `mute_set` | immer | Nimmt einen `mute_set`-Befehl an. Sagt nichts über das Aufheben — keine Firmware wird das je tun, und die Nachricht trägt dafür keinen Wahrheitswert |
| `output_chain` | immer | Wendet EQ, Bass-Schutz und Limiter selbst an, nach dem Mischen. Der Controller muss dann unbearbeitetes Audio senden: an beiden Enden zu bearbeiten sind zwei Limiter hintereinander |
| `sendspin` | immer | Kann einer Music-Assistant-Gruppe direkt beitreten, ohne Umweg über den Controller |
| `spotify` | immer | Kann einen Spotify-Connect-Endpunkt betreiben. Ob das librespot-Binary installiert ist, sagt `spotify_status`, siehe unten |
| `airplay` | immer | Kann einen AirPlay-Empfänger betreiben. Ob shairport-sync installiert ist, sagt `airplay_status`, siehe unten |
| `audio_state` | immer | Meldet, welche Quelle seine Musikebene besitzt (`audio_source`, siehe unten) |
| `ambient_light` | nur wenn der Sensor tatsächlich lesbar ist (`als.Present()`) | Meldet Lichtwerte |

**`aec_hw_ref` ist eine Fähigkeit mit einem Laufzeitbegleiter, und beide
werden gebraucht.** Die Fähigkeit sagt, dass die Firmware *weiß, wie* man eine
Hardware-Echoreferenz benutzt. Ob das Board tatsächlich eine hat, beantwortet
`aecRef` in der periodischen `stats`-Nachricht — `"hw"`, `"sw"` oder `"off"`,
und **abwesend** bei Firmware, die zu alt ist, um es zu sagen, was nicht als
`"sw"` gelesen werden darf.

Die Trennung gibt es, weil der Nachweis erst zur Laufzeit verfügbar ist: Ein
Kanal ist als Wiedergabe-Loopback bestätigt, wenn er im Leerlauf bitgenau
still ist *und* Ton trägt, während der Lautsprecher spielt — und zur
Registrierungszeit hat nichts gespielt. Wichtig ist das für die
AEC-Verzögerungseinstellung, die beim Software-Abgriff die Latenz vom
Schreiben bis zum Ohr ausgleicht und bei einer bildgenauen Hardwarereferenz
nichts bedeutet. Dieses Bedienelement an die Fähigkeit zu koppeln würde es auf
jedem heutigen Gerät ausgrauen, auch auf denen, die auf den Abgriff
zurückfallen und es brauchen; es an `aecRef == "hw"` zu koppeln deaktiviert es
genau dort, wo es nichts tut.

Ein Board ohne Loopback kündigt `aec_hw_ref` an und meldet dauerhaft
`aecRef: "sw"` — das korrekte abgestufte Verhalten, das keine Änderung am
Controller braucht.

**`oww_shadow` und `oww_trigger` sind zwei Fähigkeiten, nicht eine, und diese
Trennung ist tragend.** Shadow kam zuerst, es gibt also Firmware im Feld, die
bewertet und meldet, aber keinen Code hat, um auf eine Erkennung zu handeln.
Ein Controller, der „kann bewerten" als „kann auslösen" läse, würde seine
eigene Wake-Erkennung zurückstellen und auf einen Auslöser warten, den das
Gerät nie sendet — ein Gerät, das perfekt bewertet und nie antwortet. Ein
Board, das lokal bewertet, SOLLTE beide ankündigen; ein Board, das nur
Bewertungen weiterreicht, kündigt allein `oww_shadow` an.

Der Controller liest Fähigkeiten über `@property`-Zugänge an der Klasse
`Device` (`controller/em_controller.py`): `led_anim_capable`,
`audio_mix_capable`, `button_hold_capable`, `oww_shadow_capable`,
`oww_trigger_capable`, `aec_hw_ref_capable`, dazu direkte
Mitgliedschaftsprüfungen für die vier Grundfähigkeiten
(`mic`/`speaker`/`leds`/`buttons`). Jede Zeichenkette, die der Controller
prüft, muss eine sein, die das Gerät senden kann, und jede Zeichenkette, die
das Gerät sendet, muss eine sein, die der Controller versteht — beide
Richtungen sind von `test_capabilities.py` festgenagelt, denn ein Tippfehler
auf einer der beiden Seiten scheitert stillschweigend.

## `/control` — JSON-Nachrichten

Auf dem Gerät vom Typ-Switch in `control.go` verteilt. Unbekannte Typen werden
ignoriert (Vorwärtskompatibilität). Die unten genannten Nutzlastfelder sind
die, auf die reagiert wird; abwesende optionale Felder behalten das vorherige
bzw. voreingestellte Verhalten.

**Gerät → Controller**

| `type` | Nutzlast | Bedeutung |
|--------|---------|---------|
| `register` | siehe oben | Handschlag, einmal beim Verbinden |
| `button` | `clickType`, `down`, `heldMs`, `muted`, `button.type` | Drücken/Loslassen; `heldMs` nur bei `button_hold` |
| `mute_state` | `muted` | Mute umgeschaltet (Mute ist gerätehoheitlich — siehe `device/CLAUDE.md`) |
| `volume_state` | `level` | Lautstärke geändert; der Controller speichert sie als `startupVolume` |
| `oww_shadow_cross` | Felder zu Wert, Schwelle, Alter | Wake-Überschreitung im Schattenmodus (nur Meldung) |
| `oww_wake` | Wert, wirksame Schwelle, Alter | Auslöser auf dem Gerät gefeuert (`owwOnDevice=on`); landet in `Device.pending_wake` |
| `ambient_light` | `value` | Lichtwert (nur bei `ambient_light`) |
| `audio_source` | `source` | Die Musikebene hat den Besitzer gewechselt: `"none"`, `"controller"`, `"sendspin"`, `"spotify"` oder `"airplay"` (nur bei `audio_state`). Der aktuelle Wert reitet auch auf `register` mit, damit ein Wiederverbinden mitten im Stück nicht als Stille gelesen wird. Es ist die EINZIGE Möglichkeit für den Controller zu erfahren, dass ein lokaler Endpunkt spielt — kein Frame dieses Tons kommt durch ihn hindurch |
| `ble_adverts` | `adverts[]` | Stapel vom passiven BLE-Scanner. **Alter Pfad** — sende diese auf `/data` als `0x06`, wann immer der Controller `ble_adverts_data` angekündigt hat, und nimm diese Nachricht nur, wenn er es nicht tat (#404) |
| `pong` | — | Keepalive-Antwort |

**Controller → Gerät**

| `type` | Nutzlast | Bedeutung |
|--------|---------|---------|
| `ack` | `device_id`, `features[]` | Registrierung angenommen. `features` ist die Fähigkeitsliste des CONTROLLERS — das Spiegelbild der Geräteliste, und genauso zu lesen: Eine fehlende Funktion ist eine, die der Controller nicht kann. Auf Controllern vor 2.23.0 ganz abwesend |
| `leds` | `leds[]`, `listening?` | Ein LED-Bild; `listening:true` markiert den Zuhör-Ring, damit die Richtungsüberlagerung daran anknüpft |
| `led_anim` | `{pattern, colors, periodMs, ttlSec}` | Vorgabe für eine lokale Animation; nur bei `led_anim` gesendet |
| `mic_start` | `lock_mic?` | Mikrofonstrom starten. `lock_mic:false`/abwesend = immer laufender, ungegatterter Wake-Strom; `true` = begrenztes, VAD-gegattertes Gespräch |
| `mic_stop` | — | Mikrofonstrom stoppen |
| `beam_lock` / `beam_unlock` | — | Beamformer für ein Gespräch auf das gewählte Randmikrofon sperren / zurück auf Rundum |
| `volume_set` | `level` | Absolute Lautstärke setzen |
| `duck` | `on` | Musik unter einem Sprachgespräch absenken (Gesprächsbeginn/-ende) |
| `config` | `ConfigMessage`-Felder | Konfiguration schieben (siehe unten) |
| `wifi_change` / `wifi_commit` | `ssid`,`psk` / — | WLAN mit automatischem Rückfall wechseln; Commit macht es endgültig |
| `shell_open` / `shell_close` | `pty?` | Das Gerät bitten, `/shell` zu wählen (`pty:true` = interaktiv) / zu schließen |
| `music_flush` / `speaker_flush` | — | Musik- bzw. Sprachpuffer leeren (Barge-in nutzt `speaker_flush`) |

## `/data` — binäre Frames

Das erste Byte ist der Frame-Typ, der Rest die Nutzlast. **Die Typcodes sind
nach Richtung getrennt** — `0x04`/`0x05` bedeuten Unterschiedliches, je
nachdem, wer sie gesendet hat, und das ist Absicht
(`device/internal/client/data.go:25`). Wer ein Board umsetzt, darf keine
einzelne globale Tabelle lesen.

**Controller → Gerät (Wiedergabe)**

| Code | Name | Bedeutung |
|------|------|---------|
| `0x02` | speaker | PCM-Stück der Sprachantwort; wird sofort gespielt |
| `0x03` | speaker EOS | Ende des Sprachstroms |
| `0x04` | music | Musik-PCM-Stück auf eigener Ebene — **nur bei `audio_mix`** |
| `0x05` | music EOS | Ende des Musikstroms — **nur bei `audio_mix`** |

**Gerät → Controller (Aufnahme)**

| Code | Name | Bedeutung |
|------|------|---------|
| `0x01` | mic | Mikrofon-PCM-Stück (mono `S16_LE`, 16 kHz) |
| `0x04` | VAD-Ende | Begrenztes Gespräch: Sprache wurde erkannt und endete dann |
| `0x05` | Keine-Sprache-Timeout | Begrenztes Gespräch: vor dem Timeout wurde nie Sprache erkannt |
| `0x06` | BLE-Advertisements | Stapel gescannter BLE-Advertisements — **nur wenn der Controller `ble_adverts_data` angekündigt hat** |

`0x04`/`0x05` lassen sich gefahrlos doppelt verwenden, weil Wiedergabe-Frames
nur zum Gerät und Aufnahme-Frames nur von ihm fließen. Die beiden
Aufnahme-Endmarken sind bewusst unterschiedlich: „Wakeword, dann Stille"
(leise aufgeben) muss von „gesprochen, und das Backend hatte nichts zu sagen"
unterscheidbar sein.

Formen des Mikrofonstroms (`device/CLAUDE.md`, Device audio pipeline):

- Der **immer laufende Wake-Strom** (`mic_start` ohne `lock_mic`) ist
  ungegattert und AGC-frei — jede Periode wird durchgehend gesendet, damit die
  Wake-Bewertung einen ununterbrochenen Strom sieht.
- Der **begrenzte Gesprächsstrom** (`lock_mic:true`) ist VAD-gegattert mit
  einem Vorlaufring, endet mit einer `0x04`-Endmarke, wenn das Gatter nach
  Sprache schließt, und mit `0x05`, wenn innerhalb des Timeouts keine Sprache
  kam.

### `0x06` — BLE-Advertisements

Die Nutzlast ist UTF-8-JSON, `{"adverts": [ ... ]}`, jeder Eintrag wie oben
unter `ble_adverts` beschrieben. Sie trägt keinen Sequenzkopf: Ein Stapel ist
in sich abgeschlossen, und ein verlorener ist keine erneute Übertragung wert.

**Sende diese nur, wenn der Controller in seinem `ack` `ble_adverts_data`
angekündigt hat.** Ein Controller, der das nicht tat, ignoriert das Frame —
unbekannte Frame-Typen werden in beide Richtungen ignoriert — und jedes
Advertisement verschwindet, ohne dass irgendwo ein Fehler auftaucht. Weich
stattdessen auf die Kontrollnachricht `ble_adverts` aus.

Zwei Regeln für die Senderseite, von denen der Controller keine für dich
erzwingen kann:

- **Sende nicht, während ein BEGRENZTES GESPRÄCH streamt** (`mic_start` mit
  `lock_mic:true`). Die Datenebene ist ein einziger TCP-Strom, ein bereits
  geschriebenes Frame lässt sich also nicht vordrängeln; der einzige Weg,
  dass der Ton eines Gesprächs nicht hinter Telemetrie ansteht, ist, die
  Telemetrie nicht zu schreiben. Ein Gespräch dauert Sekunden, und Home
  Assistant duldet 195 s Veralterung pro Gerät — nachgelagert merkt das also
  nichts.

  **Dehne das nicht auf den immer laufenden Wake-Strom aus.** Der läuft auf
  jedem Gerät immer, dessen Wakeword controllerseitig bewertet wird;
  Advertisements zurückzuhalten, sobald das Mikrofon streamt, verwirft also
  für immer jeden Stapel, und der Proxy stirbt ohne Fehler an beiden Enden.
- **Verwirf einen Stapel, den du nicht senden kannst, statt auf die
  Kontrollebene auszuweichen.** Auszuweichen legt Massentelemetrie genau dann
  auf den Lebendigkeitskanal, wenn die Verbindung ohnehin in Schwierigkeiten
  ist — und genau dafür existiert dieses Frame.

## Config-Push — `ConfigMessage`

Der Controller sendet `config` beim Verbinden und bei jeder Änderung der
Gerätekonfiguration. Felder sind camelCase
(`device/internal/config/config.go`). **Teilaktualisierung: Felder ungleich
null werden angewandt, Felder gleich null bzw. nil werden ignoriert** — daher
gibt es Zeigertypen (`*bool`, `*int`), wo `false`/`0` von „nicht gesetzt"
unterscheidbar sein muss. Änderungen wirken sofort, ohne Neustart.

Der kanonische Feldsatz (`CLAUDE.md` im Repository-Wurzelverzeichnis, „Device
config push"):

```
vadThreshold, vadSpeechMs, vadSilenceMs,
owwThreshold, owwModel, owwSpeexNs, owwOnDevice,
adcDigitalGain, adcMicpga, micGainDb,
startupVolume,
beamAngle, beamformingEnabled,
aecEnabled, aecDelayMs, aecTailMs, agcEnabled, nsAsr,
bargeInEnabled, bargeInThreshold,
bleProxyEnabled,
eqBands, eqLoudness, limiterEnabled, limiterThreshold, limiterRelease,
bassGuardEnabled, bassGuardDb,
ledScene, ledListenColor, ledThinkColor,
meterAttack, meterDecay, meterFloor, meterGamma, meterRef, meterCurve,
wakeArbitrationMs, duckDb,
buttonSingleTapEvent, buttonMultiTapMs,
owwOnDevice, saveUtterances
```

Nicht auf jedes Feld reagiert das Gerät. Die Schlüssel der Klangkette
(`limiter*`, `bassGuard*`), `eq*`, `saveUtterances`, `wakeArbitrationMs` und
die `button*`-Zeitschlüssel sind **controllerseitig** — diese Verarbeitung
passiert, bevor der Ton auf die Leitung geht, oder dient nur der
Konfigurationszuordnung. `owwOnDevice` wird sowohl vom Controller verarbeitet
(Zuordnung) als auch vom Gerät umgesetzt. Ein neues Board muss nur die
Schlüssel umsetzen, die zu tatsächlich vorhandener Hardware gehören;
unbekannte Schlüssel werden ignoriert, was das korrekte Zurückfallen ist.

## Link-Authentifizierung und TLS

- Alle drei Ebenen tragen einen `X-EM-Token`-Header, bei **jedem Wählvorgang**
  aus der Zugangsdatei des Geräts gelesen
  (`device/internal/client/tlscreds.go`) — eine aufgespielte Zugangsberechtigung
  wirkt also beim nächsten Verbinden, ohne Neustart.
- TLS wird gewählt, wenn das Gerät eine Zertifizierungsstelle auf der Platte
  hat **und** der Controller einen `tls_port`-mDNS-TXT-Eintrag ankündigt →
  `wss://` wählen. Zertifizierungsstelle vorhanden, aber kein TXT-Eintrag →
  unverschlüsselt mit Warnung (bewusster Rückfall für die Einführung). Die
  Serveridentität ist der feste DNS-SAN `revoice-controller`, nie eine IP.
- Zertifikate sind rückdatiert und langlebig, **und** das Gerät klemmt seine
  Prüfuhr auf die Bauzeit der Firmware, weil ein Echo vor NTP mit falscher Uhr
  startet und ein Gerät, das sich nicht verbinden kann, seine Uhr nicht
  richten kann. Ein neues Board erbt das — „normalisiere" keine der beiden
  Hälften.
- Durchgesetzt wird das von `em_linkauth.decide`
  (`controller/em_linkauth.py`): Ein falscher Token wird immer abgewiesen; ein
  gespeicherter Token ohne vorgelegten wird zugelassen (der Zugangs-Push
  selbst reitet auf der unverschlüsselten Ebene); ein Token für ein Gerät, zu
  dem nichts gespeichert ist, wird ignoriert, nicht abgewiesen.
  `REQUIRE_DEVICE_TLS=1` macht TLS und Token verpflichtend.

## Was dem Geräte-Binary gehört

Für jedes Board ist das Geräte-Binary der lokale Hardware-Agent eines
Sprachsatelliten. Seine Aufgaben:

- **Aufnahme für den Sprachassistenten** — Mikrofon-Streaming (Wake-Strom und
  begrenzte Gespräche), die Aufnahmepipeline auf dem Gerät
  (Beamforming/Verstärkung/AEC/AGC/VAD, soweit die Hardware es hergibt) und
  optional Wake-Bewertung und -Auslösung auf dem Gerät.
- **Wiedergabe** — Lautsprecherausgabe, und Musik auf eigener Ebene, unter
  Sprache gemischt (Ducking), wenn `audio_mix` angekündigt wird.
- **Tasten** — Ereignisse für Drücken, Halten und Mute, aufgelöst über den
  **Namen** des Geräts, nicht über den Index (`event2` ist auf einem Board
  eine Taste und auf einem anderen ein Touchscreen).
- **Durchsagen** — vom Controller geschobenen Ton außerhalb eines Gesprächs
  abspielen.
- **Statusausgabe** — LEDs beim Dot; auf einem Board ohne eigenen oder mit
  geliehenem Bildschirm die minimale Statusfläche, die dieses Board festlegt
  (oder keine).

Alles, was ein Backend vernünftigerweise erledigen kann — Absichten, TTS,
HA-Entitäten —, bleibt im Controller bzw. in Home Assistant über die
ESPHome-Voice-Assistant-API. Das Gerät setzt das nicht neu um und hängt für
seine eigene Arbeit nicht von Amazons HALs oder Bibliotheken ab (siehe den
Richtungshinweis in `CLAUDE.md` im Wurzelverzeichnis).

## Board-Profil — `crown` (Echo Show 8, 1. Generation)

Die `crown`-Bindings werden hinter einem Go-Build-Tag gebaut, das `server`
spiegelt (ADR-0001). Das Hardware-Inventar — ALSA-Karten und -Geräte,
Formate, Pfade der Eingabegeräte, Autostart — steht in
[echo-show-8-hardware-map.md](echo-show-8-hardware-map.md); dieser Abschnitt
hält nur fest, worauf sich die *Schnittstelle* für das MVP festlegt.

**Fähigkeiten fürs MVP** (Teilmenge der des Dots):

| Fähigkeit | crown-MVP | Anmerkung |
|------------|-----------|------|
| `speaker` | ja | `card0,device0` → RT5616 (Issue #5). Streamt sauber, aber `Ext_Speaker_Amp_Switch` ist invertiert (`On` = still, `Off` = hörbar) — dieselbe Falle wie bei checkers, am 2026-08-26 nach Gehör bestätigt. Muss in der Init des Bindings auf `Off` gefahren werden; die Voreinstellung beim Start ist `On` |
| `mic` | **nachgewiesen** | `card0,device22`, 6 Kanäle / 16 kHz / `S24_3LE`, am 2026-08-26 durch Aufnahme auf echter Hardware bestätigt — echtes Signal, keine digitalen Nullen. HW_REFINE deckt sich exakt mit den Treiberkonstanten von checkers. Offen: Störabstand im ruhigen Raum und über Distanz, nicht die Rohaufnahme — siehe unten |
| `buttons` | ja | Über den Namen aufgelöst (`gpio-keys` Lautstärke, Aktionstaste, Kameraverschluss) |
| `leds` / `led_anim` | **nein** | Kein LED-Ring; eine Statusüberlagerung für „Sprachgespräch" auf dem Display ist auf nach dem MVP verschoben und bleibt auch dann dem Nutzer aus dem Weg |
| `audio_mix` | später | Für die MVP-Sprachschleife nicht nötig |
| `ambient_light` | offen | Nur wenn ein lesbarer Sensor gefunden wird |
| `oww_shadow` / `oww_trigger` | nein (MVP) | Das MVP nutzt das Wakeword **controllerseitig** |

**Audio-Besitz** (ADR-0002): `crown` beansprucht Mikrofon und Lautsprecher
exklusiv, solange Revoice läuft, genau wie der Dot. Das Mikrofon wird
**durchgehend gehalten** — das Wakeword liegt im MVP beim Controller, das Gerät
streamt Mikrofon-PCM also die ganze Zeit, sonst wird es taub für das nächste
Wakeword. Der **Lautsprecher** wird für ein Gespräch geholt (die Antwort und
alle Medien, die das Gespräch verlangte) und danach freigegeben — das
Alexa-Modell. Der Bildschirm gehört durchgehend vollständig der eigenen
Software der Nutzerin.

**Das Mikrofon war der MVP-Blocker; das ist es nicht mehr.** Die Mikrofone
sitzen auf externen TLV320AIC3101-Dies (TDM, `card0,device22`, 6 Kanäle /
16 kHz / `S24_3LE`) — dasselbe Format, das der Dot bereits aufnimmt
(`device/internal/bindings/mic/pcm_microphone.go`, 9 Kanäle / 16000 Hz /
`tinyalsa.PCM_FORMAT_S24_3LE`), nur mit weniger Kanälen, gleicher
`Channels * 3`-Schrittweite, gleichem `DeviceConfig`-gesteuerten Pfad
danach. Am 2026-08-26 auf echter Hardware mit `device/tools/capture_mics`
nachgewiesen: öffnet sauber, echtes Signal auf allen 4 Mikrofonkanälen, keine
digitalen Nullen. `device/tools/hw_refine_probe` bestätigt, dass der Bereich
des Treibers exakt den Konstanten von checkers entspricht. Die vollständige
Geschichte (warum „digitale Nullen" die ursprünglich falsche Lesart war, die
Untersuchung zur Verstärkung, die tatsächlichen Aufnahmedaten) steht in
[echo-show-8-hardware-map.md](echo-show-8-hardware-map.md#on-hardware-capture-2026-08-26--the-gono-go-measurement-done).

**Weiterhin offen: der Störabstand im ruhigen Raum und über Distanz**, nicht
die Rohaufnahme — der Test von heute war eine Plausibilitätsprüfung in einem
lauten Raum mit laufender Musik, keine Messung der Wake-Zuverlässigkeit.
Lautsprecher (#5) und Mikrofon (#6) sind beide nachgewiesen; der Störabstand
ist das verbleibende Ja-oder-Nein für den MVP-Meilenstein (Start → verbinden →
HA → Wakeword → Assist → gesprochene Antwort).

Gilt für jedes crown-Binding, nicht nur fürs Mikrofon: Eingabegeräte und
i2c-Adressen **über den Namen** auflösen, nie über die Nummer — die
`eventN`-Nummerierung ist über Boards hinweg nicht stabil (auf `biscuit` ist
es die Lautstärketaste, auf `checkers` der Touchscreen), und ein Öffnen mit
falscher Nummer scheitert still statt mit einem Fehler. Siehe auch Issue #36
(Echo Show 5 / `checkers`) — dieselbe Form von Inbetriebnahmeproblem, lesenswert,
bevor man crown-spezifische Bindings schreibt.

## Invarianten (was die Tests festnageln)

`controller/tests/test_capabilities.py` ist die Leitplanke dieses Vertrags:

- Das Gerät kündigt die erwarteten Grundfähigkeiten an.
- Jede Fähigkeitszeichenkette, die der Controller prüft, ist eine, die das
  Gerät senden kann, und umgekehrt (beidseitiger Tippfehlerschutz).
- `oww_shadow` und `oww_trigger` bleiben getrennt.
- Fähigkeiten, die gemeldet werden, bevor der ESPHome-Server existiert, gehen
  nicht verloren.
- Eine Änderung der Fähigkeiten stößt die HA-Verbindung an, damit sich die
  Entitätsliste erneuert.
- Ein deaktiviertes Bedienelement schreibt nicht stillschweigend, wenn seine
  Fähigkeit fehlt.

Eine Board- oder Protokolländerung, die eine davon bricht, muss den Test mit
Begründung anpassen, statt ihn zu umgehen.
