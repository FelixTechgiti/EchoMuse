# FAQ

Kurze Antworten und Umgehungen für das, was am häufigsten aufkommt. Wo es
eine ausführlichere Fassung gibt, ist sie verlinkt.

Steht dein Problem nicht hier, führt der [UAT-Leitfaden](uat.md) die bekannten
offenen Fehler auf, die vor einem Issue einen Blick wert sind, und
[support-bundle.md](support-bundle.md) erklärt, was du anhängen solltest.

---

## Rooten und Entsperren

### Das Entsperren läuft auf meinem Mac nicht.
**Es braucht Linux.** Das Entsperren ist
[R0rt1z2s Arbeit auf XDA](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/),
nicht unsere, und es funktioniert nicht unter macOS. Ein Live-USB-Stick
genügt — das Entsperren ist der einzige Schritt, der Linux braucht. Alles
danach, auch der Einrichtungsassistent, läuft in einem Chromium-Browser auf
jedem Betriebssystem.

### `brick.sh` verweigert mit „restricted on locked hw".
**Dein Dot ist nicht auf dem neuesten FireOS.** Der Exploit funktioniert nur
mit aktueller Firmware.

1. Verbinde den Dot mit einem Amazon-Konto (irgendeinem — leg dir ein
   Wegwerfkonto an), damit er ins WLAN kommt.
2. Schalte ihn stumm und lass ihn 20–30 Minuten eingesteckt. Das Stummschalten
   verhindert, dass Wakewords das Update unterbrechen. Womöglich brauchst du
   zwei solcher unbeaufsichtigten Runden.
3. Sag dann „Alexa, check for software updates", um ihn auf den aktuellen
   Stand zu bringen.
4. `brick.sh` erneut versuchen.

Zwei Dinge, über die Beitragende dabei gestolpert sind: Wenn die Alexa-App
einen sehr alten Dot nicht koppeln will, wähle in der App **Echo Tap**, um den
alten Hotspot-Kopplungsablauf zu bekommen; und schalte jeden Dateimanager
**aus**, der USB-Geräte automatisch einhängt — er schnappt sich den Handshake
und lässt spätere adb-Schritte mit wenig hilfreichen Fehlern scheitern.

### Das Sideloading von FireOS 5.5.5.4 scheitert mit einem roten Aufblitzen des Rings.
Nochmal sideloaden. Beim zweiten Versuch klappt es in der Regel.

