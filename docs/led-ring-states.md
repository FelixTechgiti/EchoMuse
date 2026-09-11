# Zustandsmodell des LED-Rings

Der 12-LED-Ring (plus die eigene LED der Mute-Taste) ist die gesamte
Benutzeroberfläche von Revoice. Es gibt keinen Bildschirm und keine andere
Anzeige, der Ring trägt also die ganze Last, jemandem zu sagen, was das Gerät
tut.

**Grundanforderung:** Der Ring muss *unaufdringlich*, *konsistent* und
*verständlich* sein. Ein Ring, der aufleuchtet und dann ins Leere führt, ist
schlimmer als ein Ring, der nie geleuchtet hat.

Dieses Dokument ist die maßgebliche Verhaltensspezifikation: jeder
Ring-Besitzer, jedes Ereignis und der jeweilige Ausgang. Einträge sind
gekennzeichnet:

- **[heute]** — aktuell ausgeliefertes Verhalten (v2.9.7)
- **[vorgeschlagen]** — Entwurf, noch nicht gebaut, Freigabe ausstehend

---

## 1. Entwurfsprinzipien

1. **Nie einen Zustand zeigen, den das System nicht einhalten kann.**
   Optimistische Rückmeldung ist nur erlaubt, wo sie *begrenzt und aufgelöst*
   werden kann — vom Controller bestätigt oder sichtbar zurückgenommen.
2. **Rückmeldung lokal, Entscheidungen entfernt.** Dem Gerät gehört, was es
   aus erster Hand wissen kann (eine Taste wurde gedrückt, sein eigener
   Lautsprecherpuffer ist leergelaufen, es ist stumm). Dem Controller gehört,
   was Wissen erfordert, das dem Gerät fehlt (wurde ein Wakeword gesprochen,
   hat HA geantwortet, welches Gerät soll reagieren).
3. **Eine Bedeutung pro Signal.** Orange bedeutet bereits „Controller nicht
   erreichbar". Neue Fehlermodi lösen sich in vorhandenes Vokabular auf, statt
   Signale zu erfinden, die jemand erst lernen müsste.
4. **Jeder vorläufige Zustand löst sich auf.** Kein Zustand darf auf eine
   Annahme hin unbegrenzt gehalten werden. Vorläufige Anzeigen tragen eine
   Frist; Animationen tragen einen TTL-Totmannschalter.
5. **Gerätehoheitliche Zustände werden nie vom Netzwerk überschrieben.** Mute
   ist der Musterfall: Es muss stimmen, wenn der Controller fehlt, hängt oder
   lügt.

---

## 2. Ring-Besitzer — die Prioritätsleiter

Die höchste aktive Ebene gewinnt die physische Anzeige. Tiefere Ebenen
*zeichnen* ihren Zustand weiter auf (`baseLEDs`), damit der Ring beim Ablauf
korrekt zurückgegeben werden kann.

| # | Besitzer | Optik | Lebensdauer | Hoheit | Code |
|---|---|---|---|---|---|
| 1 | **Lautstärkebogen** | Cyan, N von 12 proportional | 2-s-Fenster (`volumeLEDSecs`) | Gerätelokal; nur physische Tastendrücke | `volume.go:145` |
| 2 | **Mute-Ring** | Dauerhaft rot `(180,0,0)` + Tasten-LED (gpio444, active-high) | Bis zum Aufheben; übersteht Neustart und OTA | **Gerätehoheitlich**, gespeichert in `/data/local/etc/revoice/state.json` | `mute.go:132` |
| 3 | **Verbindungszustand** | Oranges Sinus-Pulsieren (getrennt) / weißes langsames Pulsieren (Freigabe ausstehend) | Bis die Verbindung geklärt ist | Gerätelokal | `cmd/server.go:161,174` |
| 4 | **Gesprächs- / Medienanimation** | `solid` · `spin` · `rotate` · `pulse` · `meter` · `off`, in Szenenfarbe | Bis ersetzt oder `ttlSec` abläuft (30 s Zuhören / 135 s Spinner / je Antwort für den Pegel) | Vom Controller vorgegeben, vom Gerät gezeichnet | `animator.go:53` |
| 5 | **Richtungsüberlagerung** | Grundfarbe des Rings, am Beam-Winkel Richtung Weiß aufgehellt | Solange der Zuhör-Ring steht | Gerätelokal, braucht `listeningLEDs` | `server.go:269` |
| 6 | **Ruhe** | Alles aus | — | — | — |

### Unterdrückungsregeln [heute]

Sowohl Ebene 1 als auch Ebene 2 unterdrücken die Hardware-Anzeige für die
Ebenen 3–5, zeichnen aber weiterhin in `baseLEDs` auf:

