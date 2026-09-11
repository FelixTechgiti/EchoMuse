# Zustandsmodell für Audio

Wem der Lautsprecher gehört, was auf der Leitung liegt, und was passiert, wenn
zwei Dinge ihn gleichzeitig wollen.

Das gibt es aus demselben Grund wie `led-ring-states.md`. Vier Dinge können
inzwischen Ton auf ein Gerät bringen — eine Sprachantwort, Musik, eine
HA-Durchsage und (seit #167) ein Timer-Alarm —, und jedes wurde für sich
ergänzt, für sich genommen korrekt. Die Fehler stecken in ihrem
Zusammenspiel.

Zwei davon sind inzwischen behoben, und die Behebung hat beide Male dieselbe
Form: **Besitzer werden GEZÄHLT, nicht markiert.** #261 hob das Absenken
mitten in der Antwort auf, weil `ducked` ein einzelner Wahrheitswert war und
der erste von zwei überlappenden Besitzern es beim Beenden zurücknahm; #314
gab die Lautsprecherhoheit des Gesprächs aus demselben Grund frei.
`duck_depth` und `owner_depth` sind bewusst getrennte Zähler — das Absenken
zählt nur auf dem Mischpfad hoch, ein Gerät, das pausiert statt abzusenken,
hat also überlappende Besitzer ohne Absenktiefe, und die beiden
zusammenzufassen wäre überall richtig außer genau auf diesen Geräten.

Noch offen: #262 (Musik wird bis zum Gesprächsende verschoben), #243 (ob die
Klangkette überhaupt aufs Gerät gehört) und die Zustandskarte, die dem Alarm
als viertem Besitzer noch geschuldet wird.

Die Statusmarken entsprechen dem LED-Dokument: **[heute]** ist ausgeliefertes,
im Code geprüftes Verhalten, **[vorgeschlagen]** ist entworfen oder in Review
und nicht gemergt.

---

## 1. Entwurfsprinzipien

Die stehen fest, alles Weitere folgt daraus.

- **Das Gerät mischt; der Controller entscheidet.** Zwei unabhängige
  PCM-Ströme erreichen das Gerät und werden am ALSA-Schreibvorgang summiert
  (`speaker/pcm_speaker.go:277`). Der Controller mischt nie.
- **Sprache wird nie abgesenkt.** Ducking senkt das Bett darunter, nie die
  Antwort selbst.
- **Ton, der den Controller verlassen hat, lässt sich nicht zurückholen.**
  `LEAD_S` = 4,0 s Musik liegen bereits im Puffer des Geräts, wenn ein
  Wakeword feuert, bei einer Gerätetiefe von `audioChanDepth` = 128 × 42,7 ms
  ≈ **5,46 s** (`pcm_speaker.go:37`). Alles, was der Controller an bereits
  unterwegs befindlichem Ton ändern will, braucht eine Kontrollnachricht und
  nicht eine Änderung dessen, was er sendet.
- **Ein Sprachgespräch senkt Musik ab; es pausiert sie nicht.** Pausieren
  braucht zum Fortsetzen ein Spulen, und ein Flow-Stream von Music Assistant
  lässt sich nicht spulen — ein Gespräch von 28 s kostete also 28 s des
  Liedes.
- **Das Ende des Tons ist, was das Gerät sagt.** Der Abschluss der Wiedergabe
  wartet auf die `playback_stats` des Geräts, nie auf eine Schätzung aus dem
  Socket-Schreibvorgang — der praktisch sofort fertig ist, egal wie langsam
  die Strecke ist (gemessen am 2026-07-24: Der Ring erlosch auf einem Gerät
  6,1 s zu früh, auf einem anderen 3,2 s).

---

## 2. Lautsprecher-Besitzer — die Prioritätsleiter

Höchste zuerst. Nur ein Besitzer treibt zu einer Zeit die **Sprachebene**;
Musik läuft darunter auf einer eigenen Ebene und wird abgesenkt statt
verdrängt.

| # | Besitzer | Ebene | Übernimmt durch | Gibt frei bei |
|---|---|---|---|---|
| 1 | Sprachgespräch (Wakeword oder Taste) | Sprache | `em_player.interrupt()` — **bedingungslos**, auch wenn nichts spielt | `resume_interrupted()` am Gesprächsende |
| 2 | HA-Durchsage | Sprache | derselbe `interrupt()`-Pfad | Ende der Durchsagenwiedergabe |
| 3 | Timer-Alarmklingeln | Sprache | `start_timer_alarm()`, Salven abhängig von `speaker_busy` | Abweisen (Taste / gesprochen / CANCELLED) oder `MAX_RING_S` = 120 s | **[heute]** |
| 4 | Medien / Musik | Musik | `em_player.play()` | `stop()` / `pause()` / Gerät weg |

**Der Besitz wird bedingungslos genommen, und das ist Absicht** — keine
Optimierung zum Entfernen. „Spiel etwas Jazz" führt die Absicht aus, *bevor*
HA die gesprochene Antwort erzeugt, `play_media` kann also eintreffen, während
das TTS noch kommt. Wäre der Besitz davon abhängig, dass schon etwas spielt,
landete die Musik auf derselben Ebene wie die Antwort und redete darüber. Das
ist zugleich die direkte Ursache von #262, und die Behebung dort besteht
darin, Musik auf ihrer eigenen Ebene starten zu lassen, nicht darin, diese
Regel aufzuweichen.

---

## 3. Die zwei Ebenen

| Byte | Richtung | Bedeutung | Konstante |
|---|---|---|---|
| `0x01` | Gerät → Controller | Mikrofon-PCM | — |
| `0x02` | Controller → Gerät | Sprach-PCM (48 kHz mono S16_LE) | `frameTypeSpeaker` |
| `0x03` | Controller → Gerät | Ende des Sprachstroms | `frameTypeEOS` |
| `0x04` | Controller → Gerät | **Musik-PCM** | `frameTypeMusic` |
| `0x05` | Controller → Gerät | **Ende des Musikstroms** | `frameTypeMusicEOS` |
| `0x04` | Gerät → Controller | **VAD-Ende der Sprache** | `frameTypeVADEnd` |
| `0x05` | Gerät → Controller | **Keine-Sprache-Timeout** | `frameTypeNoSpeechTimeout` |

**`0x04` und `0x05` bedeuten in jeder Richtung etwas anderes** und werden
allein dadurch unterschieden, wohin das Frame reist
(`device/internal/client/data.go:27-45`). Erzwungen wird das nur dadurch, dass
der Lesende an einem Ende des Sockets sitzt. Gut zu wissen, bevor man einen
Frame-Typ ergänzt.