### Tiefergehende Fragen zum Entsperren selbst.
Die gehören in den [XDA-Thread](https://xdaforums.com/t/unlock-root-twrp-unbrick-amazon-echo-dot-2nd-gen-2016-biscuit.4761416/).
Wir verlinken ihn bewusst, statt ihn zu kopieren — eine Kopie veraltet, ohne
dass es jemand merkt.

### Habe ich ihn gebrickt?
**Wahrscheinlich nicht dauerhaft.** Ein im Preloader hängender Dot ist meist
zu retten — siehe die Recovery-Hinweise im XDA-Thread. Flashe nichts weiter
darauf, bevor du nachgefragt hast.

---

## Der Einrichtungsassistent

### Der Assistent sieht mein Gerät nicht oder sagt, ein anderes Programm benutze es.
**Führe zuerst `adb kill-server` aus.** Ein laufender adb-Server auf deinem
Rechner hält das Gerät fest, und der Browser kommt nicht heran. Das ist mit
Abstand der häufigste Fehler beim Einrichten.

### Die Geräteauswahl zeigt nichts an.
Nimm **Chrome oder Edge**. Der Assistent spricht per WebUSB mit dem Gerät, und
andere Browser verhalten sich unterschiedlich. Brave ist insbesondere
unbestätigt.

### Der Assistent sagt, er brauche einen sicheren Kontext.
Wenn du Home Assistant über einfaches `http://` erreichst, blockiert der
Browser WebUSB. Trage deine HA-URL unter
`chrome://flags/#unsafely-treat-insecure-origin-as-secure` ein — etwa
`http://homeassistant.local:8123` — und starte den Browser neu. Verfolgt als
[#170](https://github.com/wilbowes/EchoMuse/issues/170).

### Die USB-Verbindung bricht alle paar Sekunden ab und meldet sich neu an.
`persist.sys.usb.config` steht auf `mtp,adb`, und das zusammengesetzte
USB-Gadget ist es, was den Bus abwirft — etwa alle sechs Sekunden, jedes Mal
mit `device firmware changed` im `dmesg`. Es auf `adb` allein zu zwingen
behebt das. Nichts an Revoice braucht MTP.

**`setprop` von einem gestarteten Gerät aus funktioniert nicht.** Androids
Property-Dienst verweigert genau diese Eigenschaft, egal was `ro.secure` und
`ro.debuggable` sagen (`init: sys_prop: permission denied uid:2000
name:sys.usb.config`) — sie muss also geändert werden, bevor diese Schicht
greift. Entpacke aus TWRP heraus das Boot-Image, ändere `default.prop` zu
`persist.sys.usb.config=adb`, ergänze `persist.sys.usb.state=adb`, packe es
wieder ein und schreibe es zurück — die vollständigen Befehle stehen in
[#79](https://github.com/wilbowes/EchoMuse/issues/79).

Ursache gefunden von @kylegordon, unabhängig bestätigt von @midiland, bei dem
`setprop` allerdings funktioniert hat — einen ersten Versuch wert, er kostet
nichts.

*(Dieser Eintrag riet früher dazu, `mtp,adb` zu setzen, was die Ursache ist
und nicht die Lösung. Korrigiert am 2026-09-05.)*

### Ein Schritt des Assistenten ist gescheitert und ich weiß nicht warum.
Jeder gescheiterte Schritt bietet eine Diagnose an. Hol sie dir vor dem
nächsten Versuch — der Zustand, in dem das Gerät ist, *ist* die Diagnose, und
ein erneuter Versuch zerstört ihn.

---

## Controller und Dashboard

### Add-on oder Docker-Container — was soll ich nehmen?
Was dir besser passt. **Beide sind gleichwertig**, und keines wird zugunsten
des anderen vernachlässigt. Das Add-on ist einfacher, wenn du ohnehin Home
Assistant OS betreibst; der Container ist die Antwort, wenn der Controller
woanders leben soll.

### Ich habe im Add-on nur Lesezugriff und komme nicht mehr an Admin.
Behoben — **aktualisiere den Controller**. Die Regel zählt jetzt
Home-Assistant-Administratoren, die sich über Ingress anmelden können, also
bekommt der erste HA-Benutzer Adminrechte. Beim ersten Laden der Seite nach
dem Update liest das Dashboard deine Rolle neu vom Server und korrigiert sich
selbst; mehr solltest du nicht tun müssen.

Wenn du auf einem älteren Stand feststeckst, in der Browser-Konsole:

```js
['em_token','em_role','em_auth_via'].forEach(k => localStorage.removeItem(k));
location.reload();
```

Hintergrund: [#235](https://github.com/wilbowes/EchoMuse/issues/235).

### Wie aktualisiere ich den Controller?
```
docker compose pull && docker compose up -d
```
oder aktualisiere das Add-on aus Home Assistant heraus. **Der Update-Hinweis
im Dashboard ist ein Hinweis und bleibt es** — der Controller ist dein
Container, und ein Prozess kann sich nicht mitten in einer Anfrage selbst
neu starten und dir danach erzählen, wie es lief.

### Der Update-Hinweis zeigt keine Versionshinweise.
Die Hinweise kommen aus der Tag-Annotation. Sind sie leer, ist das unser
Fehler — bitte melde es mit der Version, auf der du bist.

### Das Dashboard zeigt mein Gerät als Online, aber nichts geht.
Sieh unter **Gerät → Status → Voice assistant** nach. `Waiting for HA` heißt,
dass Home Assistant sich nie mit diesem Satelliten verbunden hat — meist ein
veralteter HA-Konfigurationseintrag, nachdem ein Gerät neu hinzugefügt wurde.
Lösche den ESPHome-Eintrag in HA und lass ihn neu finden.

### Wo liegen meine Daten?
In SQLite neben dem Controller — beim Add-on unter `/data`, beim Container im
eingehängten Volume. Sichere dieses Verzeichnis; darin stecken Geräte,
Benutzer, Konfiguration, Aktivitätsverlauf und die TLS-Zertifizierungsstelle.

### Kann ich den Controller auf eine andere IP umziehen?
Ja. Geräte finden ihn per mDNS, und das TLS-Zertifikat identifiziert ihn
bewusst über den Namen und nicht über die Adresse — er kann sich also frei
bewegen. **Feste Endpunkte für geroutete oder getunnelte Netze sind in
Arbeit** — [#106](https://github.com/wilbowes/EchoMuse/issues/106) /
[#166](https://github.com/wilbowes/EchoMuse/issues/166).

---

## Home Assistant

### Der Dialog „Sprachsatellit einrichten" läuft in einen Timeout und braucht „Wiederholen".
**Bekannt und rein kosmetisch** — auf Wiederholen drücken, dann geht es durch.
Der Klang spielt, der Satellit wird eingerichtet, und HAs Dialog gibt auf,
bevor er die Antwort hört.
[#219](https://github.com/wilbowes/EchoMuse/issues/219).

### Home Assistant weist mein Gerät ab: „Unexpected device found at …".
Ein ESPHome-Konfigurationseintrag in HA ist auf Host **und Port** geschlüsselt
und zeigt auf einen Port, der inzwischen zu einem anderen Gerät gehört —
meist nachdem Geräte gelöscht und neu hinzugefügt oder Release-Kanäle
gewechselt wurden. **Lösche den veralteten ESPHome-Eintrag in HA und lass ihn
neu finden.** Beachte: Das Gerät bekommt dabei neue `entity_id`s, Automationen
mit den alten müssen also angepasst werden.

### Jede Durchsage wirft einen Fehler ins HA-Log.
Behoben — aktualisiere den Controller. Wir haben einen Media-Player-Zustand
gesendet, für den HA keine Zuordnung hat.

### Kann ich zwei verschiedene Wakewords für zwei Assistenten nutzen?
**Noch nicht, aber die Hälfte von Home Assistant funktioniert bereits.** HA
wählt eine Pipeline anhand des Wakeword-Ausdrucks, den der Satellit meldet,
und wir melden ihn inzwischen. Was fehlt, ist mehr als ein Wakeword-Modell
gleichzeitig laufen zu lassen. Verfolgt als
[#112](https://github.com/wilbowes/EchoMuse/issues/112).

### Music Assistant verhält sich seltsam.
Siehe [#210](https://github.com/wilbowes/EchoMuse/issues/210) — und bitte
ergänze deinen Aufbau, dieses Issue braucht mehr Meldungen, als es hat.

### Das Gerät wird nicht als Ziel für „ein Gespräch beginnen" angeboten.
Noch nicht umgesetzt. [#335](https://github.com/wilbowes/EchoMuse/issues/335).

---

## Sprache, Wakewords und Audio

### Er wacht nicht zuverlässig auf.
Der Reihe nach:

1. **Config → Wake word → Sensitivity**, Richtung „Eager" schieben.
2. **Config → Microphones → mic gain**, wenn der Raum groß ist oder du weit
   vom Gerät entfernt stehst.
3. Ein anderes Modell probieren. Die mitgelieferten
   openWakeWord-Modelle passen sehr unterschiedlich gut zu einer bestimmten
   Stimme.

Wenn er in einem ruhigen Raum auf normale Sprechentfernung immer noch nicht
anspringt, ist das eine Meldung wert — mit Modellnamen und Entfernung.

### Er wacht auf, obwohl niemand etwas gesagt hat.
Sensitivity Richtung „Precise" schieben. Wenn er speziell bei laufendem
Fernseher auslöst, ist [#294](https://github.com/wilbowes/EchoMuse/issues/294)
die offene Arbeit dazu — schreib dazu, was lief.

### Kann ich mein eigenes Wakeword nutzen?
Ja — [oww_forge](../oww_forge/README.md) trainiert eines, und du installierst
es im Dashboard unter **Config → Wake word → + Custom model**. Nimm lieber das
veröffentlichte Docker-Image, als es selbst zu bauen; die Upstream-Pins, die
es funktionieren lassen, bleiben nur in einem veröffentlichten Artefakt
erhalten.

### Wie höre ich, was das Gerät tatsächlich an die Spracherkennung geschickt hat?
**Config → Microphones → Advanced → save utterances.** Der Ton erscheint
danach pro Gespräch im Reiter **Activity** des Geräts. Das ist exakt der Ton,
den Whisper bekommen hat — also genau das Richtige zum Anhören, wenn die
Transkription falsch ist.

### Der Ton verzerrt, wenn es laut wird.
Sieh unter **Config → Playback** nach: Limiter und Bass-Schutz sollten an
sein. Wenn es weiter verzerrt, melde den Lautstärkeprozentsatz — die Zahl ist
die Diagnose.

### Es klingt schlechter als das originale Alexa.
Teils richtig, teils behoben. Ein Übersteuern des DAC oberhalb von Unity Gain
wurde gefunden und korrigiert, das war der größte Anteil. Was bleibt: Das
Original wendet eine erhebliche Treiberkorrektur an, die wir noch nicht
haben — [#247](https://github.com/wilbowes/EchoMuse/issues/247).

### Die Musik pausiert, statt unter einer Sprachantwort leiser zu werden.
Ducking braucht Firmware, die Audio-Mixing ankündigt. Aktualisiere die
Geräte-Firmware; wenn sie weiter pausiert, sieh unter Gerät → Status nach und
melde es.

### Lange Antworten brechen mittendrin ab.
Bekannt: [#324](https://github.com/wilbowes/EchoMuse/issues/324). Ein
Support-Bundle mit dem Zeitpunkt hilft hier wirklich.

### Kann ich ihn unterbrechen, während er spricht?
Ja — sag das Wakeword noch einmal. Aktiviere es unter **Config → Wake word →
Barge-in**, falls noch nicht geschehen.

### Kann er Timer?
Ja, seit der aktuellen Version — frag danach, wie du es erwarten würdest.
**Einen klingelnden Timer per Sprache zu stoppen, ist bekanntermaßen
unzuverlässig**, und diese Hälfte ist noch in Arbeit. Melde, was du gesagt
hast und was passiert ist; die Formulierungen, die Leute tatsächlich
benutzen, sind der nützliche Teil.

---

## Geräte und Flotte

### Ein Firmware-Update ist gescheitert.
Behoben in `controller-v2.18.0` — **aktualisiere zuerst den Controller**, dann
noch einmal versuchen. Zwei Fehler steckten dahinter: Die Übertragung löschte
den Rückfall-Slot, bevor überhaupt etwas gesendet wurde, und eine einzige
Fehlermeldung deckte fünf verschiedene Ausgänge ab.

Auf einem älteren Controller half diese Umgehung: ein Gerät nach dem anderen
aktualisieren, mit dem Gerät nah am Access Point.

**Ein Gerät niemals mitten im Update vom Strom trennen.**

### Ein Gerät reagiert nach einem Update nicht mehr.
Gerät → **Updates** bietet einen Rückfall auf den vorherigen Slot an. Hilft
das nicht, ist ein Support-Bundle samt Zeitpunkt die richtige Meldung.

### Ich habe ein Gerät gelöscht und es lief weiter.
Behoben — aktualisiere den Controller. Ein gelöschtes Gerät wirft jetzt seine
Verbindung ab und kommt als ausstehend zurück.

### Ich habe ein Gerät neu hinzugefügt und sein Sprach-Port fehlt.
Gleiche Behebung, gleiche Antwort: Controller aktualisieren.

### Mein Gerät hat seine Home-Assistant-Entitäts-IDs geändert.
Das passiert immer, wenn ein Gerät gelöscht und neu hinzugefügt wird — HA
schlüsselt Entitäten auf die Identität, und ein neu hinzugefügtes Gerät ist
ein neues. Die alten IDs lassen sich nicht mitnehmen. Benenne die Entitäten in
HA um, wenn du die alten Namen zurück brauchst.

### Einstellungen für ein Gerät oder für alle?
Die Konfiguration ist je Abschnitt zugeordnet. Ein Gerät folgt der Flotte in
jedem Abschnitt, den es nicht überschreibt — änderst du also die
Ring-Einstellungen eines Geräts, folgen sein Mikrofon und sein Wakeword
weiterhin den Flottenänderungen. **Gerät → Status → Config** sagt dir, was
gerade gilt.

### Zwei Geräte antworten beide, wenn ich das Wakeword sage.
Sollten sie nicht — die Arbitrierung wählt eines aus. **Config → Wake word →
Arbitration window** verbreitert das Fenster, in dem Geräte verglichen werden.
Wenn danach immer noch beide antworten, melde es.

### Kann ich einen defekten Echo gegen einen neuen tauschen und seine Historie behalten?
Noch nicht — [#133](https://github.com/wilbowes/EchoMuse/issues/133).

### Das Gerät verbindet sich nach einem Neustart nicht wieder mit dem WLAN.
Wenn dein Netzwerk keinen Internetzugang hat, ist das ein bekanntes
Android-Verhalten, das das automatische Verbinden blockiert —
[#317](https://github.com/wilbowes/EchoMuse/issues/317).

### Mein Gerät hat keinen Helligkeitssensor.
Manche Dots haben einen Sensor aus zweiter Quelle verbaut, der einen anderen
Treiber bindet. Bekannt, und die Behebung ist auf unserer Hardware nicht
überprüfbar — [#90](https://github.com/wilbowes/EchoMuse/issues/90) — eine
Meldung von betroffener Hardware ist also wirklich nützlich.

---

## Privatsphäre

### Telefoniert Revoice nach Hause?
**Nein.** Keine Telemetrie, keine Analytics, keine Absturzberichte, kein
Installationszähler. Die Folge steht deutlich in
[configuration.md](configuration.md#was-dein-netzwerk-verlässt): Niemand,
auch nicht die Entwickelnden, weiß, wie viele Leute es benutzen.

### Was verlässt denn mein Netzwerk?
Eine Sache: Der Controller fragt einmal pro Stunde bei `api.github.com` nach,
welches die neueste Version ist, damit das Dashboard dir sagen kann, dass es
ein Update gibt. Firmware wird von GitHub geladen, wenn du dich für ein Update
entscheidest. Setze `update_check_interval` auf `0`, um auch das abzustellen.

### Wird mein Sprachton irgendwohin geschickt?
Er geht vom Gerät zu deinem Controller zu deinem Home Assistant, über dein
LAN. Wohin er danach geht, entscheidet die Spracherkennung, die du in HA
eingerichtet hast — das ist deine Wahl, nicht unsere.

### Kann ich ein Support-Bundle bedenkenlos an ein öffentliches Issue hängen?
Ja, das ist so entworfen. Es ist eine Positivliste: keine Transkripte, kein
gespeicherter Ton, keine WLAN-Namen, keine IP-Adressen, keine von dir
vergebenen Gerätebezeichnungen, keine Zugangsdaten, keine Dateipfade. Es ist
schlichtes JSON — [mach es lieber selbst auf](support-bundle.md), statt uns
zu glauben.

### Ist die Verbindung zwischen Gerät und Controller verschlüsselt?
Sie kann es sein und sollte es sein. Gerät → **Status** → auf **Secure link**
drücken, wenn in der Zeile „Link" `plain ws` steht. **Die ESPHome-Verbindung
zu Home Assistant ist weiterhin unverschlüsselt**, einschließlich
Mikrofonton — [#341](https://github.com/wilbowes/EchoMuse/issues/341).

---

## Hardware und Umfang

### Funktioniert das mit einem Echo Dot Gen 3 / Show / Studio?
Unterstützt wird heute nur der Echo Dot Gen 2 („biscuit"). **Unterstützung für
den Echo Show 8 liegt in Review**
([#358](https://github.com/wilbowes/EchoMuse/pull/358)), und am Echo Show 5
wird gearbeitet ([#36](https://github.com/wilbowes/EchoMuse/issues/36)).
Andere Boards sind willkommen — die Android-spezifische Fläche umfasst rund
zwanzig Aufrufstellen, ein neues Board ist also überwiegend ein Binding für
Mikrofon, Lautsprecher, LEDs und Tasten.

### Hängt es von Amazons Software ab?
Kaum, und das bleibt bewusst so. Es ist ein Linux-Dienst auf ALSA, i2c, evdev
und sysfs, der zufällig auf Android läuft, weil das nun mal auf der Kiste war.
Eine Änderung, die einen Amazon-Blob zurück in den Audiopfad setzt, geht in
die falsche Richtung — auch wenn sie besser klingt.

### Gibt es ein Video davon?
Ja — siehe [#193](https://github.com/wilbowes/EchoMuse/issues/193), dort
sammeln sich Aufnahmen aus der Community.

### Ich will helfen. Wo fange ich an?
Issues mit den Labels **`good first issue`** und **`ready`** haben einen
abgeschlossenen Entwurf. Alles mit `needs-design` oder `needs-decision` ist
noch nicht baureif, egal wie klein es aussieht — frag vorher nach, damit du
nichts schreibst, was wir dann ablehnen müssen.