```go
if s.volume.DisplayActive() || s.mute.IsMuted() {
    return          // server.go:381 (SetLEDs), server.go:275 (SetDirectionLEDs)
}
```

Zwischen Ebene 1 und 2 gewinnt der Lautstärkebogen für sein Fenster — beide
zeichnen direkt auf die Hardware, und der Bogen zeichnet zuletzt. Sein
Ablauftimer kennt den Mute-Zustand und stellt den roten Ring wieder her,
statt an den Controller zurückzugeben:

```go
if vc.isMuted != nil && vc.isMuted() {
    lc.SetLEDs(redRing...)      // volume.go:177
} else if expire != nil {
    expire()                    // → paintBaseLEDs()
}
```

Animationen der Ebene 4 laufen auf dem eigenen Taktgeber des Geräts und
leiten jedes Bild durch `SetLEDs`, erben also die Unterdrückungen und halten
`baseLEDs` aktuell. Das macht die Rückgabe nach dem Lautstärkebogen mitten in
einer Animation nahtlos: Der Animator hat `baseLEDs` das ganze unterdrückte
Fenster über aktualisiert, `paintBaseLEDs()` zeichnet also das *neueste* Bild,
und der nächste Takt (≤80 ms) macht normal weiter.

Das Ersetzen ist atomar über einen Generationszähler — eine veraltete
Animations-Goroutine kann nie über ihre Nachfolgerin malen
(`animator.go:45-48`).

---

## 3. Verbindungsverfügbarkeit — drei Zustände, nicht zwei

Lokale Rückmeldung muss die Verbindung kennen, und das verlangt einen
schärferen Begriff von Verfügbarkeit, als der Code ihn heute hat.

| Zustand | Bedeutung | Lokale Rückmeldung erlaubt? |
|---|---|---|
| **LINKED** | Control-WS registriert *und* jüngste eingehende Belege | Ja |
| **SUSPECT** | Socket vermutlich offen, aber keine jüngsten Belege — oder eine Interaktion blieb unbestätigt | Nein — als DOWN behandeln |
| **DOWN** | Socket geschlossen oder Freigabe ausstehend | Nein — Verbindungszustand zeigen (Ebene 3) |

### Das Problem mit dem, was existiert [heute]

```go
func (c *ControlClient) IsConnected() bool {
    return c.conn != nil        // control.go:131
}
```

`c.conn` wird nur nil, wenn die Leseschleife einen Fehler bekommt, und das
regelt `wsPongWait = 45 * time.Second` (`data.go:61`). Bei einer still
gestorbenen Verbindung — Controller gestoppt, WLAN-Blackhole,
Controller-Ereignisschleife hängt — meldet das Gerät also bis zu **45 Sekunden
lang „verbunden"**, nachdem der Controller weg ist, und das orange Pulsieren
beginnt nicht.

Optimistische Anzeige an `IsConnected()` zu koppeln erzeugt daher ein
45-Sekunden-Fenster, in dem der Ring zuversichtlich leuchtet und nichts
passiert. Dieses Fenster ist *genau* der Moment, in dem eine Person am
ehesten die Taste drückt — sie drückt ja, weil nichts reagiert hat. Es
erfüllt die Anforderung buchstäblich und verletzt sie vollständig.

Eine zweite Lücke: Die Socket-Lebendigkeit sieht keinen Controller, der zwar
verbunden, aber **nicht bedienfähig** ist — HA aus, Pipeline im Fehler,
Ereignisschleife blockiert.

### Passive Erkennung hat einen Boden [vorgeschlagen]

Eingehende Belege im Leerlauf kommen aus der Ping-Schleife des Controllers,
alle **30 s** (`em_controller.py:1674`). Eine passive Frischeschwelle kann
also nicht enger als ~35 s sein, ohne Fehlalarme zu erzeugen. **Passive
Lebendigkeit allein kann keine schnelle Erkennung liefern.** Das ist der
strukturelle Grund, warum der Entwurf unten auf Bestätigung je Interaktion
aufbaut statt auf einem Verbindungsflag.

### Bestätigung je Interaktion [vorgeschlagen]

Frag nicht „ist der Controller da?". Frag „hat der Controller *diese*
Interaktion quittiert?".

- Bei einer lokalen Interaktion sofort zeichnen und eine
  **Bestätigungsfrist** scharfstellen.
- Jede Controller-Nachricht, die den Ring beansprucht (`led_anim` / `leds`),
  bestätigt sie vor Fristende und ersetzt die vorläufige Anzeige. **Dieser
  Mechanismus existiert schon** — der Generationszähler tut genau das. Keine
  Protokolländerung.