Das Gerät hält je Ebene einen `audioStream`, jeweils `audioChanDepth` tief,
und mischt sie beim Schreiben, wobei die aktuelle Absenkverstärkung nur auf
die Musik wirkt (`pcm_speaker.go:123-125`, `:277`). Die Summe **sättigt,
statt überzulaufen** — ein Überlauf macht aus einer lauten Spitze eine mit
voller Amplitude und umgekehrter Polarität, weit schlimmer als Übersteuern.

---

## 4. Abstufung nach Fähigkeit — `audio_mix`

`em_player._frame_types()` wählt die Ebene anhand der angekündigten Fähigkeit
des Geräts (`em_player.py:70`):

| Firmware | Musikebene | Sprachgespräch tut | Folge |
|---|---|---|---|
| kündigt `audio_mix` an | `0x04`/`0x05` | **senkt ab** (`duck on`) | Musik läuft leise unter der Antwort weiter | **[heute]** |
| tut es nicht | `0x02`/`0x03` | **pausiert**, `resume_after` gesetzt | altes Verhalten, zum Fortsetzen ist Spulen nötig | **[heute]** |

Auf den alten Pfad zurückzufallen statt auf eine falsche Antwort ist die Regel
aus `CLAUDE.md`: Ein Gerät, das nicht mischen kann, würde `0x04` überhaupt nie
abspielen, und das ist Stille, kein abgestuftes Verhalten.

---

## 5. Übergänge

### 5.1 Sprachgespräch über Musik

| # | Voraussetzung | Aktion | Musik | Sprache | Status |
|---|---|---|---|---|---|
| V1 | Musik läuft, `audio_mix` | `interrupt()` setzt `ducked`, sendet `duck on`, der Vorlauf sinkt von `LEAD_S` 4,0 s auf `TURN_LEAD_S` 1,0 s, um die gemeinsame Datenebene freizugeben | läuft weiter, um `duckDb` abgesenkt (Standard −18 dB) | Antwort auf `0x02` | [heute] |
| V2 | Musik läuft, kein `audio_mix` | `interrupt()` → `pause()`, `resume_after = True` | hält an, mit Lesezeichen | Antwort auf `0x02` | [heute] |
| V3 | Gespräch endet | `resume_interrupted()` gibt den Besitz frei, `duck off` | zurück auf volle Lautstärke | — | [heute] |
| V4 | Nutzerbefehl während des Gesprächs | als `pending` vermerkt; **überstimmt** unser automatisches Fortsetzen | der letzte Schreibvorgang gewinnt | — | [heute] |
| V5 | Absenkung endet vor der Antwort | — | **hebt sich zu früh, konkurriert mit dem Ausklang** | — | **Fehler, #261** |

**V5 ist #261 und ist ungeklärt.** `em_player` protokolliert nur die
Fehlerpfade (`duck failed` / `unduck failed`), eine Absenkung, die gesendet,
angewandt und dann zu früh gelöst wird, ist im Log also völlig still. Erst die
Logzeile ergänzen, dann Theorien bilden: „Die Absenkung ging nie hinaus" und
„die Absenkung ging hinaus, und etwas hat sie gelöst" verlangen
gegensätzliche Untersuchungen.

### 5.2 Stoppen und Leeren

| # | Situation | Nachricht | Warum | Status |
|---|---|---|---|---|
| F1 | Nutzer stoppt/pausiert Musik, `audio_mix` | `music_flush` | verwirft nur die gepufferte *Musik* | [heute] |
| F2 | Nutzer stoppt/pausiert Musik, kein `audio_mix` | `speaker_flush` | Musik liegt dort auf der Sprachebene | [heute] |
| F3 | Barge-in während einer Antwort | `speaker_flush` | schneidet die gepufferte Antwort; der Rest steckt meist noch im TCP, das Gerät verwirft also, bis es das `0x03` des Stroms sieht | [heute] |
| F4 | Sprachgespräch beginnt über Musik | **keine von beiden** | Leeren würde den gepufferten Ton verwerfen, der das Absenken sofort wirken lässt — und auf einem nicht spulbaren Strom ist er dann endgültig weg | [heute] |
| F5 | Alarm abgewiesen | `speaker_flush` | sonst klingelt es aus ~5,5 s Gerätepuffer aus | **[heute]** |

Das Gatter für F1/F2 ist `em_player.py:481`. **Ein Sprachgespräch darf
niemals `music_flush` senden** — der Handler des Geräts sagt das selbst
(`control.go:520`), und genau dafür existiert die zweite Ebene.

### 5.3 Timer-Alarm **[heute — #167]**

| # | Voraussetzung | Aktion | Status |
|---|---|---|---|
| T1 | HA sendet `TIMER_FINISHED` | Klingeln beginnt: Salven in Schleife plus bernsteinfarbenes LED-Pulsieren, falls `led_anim_capable` | [heute] |
| T2 | Ein Gespräch oder eine Durchsage spielt | Salve wird zurückgehalten, solange `device.speaker_busy` ungleich null ist | [heute] |
| T3 | Wakeword über dem Klingeln gehört | Alarm um `DUCK_DB` abgesenkt für `DUCK_HOLD_S` = 12 s, damit der Befehl bei der Spracherkennung ankommt | [heute] |
| T4 | Abweisen (Taste, Transkript oder `CANCELLED`) | Klingeln hört auf, `speaker_flush` | [heute] |
| T5 | Niemand reagiert | endet bei `MAX_RING_S` = 120 s | [heute] |

`speaker_busy` ist ein Zähler und kein Schalter, weil eine Durchsage die
Wiedergabe eines Gesprächs überlappen kann, und er wird in einem
`try/finally` gehalten, weil ein abgebrochenes Gespräch, das ihn lecken ließe,
für die gesamte Prozesslaufzeit jedes künftige Klingeln blockieren würde.

---

## 6. Sendspin **[in Arbeit — #89]**

**Lies für Details auf der Leitung die Spezifikation, nicht diesen
Abschnitt.** Sie ist unter `github.com/Sendspin/spec` veröffentlicht und hat
mehrere Dinge geklärt, die dieser Entwurf aus zweiter Hand hatte. Die
Korrekturen, weil jede sonst auf die teure Tour gefunden worden wäre:

- **Der Handschlag ist länger als „erst client/hello".** Er lautet
  `client/init` (im Klartext) → `server/init` + Noise-Nachricht 1 →
  Noise-Nachricht 2 → Transportmodus → `server/hello` → `client/hello` →
  `server/activate`. `client/hello` — die Nachricht mit der Formatliste und
  der einzige Schuss, der sich nicht korrigieren lässt — geht also **nach**
  dem Aufbau der Verschlüsselung und nachdem der Server sich vorgestellt hat,
  nicht als Eröffnungszug.