- Läuft die Frist ohne etwas ab, wurde die Interaktion nicht eingelöst. Auf
  DOWN wechseln und das orange Pulsieren zeigen (Ebene 3).

Gemessene Grundlage: Der Controller gibt seine Antwort **unter einer
Millisekunde** nach Eintreffen eines Tastenereignisses aus, die Frist muss
also nur zwei Netzwerkschritte abdecken. Beobachtete Uplink-Latenz, Erkennung
→ erstes Mikrofonbild, über 79 Wake-Gespräche:

| Gerät | Median | p90 | max | RSSI |
|---|---|---|---|---|
| Office | 264 ms | 272 ms | 278 ms | −25 |
| Lounge | 260 ms | 294 ms | 952 ms | −52 |
| Retreat | 258 ms | 1046 ms | 1225 ms | −66 |

Der Ausläufer folgt dem RSSI, jede Frist muss also den schlechtesten Fall von
Retreat abdecken, nicht den typischen von Office. Siehe §6 Q3 — weil die
Frist im Gutfall nichts kostet, lautet die Antwort, sie großzügig statt eng zu
setzen, was RTT-Messungen bestätigend statt blockierend macht.

Nebennutzen: Eine unbestätigte Interaktion erkennt einen Verbindungsausfall
weit schneller als der 45-s-Pong-Timeout. Die Taste wird zur aktiven Sonde,
und lokale Rückmeldung und Verbindungsbewusstsein verstärken sich gegenseitig,
statt sich zu widersprechen.

---

## 4. Ereignis → Ausgang, in Tabellen

Verwendete Zustandsnamen: `IDLE`, `LISTENING`, `THINKING`, `PLAYING`, `MUTED`,
`VOL-DISPLAY` (2-s-Bogenfenster), `DISCONNECTED`, `PENDING`.

### 4.1 Aktionstaste (Punkt) — clickType 138

| # | Zustand | Verbindung | Aktion des Geräts | Ausgang am Ring | Controller | Status |
|---|---|---|---|---|---|---|
| A1 | IDLE | LINKED | Tastenereignis senden | *Nichts, bis der Controller antwortet* — der Ring leuchtet eine Umlaufzeit später | Startet Gespräch, sendet `led_anim` listening | [heute] |
| A2 | IDLE | LINKED | Tastenereignis senden **und Zuhör-Ring lokal zeichnen**, Bestätigungsfrist scharfstellen | Zuhör-Ring **sofort** | Bestätigt mit `led_anim` listening und ersetzt die vorläufige Anzeige | [vorgeschlagen] |
| A3 | IDLE | LINKED, keine Bestätigung vor Fristende | Vorläufige Anzeige zurücknehmen, Verbindung als DOWN markieren | **Harter Schnitt** auf orangenes Pulsieren — kein Überblenden (§6 Q4) | — | [vorgeschlagen] |
| A4 | IDLE | DOWN / SUSPECT | Nicht senden, keinen Gesprächszustand zeichnen | Orangenes Pulsieren läuft weiter (unverändert) | — | [vorgeschlagen] |
| A5 | LISTENING / THINKING / PLAYING | LINKED | Tastenereignis senden | Der Ring erlischt, wenn das Aufräumen des Controllers eintrifft | Bricht das Gespräch ab (`cancel_event` + `speaker_flush`); **nur lokal — HAs Pipeline läuft zu Ende, das Ergebnis wird verworfen** (`em_esphome.py:1158`) | [heute] |
| A6 | LISTENING / THINKING / PLAYING | LINKED | Tastenereignis senden **und Ring lokal löschen** (ein Druck während eines Gesprächszustands heißt eindeutig *abbrechen*) | Ring erlischt **sofort** | Bricht wie oben ab | [vorgeschlagen] |
| A7 | **MUTED** | Tippen | Wird mit `muted: true` gesendet; **der Controller verweigert das Gespräch** (`em_button.decide` → `BLOCKED`) | Roter Ring unverändert — **still, absichtlich** (siehe §6 Q1) | Nichts. Das Mikrofon öffnet nie: `mic_start` wird bei Mute geräteseitig abgewiesen | [heute] |
| A8 | **MUTED** | Halten | Wird mit `muted: true` gesendet | Roter Ring unverändert | **Feuert `long` an HA.** Ein Halten ist keine Sprache, das Mute hat dazu keine Meinung | [heute] |
| A9 | VOL-DISPLAY | LINKED | Tastenereignis senden **und den Halt des Bogens abbrechen** (`CancelVolumeDisplay`) | Der Bogen verliert die Hoheit; das Zuhör-Bild des Gesprächs zeichnet, sobald es eintrifft | Startet das Gespräch normal | [heute] |