- **Der Server ist der Noise-INITIATOR und der Client der Antwortende**, was
  der üblichen Lesart von „der Client verbindet sich" zuwiderläuft.
- **Ungekoppelter Zugang hat eine veröffentlichte Konstante.** Der
  Sentinel-PSK ist `SHA-256("sendspin-sentinel-psk-v1")`, es existiert also
  ein klartextäquivalenter Pfad *innerhalb* des verschlüsselten Transports und
  nicht daneben. Der Befund vom August, dass Music Assistant gar keine
  Verschlüsselung umgesetzt hat, heißt nicht, dass der Handschlag entfallen
  kann; es heißt, dass der PSK auf diesem Pfad öffentlich ist.
- **Fragmentierung existiert, und Audio darf sie nutzen.** Jede Nachricht über
  65518 Byte wird auf Frames vom Typ 1 aufgeteilt, mit First- und Last-Flag in
  einem Flag-Byte, dessen übrige Bits null sein MÜSSEN. Die Schwelle liegt
  16 Byte unter 64 KiB, weil vor der Verschlüsselung fragmentiert wird und der
  Tag noch hineinpassen muss.
- **Der Kopf eines Audiostücks ist fest und klein**: Typbyte, 64-Bit-Zeitstempel
  in µs (Big Endian), 32-Bit-`send_ahead` (Big Endian). Der Zeitstempel sagt,
  wann das erste Sample DEN LAUTSPRECHER VERLASSEN muss, nicht wann das Stück
  dekodiert werden soll.

**Auf dem Host gebaut und getestet, aber noch nicht gehört:** der Zeitfilter
(`device/internal/sendspin/timefilter.go`), das Framing (`frame.go`), die
Formen der Player-Rolle (`player.go`), die Nachrichtenformen (`messages.go`),
der Verbindungsautomat (`conn.go`), angetrieben von einem gescripteten Server
über einen Transport im Speicher, die Driftkorrekturregel (`sync.go`) und die
Schlichtung der Musikebene, die jedes dieser Protokolle braucht
(`device/internal/musicplane`). **Bewusst nicht gebaut: der Noise-Handschlag.**
Die Schnittstelle `Crypto` ist da, und `Plaintext` setzt sie um; `KKpsk2`
existiert nicht und ist keine versteckte Lücke. Drei Gründe, nach Gewicht
geordnet:

- **Auf der Gegenseite setzt es niemand um.** Der Server von Music Assistant
  hat gar keine Verschlüsselung — kein Noise irgendwo in `aiosendspin`, und
  keine Krypto-Bibliothek in seinen Abhängigkeiten. Der Klartextpfad ist also
  der, der heute gegen einen echten Server funktioniert, und eine
  Noise-Umsetzung ließe sich gegen nichts testen.
- **`KKpsk2` ist nicht die ganze Arbeit.** `KK` heißt, dass beide Seiten die
  statischen Schlüssel der anderen bereits halten — es braucht also ein
  Schlüsselpaar, dessen Persistenz und den öffentlichen Schlüssel des Servers,
  der über das Koppeln kommt: `CPACE-X25519-SHA512`, Kopplungstoken, ein
  Fehlerzähler, der nach fünf Fehlversuchen eine Geste verlangt. Das ist ein
  Teilprojekt, und jeder Teil davon ist aus demselben Grund wie oben nicht
  überprüfbar.
- **Die beiden `init`-Nachrichtenformen sind weiterhin unbestätigt** (siehe
  oben), und mit ihnen beginnt der Handschlag.

Die Entwurfsanweisung, der das folgt, steht bereits geschrieben: Baue den
Client so, dass der Handschlag eine SCHICHT ist, die man einschalten kann,
keine durch den Transport hindurch eingebackene Annahme. Das ist erledigt.
Die Schicht zu schreiben, bevor irgendetwas darauf antworten kann, ist der
Teil, den man lassen soll.

**Was noch aussteht und Hardware braucht:** Der Handschlag ist nie gegen ein
laufendes Music Assistant zu Ende gegangen. Bis dahin sind `client/init` und
`server/init` zwei Formen aus Prosa statt aus der Referenz, und der gesamte
Verbindungspfad ist unbewiesen.

**Die Ausrichtung erfolgt durch Stille am Anfang und danach durch Korrektur
auf Sample-Ebene, und beide Hälften werden gebraucht.** Nichts auf dem Gerät
steuert, wann das Vorlaufgatter öffnet; ohne Auffüllen spielt das erste Sample
also in dem Moment, in dem der Puffer zufällig voll wurde. Die Musikebene
spielt der Reihe nach, was sie bekommt, also verzögern N Bilder Stille vor dem
Ton ihn um genau N Bilder. Danach läuft die Hardware in ihrem eigenen Takt,
und `delay` macht das sichtbar. Die Korrektur wird **über das Stück verteilt
und um einen halben Schritt versetzt**, damit keine Anpassung auf eine
Stückgrenze fällt — zwei an einem wiederholten oder fehlenden Bild
zusammengefügte Stücke legen die ganze Korrektur auf die Naht, und genau dort
ist ein Sprung am ehesten hörbar.

**Die angekündigte Formatliste ist nach Dekodierbarkeit geordnet, und diese
Reihenfolge ist eine Sicherheitseigenschaft, keine Vorliebe.** Der Server
nimmt die höchste Priorität des Clients, die er kodieren kann, und die Spec
verlangt, dass er alle drei kodiert — der erste Eintrag *ist* also der
gewählte. FLAC ist der Messung nach lohnend (3,68 % eines Kerns gegen 0,97 %,
spart 929 kbit/s auf einer Strecke mit gemessenen 4,6–7,1 % Verlust) und wird
*hinter* PCM angekündigt, bis sein Dekoder existiert: Es zuerst anzukündigen
würde einen Strom garantieren, den dieses Gerät in Rauschen verwandelt, ohne
dass irgendwo ein Fehler auftaucht — die Frames kommen an, etwas legt sie aus,
und der Lautsprecher spielt das Ergebnis. Es bleibt in der Liste, weil
`client/hello` ein einziger Schuss ist und ein Weglassen bedeuten würde, dass
das Hinzufügen des Dekoders erst nach einem Neuverbinden wirkt.