### 4.2 Mute-Taste — clickType 113 (gerätelokal, verlässt das Gerät nie)

| # | Zustand | Aktion des Geräts | Ausgang am Ring | Controller | Status |
|---|---|---|---|---|---|
| M1 | IDLE | ADC-Mute für alle 4 Codec-Paare, in state.json sichern, Tasten-LED an | Dauerhaft rot; **unterdrückt alle Anzeigen des Controllers** | Über `mute_state` benachrichtigt | [heute] |
| M2 | LISTENING / THINKING / PLAYING | Wie M1 | Sofort dauerhaft rot; das LED-Aufräumen des abgebrochenen Gesprächs kommt *danach* und wird korrekt ignoriert | Bricht Gespräch ab + `speaker_flush` | [heute] |
| M3 | MUTED (aufheben) | ADC entstummen, Ring löschen, Tasten-LED aus, sichern | Ring schwarz; das nächste Controller-Bild zeichnet neu | Über `mute_state` benachrichtigt | [heute] |
| M4 | VOL-DISPLAY | Wie M1 — Rot wird direkt gezeichnet | Rot gewinnt (zuletzt gezeichnet); der Ablauf des Bogens kennt Mute und stellt Rot wieder her | Benachrichtigt | [heute] |
| M5 | DISCONNECTED | Wie M1 — **funktioniert ganz ohne Controller** | Dauerhaft rot ersetzt das orange Pulsieren | Keiner | [heute] |
| M6 | Start nach Neustart/OTA im Mute-Zustand | `RestoreMuted()` — ADC und Flag vor der Verbindung; Ring und Tasten-LED, sobald die LED-Initialisierung durch ist | Rot, vor jedem Controller-Kontakt | Keiner | [heute] |

Mute ist die Referenzumsetzung von Prinzip 5, und sein Verhalten **ändert
sich nicht**.

### 4.3 Lautstärketasten — clickType 115 / 114 (gerätelokal)

| # | Zustand | Aktion des Geräts | Ausgang am Ring | Status |
|---|---|---|---|---|
| V1 | IDLE | `Set(level, showRing=true)` | Cyanfarbener Bogen 2 s → schwarz | [heute] |
| V2 | LISTENING / THINKING / PLAYING | Wie V1 | Bogen 2 s → gibt mitten im Bild an die laufende Animation zurück | [heute] |
| V3 | MUTED | Wie V1 | Bogen 2 s → **roter Ring wiederhergestellt** (der Ablauf kennt Mute) | [heute] |
| V4 | DISCONNECTED | Wie V1 | Bogen 2 s → orangenes Pulsieren geht weiter | [heute] |
| V5 | Ferngesetzt (Controller / HA) oder `SeedVolume` beim Start | `Set(level, showRing=false)` | **Kein Bogen** — es steht niemand am Gerät | [heute] |

### 4.4 Ring-Nachrichten vom Controller

| # | Nachricht | Zustand | Ausgang am Ring | Status |
|---|---|---|---|---|
| C1 | `led_anim` listening (`solid`, `listening:true`) | jeder nicht unterdrückte | Zuhörfarbe der Szene; aktiviert die Richtungsüberlagerung | [heute] |
| C2 | `led_anim` `spin`/`rotate` (Nachdenken) | jeder nicht unterdrückte | Spinner auf dem Gerätetakt, 80 ms | [heute] |
| C3 | `led_anim` `meter` (Wiedergabe) | jeder nicht unterdrückte | Pulsiert mit dem Live-RMS des Lautsprechers am ALSA-Schreibvorgang — nur die **Sprachebene**, gemessen vor dem Musikmix (v2.10.0). Der AEC-Fernabgriff sieht bewusst die gemischte Ausgabe, denn genau die muss aus dem Mikrofon gelöscht werden; der Pegel darf das nicht, sonst pulsiert er zu einem Lied, das niemand visualisiert haben wollte | [heute] |
| C4 | `led_anim` `off` | jeder nicht unterdrückte | Ring schwarz | [heute] |
| C5 | Jedes `led_anim` / `leds` | MUTED oder VOL-DISPLAY | **In `baseLEDs` aufgezeichnet, nicht gezeichnet** | [heute] |
| C6 | Altes `leds`-Frame | jeder nicht unterdrückte | Ersetzt jede laufende Animation atomar (Generationszähler) | [heute] |
| C7 | Kein Ersatz innerhalb von `ttlSec` | Animation läuft | Der Totmannschalter löscht den Ring — Schutz gegen einen Controller, der mitten im Gespräch gestorben ist | [heute] |

### 4.5 Audio- und Verbindungslebenszyklus

| # | Ereignis | Zustand | Ausgang am Ring | Status |
|---|---|---|---|---|
| L1 | Control-WS registriert | PENDING / DISCONNECTED | Pulsieren hört auf; Mute-Ring wiederhergestellt, falls stumm, sonst Rückgabe | [heute] |
| L2 | Control-WS geschlossen (Fehler in der Leseschleife) | beliebig | `StopAnim()`, dann orangenes Pulsieren | [heute] |
| L3 | Wartet auf Freigabe durch Administrator | Start | Weißes langsames Pulsieren | [heute] |
| L4 | Verbindung still tot | beliebig | **Nichts für bis zu 45 s** — der Ring zeigt weiter den letzten Zustand | [heute] |
| L5 | Verbindung still tot | beliebig | Erkannt bei der nächsten Interaktion (§3) oder über eine Frischeschwelle für Eingehendes | [vorgeschlagen] |
| L6 | **Lautsprecherstrom endet** (EOS empfangen *und* Audiokanal leer) | PLAYING | **Der Controller schätzt das aus der Wanduhr und löscht den Ring auf langsamen Strecken zu früh** — gemessen bis zu 6,1 s zu früh | [heute] |
| L7 | Lautsprecherstrom endet | PLAYING | Das Gerät löscht den Ring bzw. gibt ihn selbst zurück, aus dem Signal, das es ohnehin protokolliert (`pcm_speaker.go:309`) | [vorgeschlagen] |

---

## 5. Warum L6 heute falsch ist

`_run_post_turn_playback` erfährt nie, wann die Wiedergabe tatsächlich endete
(`em_controller.py:746`):

```python
audio_duration = len(speaker_pcm) / (SPEAKER_RATE * 2) + SPEAKER_PRIME_SECONDS
remaining      = max(0.0, audio_duration - elapsed)
```

`elapsed` ist die Zeit des *Socket-Schreibens* und wird **abgezogen**, die
Schätzung schrumpft also genau bei den Strömen, die sie am längsten brauchen.
Gemessen gegen `delivery_ms` (das Eintreffen der `playback_stats` des Geräts —
ein echtes Ende-des-Tons-Signal) über die letzten 40 instrumentierten
Gespräche löschten 4 den Ring mehr als 0,5 s zu früh:

| Gespräch | tts | send_ms | delivery_ms | recv_span | Ring gelöscht |
|---|---|---|---|---|---|
| 07-24 23:53 Retreat | 2,6 s | 1154 | 9764 | 8193 | **6,1 s zu früh** |
| 07-24 22:31 Lounge | 5,0 s | 4296 | 9353 | 2039 | **3,2 s zu früh** |
| 07-24 05:26 Lounge | 9,6 s | 5364 | 13833 | 8275 | **3,1 s zu früh** |
| 07-22 22:14 Lounge | 4,6 s | 1 | 7417 | 4112 | **1,7 s zu früh** |

Jeder Fall geht mit aufgeblähtem `recv_span_ms` / `max_gap_ms` einher. Bei
gesunden Gesprächen ist die Schätzung rund 1 s *konservativ*, weshalb das nur
gelegentlich auffällt.

Das Gerät ist die einzige Partei, die weiß, wann sein eigener Puffer leer
läuft. Es erkennt und protokolliert genau das bereits. Nach Prinzip 2 gehört
das dem Gerät, und es braucht **keine Bestätigung** — es wird nichts
vorhergesagt.

---

## 5b. Abschluss bei Fristablauf [vorgeschlagen]

Läuft die Bestätigungsfrist ab, ist die Interaktion vorbei. Vier Dinge müssen
passieren, und das dritte hat Zähne.

**1. Ring — harter Schnitt auf Orange** (§6 Q4). Das Gerät ist jetzt im
Verbindungszustand DOWN.

**2. Das Mikrofon lokal zurücksetzen.** Zum Fristende kann das Gerät bereits
`mic_start_turn` bekommen haben (es geht `led_anim` in der Sendereihenfolge
des Controllers voraus), es kann also ein begrenzter Gesprächsstrom laufen.
Das Gerät muss ihn stoppen und zum durchgehenden Wake-Strom zurückkehren —
`StopMic()`, dann `StartMic(false)`.

**3. Dem Controller sagen, dass er das Gespräch aufgeben soll.** Eine
Best-Effort-Kontrollnachricht `turn_abort`. **Das ist verpflichtend, nicht
optional, und der Grund ist Privatsphäre, nicht Ordnung:** Ohne sie führt der
Controller ein vollständiges Sprachgespräch durch — streamt Mikrofonton an HA
—, während das Gerät Orange zeigt. Ein Gerät, das ohne Ring zuhört, ist genau
der Zustand, der niemals existieren darf. Der Controller gibt `voice_lock`
frei, bricht das Gespräch ab und stoppt den Mikrofonstrom.