**Das erste Sample zu platzieren ist die leichte Hälfte.** Der Quarz des
Lautsprechers läuft mit 47973 fps gegen 48000 — 560 ppm —, ein perfekt
gestarteter Strom liegt also nach einer Sekunde 0,56 ms daneben und nach einer
Minute 34 ms, gegen eine Spec-Untergrenze von ±1 ms. Korrigiert wird durch
Weglassen und Verdoppeln ganzer Samples statt durch Resampling: Ein Resampler
mit variabler Rate ist unhörbar und kostet CPU, die dieses Gerät nicht übrig
hat, während bei einem Sample von 1786, verteilt statt am Stück angewandt, das
Weglassen aus einem anderen Grund unhörbar ist. Zwei Zahlen tragen die Regel —
ein **Totband**, ohne das der Korrektor ewig um die Null jagt und die Tonhöhe
dauernd moduliert, und eine **Korrekturrate, die den Fehler des Quarzes
übersteigen muss**. Die erste Fassung war auf 500 ppm gedeckelt, gegen die 560
des Quarzes, und konnte nie aufholen; das sieht genauso aus wie gar kein
Korrektor, und gefunden hat es der Test, der die beiden Raten vergleicht.

**`client/init` und `server/init` sind die einzigen Formen aus Prosa.** Alles
andere wurde aus `aiosendspin` abgelesen, der Umsetzung, die Music Assistant
betreibt, denn Prosa sagt nicht, welche Felder optional sind, und ein als
`null` gesendetes Feld, wo der Server es abwesend erwartet, ist eine andere
Nachricht. Die beiden gehören zum Austausch vor der Verschlüsselung und sind
unbestätigt, bis der Handschlag einmal gegen einen echten Server durchgelaufen
ist; der Rest des Protokolls ist nicht das Risiko.


Synchronisierte Multiroom-Wiedergabe über das Sendspin-Protokoll der Open
Home Foundation, das Music Assistant nativ spricht (WebSocket, Port 8927,
`/sendspin`). Platzierung entschieden am 2026-08-22: **Der Client läuft auf
dem Gerät und spricht direkt mit Music Assistant**, nicht im Controller.

**Dieser Abschnitt wird voraussichtlich mehrere Durchgänge brauchen, bis er
sich setzt**, und ist zum Bearbeiten geschrieben, nicht um fertig auszusehen.
Was ein Datum und einen Namen trägt, ist entschieden; alles andere ist offen,
und eine hier festgehaltene Entscheidung darf neu aufgerollt werden — der Sinn
des Aufschreibens ist, dass der nächste Durchgang von der aktuellen Position
startet, statt sie neu herzuleiten. Gebaut ist davon noch nichts.

### 6.1 Es ist ein zweiter Erzeuger, keine dritte Ebene

Das ist der Teil, den man vor jedem Code richtig haben muss. Sendspin trägt
dasselbe, was die Musikebene schon trägt — Audio von Music Assistant — nur auf
einem anderen Weg:

```
heute      MA → HA → Controller (em_player) → 0x04 → Musikstrom des Geräts
sendspin   MA ─────────────────────────────────────→ Musikstrom des Geräts
```

Also **kein neuer Frame-Typ, kein neuer Mischereingang, keine neue Zeile in
der Besitzleiter**. Der `audioStream` für Musik bekommt eine zweite Sache, die
ihn füllen kann, und alles, was für den ersten Erzeuger durchdacht wurde — das
Absenken, `music_flush`, sättigendes Mischen, das Vorlaufgatter, die
Aussetzerbuchhaltung — gilt unverändert. Die Arbeit ist ein Client und eine
Uhr, kein Audiopfad.

Die Alternative (der Controller betreibt den Client und streamt über `0x04`
weiter) wurde verworfen: Unsere Ebene trägt keine Zeitstempel, die Wiedergabe
landete also dann, wann der ~5,5-s-Gerätepuffer sie zufällig leerte.
Sample-genaue Synchronität ist der ganze Zweck des Protokolls, und sie
überlebt diesen Zwischenschritt nicht.

### 6.2 Was das Gerät umsetzen muss

| Teil | Anforderung | Kosten | Belegt durch |
|---|---|---|---|
| Auffinden | mDNS — der Server kündigt `_sendspin-server._tcp.local` an, der Client `_sendspin._tcp.local` | wir betreiben ohnehin mDNS für `_emcontroller._tcp` | Spec |
| Verschlüsselung | Die Spec sagt **verpflichtend** (Noise `KKpsk2`, Server als Initiator, Client als Antwortender, über einfaches `ws://`) — **aber MA setzt nichts davon um**, siehe 6.5 | `flynn/noise`, reines Go | Spec + MA-Quelltext |
| Chiffrensuite | `25519_ChaChaPoly_SHA256` oder `25519_AESGCM_SHA256`; Server unterstützen beide, Clients brauchen eine | ChaCha nehmen — der A53 hat keine AES-Befehle | Spec |
| Codec | Server MÜSSEN `pcm`, `flac` und `opus` unterstützen; der Client kündigt im `player@v1`-Objekt von `client/hello` an, was er will | siehe unten | Spec |
| Uhr | Der Client MUSS den Zeitfilter-Algorithmus (2-D-Kalman) nutzen, um Serverzeitstempel auf seine lokale Uhr abzubilden | braucht eine lokale Wiedergabeuhr — die haben wir | Spec |

**Codec: FLAC zuerst, und Opus ist keine Eintrittshürde.** Die Einschätzung
vom August hatte Opus über cgo (armv7a/API 22) als Blocker. Es ist keiner —
weil der Server alle drei unterstützen muss, darf der Client nur FLAC
ankündigen, und es gibt FLAC-Dekoder in reinem Go. Das ist etwa die halbe
Bitrate von PCM, verlustfrei, und ohne cgo. **Per Messung am 2026-08-22
entschieden — FLAC ankündigen**; 6.4 hat die Zahlen und den Vergleich mit PCM.
Opus wird bewusst zu einer später mitzunehmenden Bandbreitenoptimierung.

**Die Uhr ist der Grund, warum das überhaupt möglich ist.** Gemessen am
2026-08-10 an `/proc/asound/card0/pcm23p/sub0/status`: `hw_ptr` rückt alle
~2,4 ms in Schritten von 112–144 Bildern vor — rund 15× feiner als eine
Periode, und 47973 fps gegen 48000 nominal (−0,056 %). Der Treiber meldet eine
echte DMA-Position statt Software-Buchhaltung, geplante Wiedergabe auf ±0,5 ms
ist also plausibel. Software-Buchhaltung wäre alle 42,7 ms in Sprüngen von
2048 Bildern vorgerückt, und dieser ganze Abschnitt wäre unmöglich.

### 6.3 Besitz und Schlichtung

| # | Voraussetzung | Aktion | Status |
|---|---|---|---|
| S1 | Sendspin-Sitzung beginnt | das Gerät wird Erzeuger der Musikebene; der Controller wird informiert, damit die `media_player`-Entität korrekt meldet | [vorgeschlagen] |
| S2 | Sprachgespräch während Sendspin-Wiedergabe | unverändert — `duck on`, Musik um `duckDb` abgesenkt, Antwort auf `0x02` | [vorgeschlagen] |
| S3 | Der Controller sendet `0x04`, während eine Sendspin-Sitzung die Ebene besitzt | **HA gewinnt** — das Gerät verlässt die Gruppe sauber und spielt dann `0x04`. Nie verschachtelt (Invariante 7) | [vorgeschlagen] |
| S4 | Sendspin-Sitzung endet | Erzeuger freigegeben, `music_flush`-Semantik unverändert | [vorgeschlagen] |