Der Abbruch erreicht den Controller im Normalfall, denn die übliche Ursache
einer späten Antwort ist ein Controller, der *langsam, aber am Leben* ist. Ist
die Verbindung wirklich abgerissen, setzt die Wiederverbindung den Ring
ohnehin zurück (`leds_off` beim Config-Push, `em_controller.py:1638`), und das
Gespräch des Controllers scheitert mit dem Socket.

**4. Späte Eintreffende für die aufgegebene Interaktion abweisen.** Gürtel und
Hosenträger für das Rennen, bei dem der Abbruch und ein spätes `led_anim` sich
kreuzen. Das Gerät stempelt jede lokale Interaktion mit einer monoton
steigenden **Interaktions-Epoche**, sendet sie mit dem Tastenereignis, und der
Controller spiegelt sie auf den Ring-Nachrichten des Gesprächs. Ein
`led_anim` mit einer älteren Epoche als der aktuellen des Geräts wird
verworfen.

Das ist dasselbe Generationszähler-Muster, das der Animator schon nutzt
(`animator.go:45`), erweitert über die Leitung — eine veraltete Antwort kann
nie über ihre Nachfolgerin malen. `led_anim` pauschal für eine Weile zu
ignorieren wäre falsch: Es würde auch ein legitimes *neues* Gespräch aus einem
zweiten Tastendruck oder einem Wakeword unterdrücken.

### Zusammenspiel mit dem bereits vorhandenen Tasten-Rennen

In `handle_button_event` steckt ein latentes Rennen, das eine
Abschlusssemantik von harmlos zu sichtbar machen würde. Die
Start/Abbruch-Entscheidung liest `device.voice_lock.locked()`, aber die
gestartete Aufgabe holt sich die Sperre erst mehrere Awaits später:

```python
if device.voice_lock.locked():   # em_controller.py:1433
    ...cancel...
else:
    _btn_task = asyncio.create_task(_button_voice_turn())   # holt die Sperre später
```

Zwei Tastendrücke in diesem Fenster sehen beide die Sperre frei und stellen
beide ein Gespräch ein. Das `led_anim` des zweiten Gesprächs verzögert sich
dann um die Dauer des ersten — leicht über jede vernünftige Frist hinaus.
Unter der Abschlusssemantik ginge das Gerät auf Orange, bräche ab, und der
Controller würde das eingestellte Gespräch trotzdem fahren.

**Diese Behebung gehört also in dieselbe Änderung:** Der Startpfad muss das
Gespräch synchron im Handler beanspruchen (ein `turn_pending`-Flag, gesetzt
vor dem Starten, oder die Sperre im Handler holen), damit die
Abbruch/Start-Entscheidung atomar ist. Im Feldtest vom 2026-07-25 war es nicht
beobachtbar — Drücke im Abstand von ~700 ms kollidierten nie —, aber ein
schnelles Doppeltippen würde es.

---

## 6. Entscheidungen

### Q1 — verweigerter Druck bei Mute: **still bleiben** (entschieden)

Der rote Ring plus die rote LED der Mute-Taste sind ausreichendes Signal
dafür, dass das Gerät taub ist. So verhält sich Alexa, und es gibt keinen
Grund, ein Verhalten neu zu erfinden, das ausgedehnte Fokusgruppenarbeit
bereits geklärt hat. Kein bestätigendes Blinken, kein Verweigerungssignal.
Zeile A7 bleibt, wie sie ist.

**Ergänzt am 2026-08-08.** Die *Stille*-Entscheidung oben bleibt unverändert,
aber das, was verweigert wird, wurde enger. Das Gerät verwarf früher jeden
Druck auf die Punkt-Taste bei Mute, was richtig war, solange die Taste nur
„starte ein Sprachgespräch" bedeutete — und falsch wurde, sobald ein Halten
zusätzlich ein HA-Ereignis feuerte: Ein Halten, das mit etwas Sprachfremdem
belegt war, funktionierte nicht mehr, sobald das Mikrofon aus war, und nichts
am Ring verband die beiden. Drücke tragen jetzt den Mute-Zustand, und der
Controller verweigert nur das **Gespräch** (Zeile A7); ein Halten wird
weitergereicht (Zeile A8). Mute ist auf dem Gerät weiterhin hoheitlich, denn
die Hoheit war nie der Tastenfilter — sie ist das ADC-Mute plus die
Zurückweisung jedes `mic_start` bei Mute.

### Q2 — wie das Gerät weiß, dass es mitten im Gespräch ist: **semantisches Feld auf `led_anim`** (empfohlen)

Das zählt nur für Zeile A6 (den Ring bei einem Abbruchdruck sofort löschen).
Ohne das müsste das Gerät bei *jedem* Punkt-Druck Zuhören zeichnen, was
jedes Mal, wenn der Druck ein Abbruch war, für eine Umlaufzeit den falschen
Zustand aufblitzen lässt.

**Möglichkeit A — aus der laufenden Animation ableiten.** Der Animator
speichert nur `{mu, gen}` (`animator.go:45`) — kein Muster —, dafür müsste das
aktuelle Muster neben der Generation mitgeführt werden. Etwa drei Zeilen.

*Die Korrektheit ist heute exakt:* Weder `em_player` noch der Durchsagepfad
zeichnen den Ring (Durchsagen rufen direkt `_run_post_turn_playback` auf, das
keine LED-Aufrufe hat), also gilt „der Ring zeigt einen Gesprächszustand" ⟺
„ein Sprachgespräch läuft".

*Das Risiko ist eine künftige Fußangel, kein heutiger Fehler.* Die Äquivalenz
ist ein stillschweigender Vertrag. An dem Tag, an dem die Musikwiedergabe
einen Pegel-Ring bekommt — ein sehr naheliegender Wunsch —, ändert die
Punkt-Taste während Musik still ihre Bedeutung, und nichts scheitert
hörbar. Der Ring würde einfach bei Drücken erlöschen, die in Wahrheit
Gespräche starten.

**Möglichkeit B — aus Audio-/Mikrofonzustand des Geräts ableiten.** Nicht
gangbar. Wakeword-Gespräche behalten bewusst den durchgehenden, ungegatterten
Strom und senden nie `mic_start_turn` (P0-1), also ist `micActive && lockMic`
**während der Zuhörphase der häufigsten Gesprächsart falsch**. Und
`streamActive` ist auch bei Musik und Durchsagen wahr — das umgekehrte Problem
von A. Kein Signal, allein oder kombiniert, deckt den Zustandsraum ab.

**Möglichkeit C — explizite Semantik vom Controller.** Die Autorität für „wird
mein Druck als Abbruch gelesen?" ist buchstäblich
`device.voice_lock.locked()`; jede geräteseitige Ableitung ist eine Kopie. Der
Controller sendet ohnehin bei **jedem** Gesprächszustandswechsel ein
`led_anim`, ein semantisches Feld an dieser Nachricht
(`state: listening | thinking | playing | idle`) kostet also **keinen
zusätzlichen Verkehr** und zwei Übergänge pro Gespräch.

**Empfehlung: Möglichkeit C.** Sie behält As Eigenschaft, nichts zu kosten,
und beseitigt zugleich As stillschweigenden Vertrag — das Gerät liest einen
erklärten Zustand, statt aus Pixelfarbe auf Absicht zu schließen, und eine
künftige Musik-Pegel-Funktion kann die Tastensemantik nicht still ändern. Sie
macht die vorläufige Anzeige außerdem selbstbeschreibend: Das Gerät setzt
seinen eigenen semantischen Zustand, wenn es optimistisch zeichnet, und das
nächste `led_anim` des Controllers bestätigt oder überschreibt ihn.

Die Veralterung (eine Umlaufzeit) ist für A und C gleich und nicht
reduzierbar, sie unterscheidet die beiden also nicht. Beide Driftrichtungen
korrigieren sich innerhalb einer Umlaufzeit selbst.

### Q3 — Bestätigungsfrist: **fest, 3–3,5 s** (empfohlen)

Weil die Frist das **Gespräch beendet** (Q4), kostet eine falsche Rücknahme
jetzt ein echtes Gespräch und nicht nur ein Flackern. Die Frist ist also
*nicht* kostenlos, und ihr Boden ist die schlechteste legitime Zeit bis zur
ersten eingehenden Nachricht.

Dieser schlechteste Fall ist ein voller Umlauf: Der Controller gibt seine
erste Antwort unter einer Millisekunde nach Eintreffen des Tastenereignisses
aus (gemessen — „Dot button → voice turn" und „Voice turn starting" landen in
derselben Millisekunde), die einzige Variable ist also die Leitung. Retreats
Uplink-Abschnitt misst 1046 ms p90 / 1225 ms max, ein Umlauf im schlechtesten
Fall liegt also bei ~2,5 s.

**3–3,5 s** decken das mit Reserve ab und sind immer noch rund 13× besser als
die heutige 45-s-Blindheit. Zu lang zu liegen ist weiterhin richtig: zu langes
Warten kostet eine langsamere Fehleranzeige, zu kurzes Warten tötet
Gespräche, die die Nutzerin wollte.