S3 ist der wirklich neue Fehlermodus. „Spiel etwas Jazz", zum Gerät gesagt,
läuft über HA und kommt auf `0x04` an; eine aus der MA-App gestartete
Sendspin-Gruppe kommt über den Socket. Beides ist Music Assistant, beides ist
legitim, und beides zu summieren ist Rauschen.

**Entschieden am 2026-08-22 (Wil): HA gewinnt — es ist die direkte Bitte der
Nutzerin.** Ein Gerät, das über HA um Musik gebeten wird, verlässt die
Sendspin-Gruppe und spielt, worum es gebeten wurde.

Drei Dinge, die diese Regel *nicht* bedeutet, deren Fehldeutung jeweils echte
Folgen hätte:

- **Sie gilt nicht für Sprache.** Ein Sprachgespräch oder eine Durchsage
  konkurriert überhaupt nicht um die Musikebene — es senkt ab (V1, S2). Diese
  Regel betrifft nur die beiden MUSIK-Erzeuger, und der ganze Sinn der zweiten
  Ebene ist, dass der höchstpriorisierte Ton auf dem Gerät diesen Streit nicht
  gewinnen muss.
- **Verlassen ist nicht Ignorieren.** Das Gerät muss die Sendspin-Sitzung
  ordentlich beenden, statt aufzuhören, den Socket zu lesen: Ein Server, der
  weiter zu einem Client streamt, der still nicht mehr abspielt, füllt einen
  Puffer, den niemand hört, und die Sicht der Gruppe auf das Gerät bleibt
  falsch. Was auch immer der saubere Austrittspfad der Spec ist — den nehmen.
- **Es ist keine Prioritätsordnung.** Sendspin sitzt nicht auf einer festen
  Sprosse unter HA — es besitzt die Musikebene, wann immer HA sie nicht
  verlangt. Die letzte direkte Bitte gewinnt, dieselbe Form wie bei V4, wo ein
  Nutzerbefehl während eines Gesprächs unser automatisches Fortsetzen
  überstimmt.

**Kein Wiedereintritt, wenn die über HA geroutete Musik endet** (Wil,
2026-08-22). Ein stiller Wiedereintritt bringt Ton in den Raum, um den in
diesem Moment niemand gebeten hat, und wer die Gruppe gestartet hat, kann sie
erneut starten. Neu aufrollen, falls es in der Praxis stört — der Preis, hier
falsch zu liegen, ist ein zusätzlicher Fingertipp, und das ist die billige
Richtung, falsch zu liegen.

### 6.4 Was noch nicht bekannt ist

- ~~**CPU.**~~ **Am 2026-08-22 auf EA-Testgerät 01 (v2.12.0) gemessen, und es
  passt.** `device/tools/sendspin_bench` gegen 30 s rosa Rauschen bei
  607 kbit/s (40 % von PCM), Wanduhrzeit mit `GOMAXPROCS=1` auf einem echten
  Gerät, Scheduler-Konkurrenz also enthalten statt versteckt:

  | Pro Sekunde Audio | % eines Kerns |
  |---|---|
  | FLAC-Dekodierung | 3,28 |
  | ChaCha20-Poly1305 bei FLAC-Rate | 0,40 |
  | ChaCha20-Poly1305 bei PCM-Rate | 0,97 |
  | **gesamt, `flac` ankündigen** | **3,68** |
  | **gesamt, `pcm` ankündigen** | **0,97** (+929 kbit/s auf der Leitung) |

  X25519 braucht 7,74 ms, der KKpsk2-Handschlag also ~23 ms einmal pro
  Verbindung — unerheblich. **FLAC ankündigen**: Es kostet 2,7 Punkte CPU
  gegenüber PCM und spart 929 kbit/s auf Strecken, von denen bekannt ist, dass
  diese Flotte darauf stottert (4,6–7,1 % Verlust gemessen, #139/#140). Eine
  Strecke, die wir als grenzwertig kennen, gegen einen Kern zu tauschen, von
  dem wir 3,68 % Luft haben, ist keine knappe Entscheidung.

  Was das **nicht** abdeckt: WebSocket-Framing, den Pufferplaner und den
  Zeitfilter selbst. Alles klein, nichts null — behandle 3,68 % als Untergrenze,
  nicht als Budget.
- **32 Bit.** Jede getestete Sendspin-Plattform ist 64-bittig (Pi 3/4/5,
  Zero 2 W). Wir sind 32-bit-ARM. Nichts in der Spec hängt an der Wortbreite,
  aber niemand hat es dort laufen lassen.
- **`sendspin-go` ist eine Referenz, keine Abhängigkeit.** v1.2.0 hat das
  oto-Backend fallen lassen, um auf malgo/miniaudio zu vereinheitlichen — sein
  Ausgabepfad ist damit fest auf eine cgo-Audiobibliothek verdrahtet, die wir
  nicht brauchen: Uns gehören tinyalsa und der Mischer.

### 6.5 Von Music Assistants tatsächlicher Umsetzung abgelesen

Am 2026-08-22 geklärt, indem `aiosendspin` **6.0.5** gelesen wurde, so wie es
im laufenden Add-on ausgeliefert ist — nicht die veröffentlichte Spec und
nicht der main-Zweig auf GitHub, denn das ist der Code, mit dem unsere Geräte
tatsächlich sprechen würden. Drei Fragen geschlossen und eine Einschränkung
gefunden.

**MAs Server setzt KEINE Verschlüsselung um.** Es gibt nirgends in
`aiosendspin` Noise (jeder Treffer auf „noise" ist Kalman-Prozessrauschen in
`client/time_sync.py`), und seine Abhängigkeiten sind `aiohttp`, `mashumaro`,
`orjson`, `zeroconf` plus `av`/`numpy`/`pillow` für das Server-Extra — gar
keine Krypto-Bibliothek. Ein Klartext-`ws://`-Client funktioniert heute also
gegen MA, und das „verpflichtend" der Spec ist für diesen Server ein Wunsch.

**Noise deshalb nicht wegarchitektieren.** Die Spec verlangt es, MA setzt es
vielleicht später um, und bei 0,40 % eines Kerns ist es billig mitzuführen.
Baue den Client so, dass der Handschlag eine zuschaltbare Schicht ist und
keine durch den Transport hindurch eingebackene Annahme.

**Format-Neuverhandlung mitten im Strom IST umgesetzt** (`player/v1.py:696`,
`on_stream_request_format`). Ein aktiver Strom nimmt einen ausdrücklichen
Zweig, der die Audioanforderungen neu aufbaut und `stream/start` bis zum
nächsten Audiostück verschiebt, damit der Codec-Kopf mitreist. Der Plan aus
6.2 für das Einstecken der Klinke — `channels: 1` normalerweise, `channels: 2`
bei gestecktem Stecker — wird also vom Server unterstützt und nicht bloß von
der Spec erlaubt.

**⚠ Die Einschränkung, die beißen wird: Eine Anfrage muss GENAU zu einem vom
Client angekündigten Format passen, und das Scheitern ist still.** Der Server
filtert die `supported_formats` aus `client/hello` durch
`filter_encodable_formats` und verlangt dann, dass das vollständige Tupel
`(codec, sample_rate, bit_depth, channels)` in dieser Liste steht. Steht es
nicht drin, protokolliert er eine Warnung **auf dem Server** und fällt still
auf das Grundformat zurück. Dem Client wird nichts gesagt, und er bekommt
einfach weiter, was er hatte.

Das Gerät muss also in seinem ersten `client/hello` **jede** Kombination
aufzählen, um die es später bitten könnte — mono *und* stereo, und jeden
PCM-Rückfall. Nur mono anzukündigen und später beim Einstecken der Klinke
stereo zu verlangen sähe aus, als funktioniere es, änderte nichts und hinterließe
auf dem Gerät keine Spur.

**`client/goodbye` trägt einen `reason` und ist der saubere Austrittspfad**,
den S3 verlangt (`models/core.py:295`, behandelt in
`server/connection.py:737`). Die S3-Regel — HA gewinnt, das Gerät verlässt die
Gruppe sauber — hat damit eine Nachricht zum Senden statt einer Lücke.

**Ein Parameter zum Herleiten, nicht zum Raten:** `client/hello` trägt
`buffer_capacity`, „maximale Größe in Byte an komprimiertem Audio im Puffer,
das noch nicht gespielt wurde". Das ist eine Aussage über *unseren* Puffer
(`audioChanDepth` ≈ 5,46 s), ausgedrückt in komprimierten Bytes, und der
`BufferTracker` des Servers taktet danach. Es falsch zu setzen heißt, dass der
Server uns entweder aushungert oder überfährt.

---

## 7. Offene Fragen

- **Q1 — soll Musik während eines Gesprächs starten dürfen, auf ihrer eigenen
  Ebene?** Heute halten `play/resume/pause/stop` nur die Absicht fest und
  rühren die Leitung nicht an, solange ein Gespräch den Lautsprecher besitzt —
  ein vom Handy gestarteter Strom bleibt also still, bis die Antwort fertig
  ist (#262). Jetzt, wo Musik eine eigene Ebene hat, ist der Grund für die
  pauschale Regel schwächer als zu ihrer Entstehungszeit. Unentschieden.
- ~~**Q2 — wohin gehört die Klangkette?**~~ **Geklärt — auf das Gerät.** Siehe
  §8. Sendspin hat daraus eine Entscheidung statt einer Vorliebe gemacht: Eine
  Sendspin-Sitzung geht MA → Gerät und kreuzt den Controller nie, eine Kette im
  Controller würde also Sprache und über HA geroutete Musik formen und
  synchronisierte Musik stillschweigend nicht — derselbe Lautsprecher klänge
  unterschiedlich, je nachdem, welche App das Stück gestartet hat.
- **Q4 — ENTSCHIEDEN am 2026-09-07: Der Alarm wartet nie, die Durchsage schon
  (#373).** Beide schreiben heute `0x02`, und nur `_ring_timer_alarm` fragt
  vorher — was verkehrt herum ist: Ein Timer muss genau dann losgehen, wenn er
  abläuft, also stellt sich ausgerechnet der Schreiber zurück, dessen Timing
  der ganze Punkt ist. Gemessen am 2026-08-28: Eine Durchsage, die zwischen
  zwei Klingelsalven landet, ist hörbar; eine, die während einer Salve landet,
  nicht.

  Die Regel lautet: **die Musik zugunsten des Alarms verstummen lassen, den
  Alarm zugunsten der Antwort absenken**. Das löst den Dreierfall — Musik
  läuft, Gespräch aktiv, Timer feuert — ohne neue Firmware:
  `Mixer.Mix(voice, music, target)` nimmt genau zwei Eingänge und senkt nur die
  Musikseite ab, der Alarm reitet also auf der Musikebene, die Musik ruht,
  solange er klingelt, und das vorhandene Absenken des Geräts erledigt den
  Rest. Gekoppelt an `audio_mix`, denn Firmware ohne diese Fähigkeit spielt
  `0x04` nie, und ein stummer Timer ist der schlimmste verfügbare Fehler;
  diese Geräte behalten `0x02`, wobei der Alarm die Ebene übernimmt.

  Die Durchsage wartet, bis eine Antwort fertig ist, und stellt sich hinter
  andere Durchsagen an, mit einer Obergrenze. Sie für das gesamte Klingeln zu
  blockieren ist weiterhin falsch — HA hält `_is_announcing`, und 120 s
  `MAX_RING_S` ließen jede zweite Durchsage scheitern —, aber das ist das
  UNBEGRENZTE Warten. Auf die gerade laufende Salve zu warten dauert unter zwei
  Sekunden, und die Warnung so zu lesen, als verbiete sie beides, ist der
  Grund, warum das so lange offen lag.

  Offen: Die Absenktiefe des Alarms will einen eigenen Wert statt `duckDb`, das
  für ein Musikbett unter Sprache abgestimmt wurde; und das Fortsetzen der
  Musik nach dem Abweisen setzt bei einem nicht spulbaren Strom an der
  Live-Kante wieder ein, ein 30-s-Alarm kostet also 30 s des Stücks.

  **Das gemeinsame `playback_done`-Event ist behoben** (#481): Es ist jetzt eine
  FIFO-Warteschlange von Wartenden je Wiedergabe, eine Gerätemeldung befriedigt
  also nicht mehr zwei.

- **Q3 — wem gehört der Lautsprecher, wenn die Klinke belegt ist?** Ein
  Stecker in der Buchse verschlechtert das gesamte Audio-Teilsystem
  (#117/#141) und kann bei laufender Musiksitzung alles verstummen lassen,
  auch Sprache. Das ist ein Hardware- bzw. HAL-Fehler und kein Besitzproblem,
  aber es präsentiert sich als Besitzfehler und soll hier benannt sein, damit
  es nicht erneut als solcher diagnostiziert wird.

---

## 8. Die Klangkette wandert auf das Gerät **[gebaut — 3.0.0]**

Am 2026-08-22 entschieden UND GEBAUT (Wil): **Der gesamte Ausgabepfad läuft
auf dem Gerät, und der Controller liefert die Stellschrauben dafür.** Portiert
in `device/internal/outchain`, eingehängt bei `pcm_speaker.silenceLoop`,
gekoppelt an die Fähigkeit `output_chain`. **Auf Hardware noch nicht gehört**
— jede Aussage unten ist gegen die Referenzumsetzung oder per Test belegt,
nicht per Ohr. Der Umfang ist ausschließlich der AUSGABE-Pfad — EQ, Limiter,
Bass-Schutz und der Mix. Die Verarbeitung auf der Eingangsseite (`em_ns.py` /
DTLN auf dem zur Spracherkennung gehenden Mikrofonstrom) bleibt beim
Controller und ist ein eigener Arbeitsstrang: Sie ist ein neuronales Netz auf
dem Weg zur Spracherkennung und nicht zum Lautsprecher, und nichts hier
verlangt ihre Verlagerung.

### 8.1 Warum — drei Gründe, nach Gewicht geordnet

- **Die Abstimmungsverzögerung bricht von Sekunden auf eine Periode
  zusammen.** Im Controller erreicht eine Parameteränderung nur Samples, die
  noch nicht gesendet sind, und der Musikvorlauf läuft `LEAD_S` = 4,0 s voraus
  in einen bis zu 5,46 s tiefen Puffer — eine Änderung ist also **frühestens
  ~4 s später** zu hören, und eine Sprachantwort hört sie nie, weil die Kette
  pro Strom gebaut wird. Auf dem Gerät sitzt die Kette am ALSA-Schreibvorgang,
  ein Config-Push landet also innerhalb einer Umlaufzeit plus einer Periode
  (~43 ms). Das sind nach Gehör abgestimmte Geschmacksparameter, und vier
  Sekunden der alten Einstellung reichen, um den Vergleich zu verwischen.

  Das ist DASSELBE Argument, das das Ducking auf das Gerät gezwungen hat
  (Prinzip 3: Ton, der den Controller verlassen hat, lässt sich nicht
  zurückholen). Die EQ-Abstimmung hat dieselbe Form.
- **Eine Kette nach dem Mischen ist die richtige Topologie, und die heutige
  ist nicht erreichbar.** Der Controller betreibt **zwei unabhängige** Ketten
  — `em_controller.py:1402` für die Antwort, `em_player.py:544` für den
  Medienstrom —, also sieht **kein Limiter jemals Sprache und Musik
  summiert**. Dahinter steht heute nur die Sättigung des Mischers.
- **Sendspin macht daraus einen Zwang statt einer Vorliebe.** Eine
  Sendspin-Sitzung geht MA → Gerät und kreuzt den Controller nie; eine Kette im
  Controller würde Sprache und über HA geroutete Musik formen und
  synchronisierte Musik stillschweigend nicht — derselbe Lautsprecher klänge
  unterschiedlich, je nachdem, welche App das Stück gestartet hat.

Es folgt daraus keine Protokoll- oder Oberflächenarbeit: Alle sieben Schlüssel
(`eqBands`, `eqLoudness`, `limiter*`, `bassGuard*`) reiten bereits auf dem
Config-Push mit und werden vom Gerät derzeit ignoriert.

### 8.2 Anforderungen — alle erfüllt, keine gehört

R1–R7 unten waren die Abnahmekriterien. Was sie gekostet haben, festgehalten,
weil die nächste Portierung es wissen will:

- **R5 (Übereinstimmung mit den Fixtures) kam EXAKT heraus** — alle fünfzehn
  Fälle, Fehler −Inf dB, Spitzenunterschied 0 LSB, einschließlich jedes
  Parameterübergangs und des Flush-Ausklangs. Erreichbar statt Glück: float64
  behalten, scipys transponierte Direktform II behalten, jeden Ausdruck in der
  algebraischen *Gestalt* der Referenz belassen und das gemeinsame
  Verstärkungsgesetz in seiner arithmetischen REIHENFOLGE schreiben.
- **R7 (Festkomma gegen Fließkomma) ist beantwortet: float64.** Neun Biquads
  plus eine Frequenzweiche bei 48 kHz sind ein paar Mflop/s. Es gab nie einen
  Grund, zu etwas Schmalerem zu greifen, und float64 ist das, was die
  Übereinstimmung beweisbar macht.
- **R4 (kein Klicken) brauchte erst eine Messung, dann einen Mechanismus** —
  siehe 8.3.
- **R2 wurde aufgeschrieben und nicht gebaut, und das ist die teure Hälfte.**
  Die Gerätehälfte dieser Portierung ging mit angekündigter Fähigkeit in
  Betrieb, während der Controller weiterhin jedes Gerät formte, das sie
  ankündigte — zwei Limiter hintereinander auf genau der Firmware, für die die
  Portierung gedacht war, mit einer Dokumentation, die etwas anderes sagte. Es
  lässt keinen Test scheitern, wirft nichts, und präsentiert sich als „die neue
  Firmware klingt schlechter" — dieselbe Form wie das DAC-Übersteuern in 8.4.
  Das Gatter lebt jetzt in `controller/em_outchain.py`: eine reine Funktion mit
  Unit-Tests, dazu ein Quelltextwächter, der scheitert, wenn eine Funktion in
  `em_controller.py` oder `em_player.py` eine Kette baut, ohne ihn zu fragen.
  Ein `Bypass` statt einer deaktivierten Kette, denn ein deaktivierter
  `BassGuard` summiert die Hälften der Frequenzweiche weiterhin, und diese
  Summe ist ein **Allpass** — im Betrag flach, aber nicht die eigenen Bytes des
  Aufrufers.

#### Die Kriterien

| # | Anforderung | Warum |
|---|---|---|
| R1 | An eine **Fähigkeit `output_chain`** koppeln, unabhängig von jeder Sendspin-Fähigkeit | Ein Gerät könnte Sendspin sprechen, ohne eine lokale Kette zu haben; den Controller dafür zurückzustellen liefert unbearbeitetes Audio aus. Dieselbe Trennung wie `oww_shadow` gegen `oww_trigger` |
| R2 | Der Controller tritt **nur** bei Ankündigung zurück; sonst formt er wie heute | Auf altes Verhalten zurückfallen, nie auf eine falsche Antwort. Zwei Ketten hintereinander sind zwei Limiter hintereinander, und das ist hörbar falsch |
| R3 | Die Kette sitzt **nach dem Mischen**, einmal | 8.1 |
| R4 | **Kein Klicken bei irgendeiner Parameteränderung** | Ausschlusskriterium (Wil, 2026-08-22). Siehe 8.3 |
| R5 | Portierung gegen **goldene Fixtures** validiert, erzeugt aus der Python-Kette | Präzedenzfall: `internal/wakeword/fixture` — der Grund, warum das Wakeword auf dem Gerät bei seiner Ankunft vertrauenswürdig war |
| R6 | Python-Kette **behalten**, als Referenzumsetzung und Rückfall für alte Firmware | Kein Gerüst zum Abreißen |
| R7 | Festkomma gegen Fließkomma **messen, nicht annehmen** | Der Q15-Präzedenzfall des Mischers deckt eine Verstärkungsmultiplikation ab; ein Limiter und ein Mehrband-Schutz sind präzisionsempfindlicher. Der A53 hat VFP, float32 steht also zur Debatte |

### 8.3 R4 — wie „kein Klicken" tatsächlich erreicht wird

**In-Place zu aktualisieren ist notwendig und NICHT hinreichend.** Den
Filterzustand zu bewahren vermeidet den Neuaufbau-Einschwinger (den Fehler,
den der Kommentar in `em_player` beschreibt), aber nicht den Einschwinger
dadurch, dass sich Koeffizienten unter einem laufenden Filter ändern. Und die
naheliegende Lösung ist eine Falle: **rohe Biquad-Koeffizienten zwischen zwei
stabilen Filtern zu interpolieren kann durch instabile Zwischenzustände
laufen**, naives Glätten fliegt also auseinander, statt zu klicken.

- **Parameterrampen mit konstanter Steigung**, genau wie `duckRampPeriods` es
  bereits fürs Absenken tut — bewusst konstante Steigung und nicht
  proportional, damit es eine Dauer ist und keine Zeitkonstante, die sich an
  die letzten paar Prozent heranschleicht.
- **Überblendung mit zwei Instanzen für die Biquads**: alte und neue parallel
  laufen lassen, über ~50–100 ms überblenden, die alte fallen lassen.
  Bedingungslos stabil, weil keine der Instanzen je interpoliert wird. Kostet
  die CPU einer zusätzlichen Kette, und das nur während des Überblendfensters.
- **Limiter und Bass-Schutz sind leichter** — Änderungen an Schwelle und
  Release liegen im Verstärkungsbereich und glätten sich von selbst, sofern
  der Detektorzustand übernommen wird.

**Per Test festgenagelt, nicht per Ohr:** Eine sprunghafte Änderung eines
beliebigen Parameters darf keinen Sprung von Sample zu Sample über einer
Schwelle erzeugen. Auf dem Host in Go testbar, ohne Hardware.

**GEMESSEN am 2026-08-22, und die naheliegende Sonde erkennt nichts.** Eine
Parameteränderung ist nur dann als Sprung hörbar, wenn der FILTERZUSTAND
echte Energie hält — eine tiefe Frequenz mit Pegel. Bei einem 1-kHz-Ton
erzeugt keine dieser Änderungen einen Sprung über der eigenen Steilheit des
Signals:

| Signal | Änderung | roher Sprung | überblendet | Spitze |
|---|---|---|---|---|
| 60 Hz @20000 | Loudness an (Zustandsreset) | 53351 | 6144 | 72863 |
| 60 Hz @20000 | alle Bänder 0 → +12 | 61469 | 7759 | 113938 |
| 60 Hz @20000 | Low-Shelf +12 → −12 | 1065 | 578 | 80607 |
| 1 kHz @8000 | irgendeine der obigen | auf oder unter der eigenen Steilheit des Signals | | |

Die oberste Zeile ist ein Sprung von **73 % des Signals**, und es ist der
Fall mit Zustandsreset — die Eigenheit also, die die Portierung bewusst aus
`em_eq` nachbildet, ist der schlimmste Übeltäter, und die Überblendung ist
das, was das Nachbilden sicher statt nur getreu macht. Gemessene Reduktion:
**8,7×**.

**Lineare Überblendung, nicht leistungsgleich.** Leistungsgleich ist die
reflexhafte Wahl und hier falsch: Sie hält den Pegel für UNKORRELIERTE
Quellen, deren Leistungen sich addieren. Diese beiden sind dasselbe Signal
durch ähnliche Filter, ihre Amplituden addieren sich also, und eine
Cos/Sin-Blende würde in der Mitte um bis zu 3 dB anschwellen — ein hörbares
Aufblähen bei jeder Parameteränderung.

### 8.4 Bekanntes Risiko

Dass die Festkomma- (oder Fließkomma-)Portierung **hörbar anders** klingt als
die Python-Kette, ist der einzige Punkt hier ohne bekannte Methode — alles
andere ist Ingenieursarbeit. Es ist zugleich der Fehlermodus, mit dem dieses
Projekt Vorgeschichte hat: Das DAC-Übersteuern oberhalb von Unity Gain las
sich wochenlang als „Piper klingt schlechter als das Original". Miss es früh
statt zuletzt, und R5 ist das, was „anders" erkennbar macht, bevor es ein
Hörtest ist.

---

## 9. Invarianten — nicht brechen

1. **Sprache wird vom Ducking nie abgesenkt.** Nur die Musikebene trägt
   `duckTarget`.
2. **Ein Sprachgespräch leert nie die Musikebene** (F4 oben).
3. **Der Mischer sättigt, er läuft nie über** (`mix_test.go:149` nagelt das
   fest).
4. **Der Abschluss der Wiedergabe kommt vom Gerät**, nicht aus einer
   Dauerschätzung.
5. **`speaker_busy` wird in einem `finally` freigegeben.** [heute]
6. **Frame-Typen gelten je Richtung.** `0x04`/`0x05` sind nicht frei
   wiederverwendbar.
7. **Die Musikebene hat zu jeder Zeit genau einen Erzeuger, und HA gewinnt.**
   [vorgeschlagen #89] Controller-`0x04` mit einer Sendspin-Sitzung zu
   verschachteln summiert zwei unabhängige Ströme; eine direkte Bitte über HA
   beendet die Sendspin-Sitzung, statt sich mit ihr zu mischen, und das Gerät
   verlässt die Gruppe **sauber**, statt bei ihr zu verstummen (S3). Invariante
   2 bekommt hier einen zweiten Grund: Musik für ein Sprachgespräch zu leeren
   würde Ton verwerfen, mit dem eine synchronisierte Gruppe rechnet, und eine
   Neusynchronisation erzwingen.
8. **Die Klangkette läuft genau einmal.** [gebaut] Entweder formt der
   Controller oder das Gerät, entschieden von der Fähigkeit `output_chain` in
   `em_outchain.controller_shapes` und nirgends sonst. Beides sind zwei
   Limiter hintereinander und ist hörbar falsch; keines von beiden liefert
   Audio vor ±12-dB-Reglern aus, ohne dass etwas auffängt, was sie anheben
   (#231).