**Fest, nicht adaptiv.** Adaptive Fristen je Gerät fügen Zustand und eine
Stellschraube hinzu, um sich gegen einen seltenen Fehler zu wappnen. Nur
wieder aufgreifen, wenn Felddaten zeigen, dass Geräte der Retreat-Klasse
fälschlich abbrechen.

**Als Bestätigung zählt *jede* eingehende Kontrollnachricht, nicht speziell
`led_anim`.** Das ist wichtig und nicht offensichtlich: Bei einem
Tastengespräch sendet der Controller `mic_stop` → `mic_start_turn` →
`led_anim`, `mic_start_turn` ist also ein *früherer* Lebensnachweis als die
Ring-Nachricht. Die Frist gegen jeden eingehenden Verkehr scharfzustellen ist
eine strikt schwächere Bedingung, wird früher erfüllt und bricht daher
seltener fälschlich ab. `led_anim` bleibt die alleinige Autorität für den
Ring-*Zustand*; Lebendigkeit und Ring-Zustandshoheit sind getrennte Belange.

*RTT-Messungen* bleiben bestätigend statt blockierend — im Feld prüfen, dass
keine legitime erste Antwort die Frist überschreitet.

### Q4 — Rücknahme der vorläufigen Anzeige: **harter Schnitt auf Orange, und das Gespräch beenden** (entschieden)

Ein harter Schnitt lenkt Aufmerksamkeit auf einen Fehlerzustand; ein
Überblenden weichzeichnet etwas, das bemerkt werden soll.

**Die Frist beendet die Interaktion — sie ist nicht bloß ein Neuzeichnen.**
Sobald das Gerät auf Orange gegangen ist und der Nutzerin gesagt hat „der
Controller antwortet nicht", verwirrt eine späte Ankunft, die das Gespräch
wiederbelebt, sie nur. Das Gespräch endet bei Fristablauf, und alles, was
danach für diese Interaktion eintrifft, wird abgewiesen.

Siehe §5b für das, was der Abschluss verlangt. Beachte die Rückwirkung auf Q3:
Weil die Frist jetzt ein echtes Gespräch beendet statt nur eine Farbe zu
ändern, ist sie nicht mehr kostenlos, und ihr Boden wird vom schlechtesten
legitimen Umlauf gesetzt.

---

## 7. Invarianten — nicht brechen

- **Mute ist gerätehoheitlich.** Korrekt ohne Controller, mit hängendem
  Controller oder mit lügendem Controller. Lokal gespeichert; übersteht das
  Umschalten des OTA-Slots.
- **Der Lautstärkebogen besitzt den Ring gegenüber *Animationen* für seine
  2 Sekunden.** Gesprächsanimationen zeichnen alle ~80 ms neu und würden ihn
  sonst binnen eines Bildes zertrampeln. Er steht **nicht** über einem
  bewussten Druck auf die Aktionstaste, der den Halt abbricht — der Bogen
  schützt vor Neuzeichnungs-Getrampel, nicht vor der Nutzerin (2026-07-25:
  Die Taste nach einer Lautstärkeänderung zu drücken gab bis zum Ablauf des
  Fensters kein Zeichen, dass das Gerät zuhört).
- **Der Ablauf des Bogens kennt Mute.** Er muss bei Mute Rot wiederherstellen,
  nie an den Controller-Zustand zurückgeben.
- **Der `ttlSec`-Totmannschalter bleibt.** Er ist der einzige Schutz gegen
  einen Controller, der mitten in einer Animation stirbt.
- **Das Ersetzen von Animationen bleibt atomar** (Generationszähler). Eine
  veraltete Goroutine darf nie über ihre Nachfolgerin malen.
- **`baseLEDs` zeichnet während der Unterdrückung weiter auf.** Das macht die
  Rückgabe nahtlos.
- **Die Richtungsüberlagerung braucht `listeningLEDs`** und hellt die
  Grundfarbe auf — sie darf nie ein festes Grün zeichnen, das auf jeder
  nicht standardmäßigen Szene wie ein Fehler wirkt.

---

## 8. Risikohinweis

Das LED-Prioritätssystem ist das am häufigsten reparierte Teilsystem dieses
Codes — die beiden Anzeigeunterdrückungen in §2 wurden jeweils auf die harte
Tour gewonnen, und ihre Kommentare halten fest, warum. Eine Ebene für
vorläufige Anzeigen hinzuzufügen heißt, eine **vierte** Schlichtungsregel
hinzuzufügen. Die Vorschläge hier sind bewusst gestaffelt, damit die
risikoärmste, vollständig belegte Änderung (L7) unabhängig von der Ebene für
vorläufige Anzeigen (A2/A3/A6) ausgeliefert werden kann, die die neue
Schlichtungsregel und das semantische `led_anim`-Feld mitbringt.
