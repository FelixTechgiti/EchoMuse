# Konfigurationsleitfaden

Jede Einstellung, was sie tatsächlich tut und wann du sie anfassen würdest —
in verständlichen Worten.

## Wo Einstellungen leben

- **Flottenkonfiguration** (Zahnradsymbol → Fleet Config): die
  Voreinstellungen, die jedes Gerät benutzt.
- **Konfiguration je Gerät** (Geräteseite → Reiter „Config"): Jeder Abschnitt
  trägt in seiner Kopfzeile einen eigenen Schalter **Fleet / Device**.

Die Zuordnung gilt **je Abschnitt**, nicht alles oder nichts. Lass einen
Abschnitt auf *Fleet*, und er folgt weiterhin dem flottenweiten Wert,
künftige Änderungen eingeschlossen. Stell ihn auf *Device*, und nur dieser
Abschnitt gehört diesem Gerät — alles andere folgt weiter der Flotte.

So kann ein Dot in einem kleinen Raum seine eigene **Ring**-Szene und seine
eigene **Microphones**-Verstärkung haben und trotzdem jede Flottenänderung an
Wakeword, EQ und Bluetooth mitnehmen. Vorher hat eine Überschreibung *alle*
Einstellungen abgespalten und sie dauerhaft gegen Flottenänderungen
eingefroren.

Ein Abschnitt, der *Fleet* zeigt, wird schreibgeschützt angezeigt statt
versteckt, du siehst also immer, was er erbt. Das Banner oben im Reiter fasst
zusammen — `Fleet`, oder `Local override (2 of 6)` mit den benannten
Abschnitten — und **Revert all to fleet** setzt alles zurück.

Einen Abschnitt zurück auf *Fleet* zu stellen **verwirft** die Werte, die er
hielt. Es gibt keine versteckte Schattenkopie, die wieder auftaucht, wenn du
ihn Monate später erneut auf *Device* stellst; er startet vom Flottenwert.

Änderungen wirken **sofort** — keine Neustarts, keine Neubauten. Der Reiter
„Config" öffnet oben mit den **Netzwerkeinstellungen (WLAN)** des Geräts —
immer je Gerät, nie von der Flotte geerbt —, gefolgt von den flottenfähigen
Abschnitten, geordnet danach, wie oft du sie realistisch anfasst:
**Playback**, **Wake word**, **Microphones**, **Ring**, **Advanced**,
**Bluetooth**.

Die **CPU**-Anzeige zeigt neben dem Prozentwert die Kernanzahl — „27 % · 2/4
cores". Der Dot hat vier Kerne und parkt die, die er nicht braucht; der
Prozentwert ist ein Anteil der Kerne, die *wach* sind. Dieselbe Arbeitsmenge
liest sich also als größere Zahl, wenn weniger wach sind. Ohne die Kernanzahl
daneben kann der Wert sich halbieren, ohne dass sich etwas geändert hat.

Zwei weitere Reiter am Gerät sind wissenswert: **Status** (IP, Firmware,
WLAN, ESPHome-Port, aktuelle Lautstärke, ob die Konfiguration von der Flotte
kommt oder überschrieben ist, Ressourcenanzeigen einschließlich **Latency**
— die Umlaufzeit zum Gerät, bernsteinfarben jenseits von 200 ms, rot jenseits
von 1 s; das einzige Signal zur Verbindungsgesundheit, das der WLAN-Treiber
des Echos überhaupt liefert, denn er meldet weder Wiederholungen noch
Rauschwerte — und **Temp** (der CPU-Sensor des Dots und der heißeste seiner
elf Sensoren, wenn der spürbar wärmer ist; im Leerlauf liegt er bei etwa
33 °C, alles Bernsteinfarbene ist also wirklich ungewöhnlich — und wenn der
Wärmeregler des Chips je anfängt, CPU-Leistung zu deckeln, sagt er es hier),
dazu das Diagnosefeld des Bluetooth-Proxys, wenn dieser aktiviert ist — die
Statuszeile liest `Online` oder `Offline` mit der Angabe, wann das Gerät
zuletzt gehört wurde) und **Activity** (Verlauf der Sprachgespräche — was
gehört wurde, wie es transkribiert wurde, Wakeword-Werte, Aussetzer bei der
Wiedergabe, Beinahe-Treffer, und, wenn **Save utterances** an ist, der
aufgezeichnete Ton jedes Gesprächs, abspielbar und herunterladbar). Der
Aktivitätsverlauf liegt in der Datenbank des Controllers und übersteht
Neustarts von Controller und Gerät; stündliche Hardware-Trends (CPU,
Speicher, WLAN-Signal) werden 180 Tage aufbewahrt und sind über die API
verfügbar (`/api/devices/{id}/activity?days=N`).

---

## 01 — Playback

Wie Antworten klingen.

**Zwei Dinge, die du wissen solltest, bevor du hier etwas abstimmst.**

**Änderungen sind erst nach etwa vier Sekunden zu hören.** Ton wird mehrere
Sekunden vor dem Hören an das Gerät geschickt; was gerade spielt, wurde also
verarbeitet, bevor du den Regler bewegt hast. Warte fünf Sekunden, bevor du
urteilst, und schalte eine Einstellung nicht schnell hin und her — du hörst
das alte Audio und schließt daraus, es sei nichts passiert.

**Ändere die Einstellungen an dem Gerät, dem du zuhörst.** Wenn ein Gerät
eigene Playback-Einstellungen hat (der Fleet/Device-Schalter an diesem
Abschnitt), wirkt das Bearbeiten der Flottenvoreinstellungen nicht auf es. Das
Speichern gelingt in beiden Fällen, das Symptom ist also ein Bedienelement,
das scheinbar überhaupt nichts tut.

### Equalizer (8 Regler + Voreinstellungen)
Formt den Klang der Sprachantworten, wie der EQ an einer Stereoanlage. Der
kleine Lautsprecher des Dots ist von Haus aus dröhnend und dumpf.

- **Flat** — keine Formung.
- **Clarity** — hebt die oberen Mitten an, wo die Sprachverständlichkeit
  wohnt. Gute Voreinstellung für Sprache.
- **Warmth** — sanfte Anhebung der unteren Mitten, weichere Höhen. Netter für
  musikalischeres Material.
- Zieh einen beliebigen Regler für eine eigene Kurve.

### Speech boost
Eine zusätzliche Präsenzanhebung für gesprochene Antworten. Probiere es, wenn
Antworten aus der Entfernung dumpf klingen.

### Speaker protection
Hält Bass, den der Treiber nicht liefern kann, davon ab, alles darüber zu
vermatschen. Lass es an.

Es klingt verkehrt herum und ist für einen so kleinen Lautsprecher die
richtige Antwort. Frequenzen unter etwa 115 Hz bewegen die Membran weiterhin,
obwohl du sie nicht hörst, und diese Bewegung verschmiert die Mitten — genau
das, was Leute üblicherweise als dünn, kastig oder „Blechdose" beschreiben.
Sie zu entfernen macht die Mitten klarer und bei lautem Material sogar etwas
*lauter*, weil der Limiter nicht mehr alles herunterhalten muss, um Bassspitzen
unterzubringen, die du ohnehin nie gehört hättest.

Leise Passagen behalten ihren Tiefton. Betroffen ist nur lautes Material,
weshalb es ein Schutz und kein Filter ist.

Dieser eine Schalter deckt zwei Stufen ab: den beschriebenen Bass-Schutz und
einen Limiter, der verhindert, dass der Equalizer verzerrt, was er anhebt.
Ein EQ-Band aufzudrehen kann den Ton über das treiben, was die Hardware
darstellen kann, und ohne Limiter wird das abgeschnitten — gemessen bei knapp
5 % der Samples bei einer gewöhnlichen Antwort mit maßvoller Bassanhebung,
hörbar als Härte oder Knistern, und es traf immer nur Leute, die den EQ
anfassten, um ihren Klang zu verbessern.

**Es waren einmal fünf Bedienelemente, jetzt ist es eines — absichtlich.**
Keines der fünf ließ sich nach Gehör beurteilen. Die zwei Stufen heben ihre
jeweils offensichtlichste Wirkung gegenseitig auf: Bei flachem EQ ist das
Einschalten des Schutzes eine Pegeländerung von 7,7 dB, mit allen Bändern auf
+12 dB sind es 0,2 dB, weil der Limiter schlicht zurückgibt, was der Schutz
nimmt. Der Tiefenregler bewegte den Gesamtpegel über seinen ganzen Bereich um
0,14 dB. Bedienelemente, deren Wirkung je nach Stellung der anderen zwischen
„groß" und „nichts" schwankt, sind keine Abstimmungsfläche; sie sind eine Art,
zu dem Schluss zu kommen, die Funktion sei kaputt — und genau das passierte
immer wieder.

Die Einzelwerte existieren weiterhin und wirken weiterhin — wenn du Decke,
Release oder Tiefe abgestimmt hattest, sind deine Einstellungen unverändert.
Sie sind über die API erreichbar, für alle, die sie wollen, nur nicht im
Dashboard.

Die Übergangsfrequenz und die Form der Kurve stammen aus Messungen an Amazons
eigener Firmware auf demselben Lautsprecher, sie sind also nicht geraten.
Unsere voreingestellte Tiefe ist sanfter als die von Amazon, weil deren vor
einer Equalizer-Kurve sitzt, die wir noch nicht vermessen haben — siehe Issue
#247.

### Duck depth
Wie weit Musik absinkt, während der Assistent darüber spricht. Die Musik
**läuft** durch ein Sprachgespräch **weiter** — sie wird nicht pausiert —, die
Antwort kommt also über ein leises Bett, und das Bett kommt am Ende wieder
hoch.

Standard **−18 dB**. Weniger negativ (−6, −10) lässt die Musik präsenter;
stärker negativ (−25, −30) macht sie für die Dauer der Antwort nahezu still.
Es lohnt sich, das im Raum nach Gehör einzustellen: Die richtige Tiefe hängt
davon ab, was du hörst und wie laut, und es gibt keinen überall richtigen
Wert. Die Antwort selbst wird nie leiser gedreht, nur die Musik darunter —
wenn eine Antwort über Musik schwer zu hören ist, ist das hier die
Einstellung, nicht die Lautstärke.

Braucht Firmware **v2.10.0 oder neuer**, die die beiden Audioströme auf dem
Gerät selbst mischt. Das ist keine willkürliche Anforderung: Der Controller
läuft etwa vier Sekunden vor dem, was du hörst; wenn du das Wakeword sagst,
liegen diese vier Sekunden Musik schon auf dem Echo, jenseits von allem, was
der Controller noch ändern könnte. Ältere Firmware zeigt den Regler
deaktiviert und fällt darauf zurück, die Musik fürs Gespräch zu pausieren und
danach fortzusetzen.

### Audio hold-off
Wie lange der **Audio**-Sensor in Home Assistant nach dem letzten Ton noch an
bleibt.

Dieser Sensor ist an, wann immer der Echo spricht, gleich sprechen wird oder
irgendetwas abspielt — auch Musik über Spotify Connect, AirPlay oder Sendspin,
die sonst nichts hier sehen kann. Er ist für einen Verstärker gebaut, der an
der Klinke des Echos hängt: den Verstärker auf diesen Eingang schalten, wenn
der Sensor angeht, und zurück, wenn er ausgeht.

Standard **5 Sekunden**, und der Grund, warum es nicht null ist: Ton kommt in
Schüben, die nichts mit dir zu tun haben — eine Antwort endet und eine
Durchsage folgt, ein Titel geht mit einer Lücke in den nächsten über. Jede
dieser Pausen zu melden schaltet den Eingang eines Verstärkers hin und her,
und das ist schlimmer, als es gar nicht zu automatisieren. Dreh es hoch für
einen langsamen Verstärker oder eine lange Folge von Durchsagen; 0 meldet
jede Lücke.

Das **Angehen** wird nie verzögert — nur das Ausgehen —, ein Verstärker hat
also weiterhin die gesamte Denkzeit von Home Assistant, um sich vor dem
ersten Wort zu fangen.

Braucht Firmware **v2.17.0-fx.1 oder neuer**, die meldet, was der Echo von
sich aus abspielt. Ältere Firmware zeigt das deaktiviert und bekommt keinen
Audio-Sensor: Einer, der durch ein ganzes Album „aus" läse, wäre schlimmer als
keiner, denn eine darauf gebaute Automation würde den Verstärker von der Musik
wegschalten.

Setze beim Schreiben dieser Automation `not_from: [unavailable, unknown]` an
den Zustandsauslöser. Home Assistant stellt den Zustand einer Entität wieder
her, wenn eine Verbindung zurückkommt, und ohne das feuert ein Neuverbinden
den Auslöser.

### Lautstärke
Die Lautstärke **folgt dem, was du tatsächlich benutzt**, und übersteht
Neustarts: Jede Änderung — Tasten, Regler in Home Assistant, wo auch immer —
merkt sich der Controller und stellt sie beim Wiederverbinden her. Stell sie
abends leise, und ein nächtlicher Stromausfall bringt sie leise zurück.

In diesem Abschnitt gibt es keinen Lautstärkeregler. Früher gab es einen, und
er war irreführend: Das Gerät wendet den gespeicherten Pegel nur beim ersten
Config-Push nach dem Start an, den Regler zu bewegen tat also nichts, bis das
Gerät neu startete — und jede echte Lautstärkeänderung überschrieb ihn
zwischenzeitlich. Lautstärke ist gemerkter Gerätezustand und keine
Einstellung, die man einstellt, deshalb steht der aktuelle Pegel jetzt
schreibgeschützt im Reiter **Status**. Ändere ihn in Home Assistant oder an
den Tasten des Geräts.

Sie wird außerdem nie von der Flotte geerbt, was der Fleet/Device-Schalter des
Abschnitts auch sagt — sonst käme ein Gerät mit der Lautstärke eines anderen
Raums zurück.

**Das obere Ende des Bereichs hat sich in 2.20.0 geändert.** Revoice trieb
die digitale Lautstärke des Codecs früher über den Punkt hinaus, an dem sie
nur noch übersteuern kann — gemessen 65 % Verzerrung drei Tastendrücke über
der Mitte und 89 % am Maximum, ohne dass die Ausgabe noch lauter wurde. Das
Original-Alexa fasst diesen Regler nie an, weshalb Revoice aufgedreht
schlechter klang als das Original. Der Bereich endet jetzt am Unity Gain des
Codecs — die lauteste Stellung ist also leiser als früher, und alles darunter
ist sauberer.

Auch der von Home Assistant angezeigte Prozentwert hat sich verschoben: Ein
Gerät mit demselben physischen Pegel liest eine höhere Zahl als vorher, weil
die Skala keinen Abschnitt mehr enthält, der nur verzerrte. An der
tatsächlichen Lautstärke hat sich nichts geändert. Wenn du eine Automation mit
einer Lautstärkeschwelle hast, prüfe diese Schwelle.

Die physischen Tasten gehen über den hörbaren Bereich in Schritten von etwa
4 dB je Druck, statt Tastendrücke am unteren Ende einer Skala zu verbrauchen,
wo nichts hörbar ist — das Gerät still zu machen ist die Aufgabe der
Mute-Taste. Der cyanfarbene Ring spannt denselben Bereich, ein Druck bewegt
ihn also immer.

Auch Mute wird gemerkt, aber vom Gerät selbst: Ein stummgeschalteter Dot
bleibt über Neustarts, Stromausfälle und Firmware-Updates stumm — roter Ring
inklusive —, ob der Controller erreichbar ist oder nicht.

---

## 02 — Wake word

Wie das Gerät entscheidet, dass du das Zauberwort gesagt hast. Standardmäßig
passiert diese Arbeit auf dem Controller, nicht auf dem Dot — der Dot streamt
nur Ton dorthin. Der Dot kann diese Arbeit auch selbst erledigen, entweder
neben dem Controller zum Vergleich oder statt seiner; siehe **Wake word
detection** unten.

### Wake word model
Welches Wort weckt: Hey Jarvis, Alexa, Hey Mycroft oder Hey Rhasspy. Das sind
vortrainierte Erkenner — du wählst ein Wort, du trainierst nichts. Nimm eines,
das nicht mit Wörtern kollidiert, die du oft sagst (und wenn in deinem
Haushalt noch echte Alexas stehen, nimm nicht Alexa).

Du willst dein eigenes Wort? Trainiere ein Modell mit `oww_forge/` (siehe
dessen README) und lade das `.onnx` über die Kachel **+ Custom model** hoch —
es liegt im Datenvolumen des Controllers, erscheint als Kachel neben den
mitgelieferten Wörtern und wirkt sofort mit der Auswahl. Das `×` auf einer
nicht ausgewählten eigenen Kachel löscht sie.

**Wenn das Gerät seine Wakeword-Erkennung selbst macht** (siehe *Wakeword auf
dem Gerät*), braucht es den Erkenner erst auf sich, bevor es auf dieses Wort
hören kann.

Alle vier mitgelieferten Wörter werden beim Einrichten installiert, das
Wechseln zwischen ihnen ist also sofort und braucht keine Kopie. Ein
**eigenes** Modell wird bei der Auswahl hinüberkopiert: Das Gerät antwortet
weiter auf sein **aktuelles** Wakeword, bis das neue ankommt — meist ein paar
Sekunden — und wenn das Kopieren scheitert, bleibt es bei dem Wort, das es
schon hat, und das Log sagt warum. Es wird nie mit einem Wort zurückgelassen,
das es nicht hat.

Geräte, die vor dieser Ergänzung eingerichtet wurden, bekommen die fehlenden
mitgelieferten Erkenner beim nächsten Verbinden automatisch.

**Das Wakeword zu ändern verbindet das Gerät in Home Assistant kurz neu.**
Home Assistant liest die Wakeword-Konfiguration eines Satelliten nur beim
Verbinden, der Controller trennt und erneuert diese Verbindung also, damit der
neue Name auftaucht. Es dauert ein paar Millisekunden, aber währenddessen
**gehen alle Entitäten dieses Geräts auf „nicht verfügbar" und kommen sofort
zurück** — der Sprachassistent, der Media Player, die Aktionstaste, der
Helligkeitssensor.

Das ist wichtig, wenn du eine Automation mit einem **Zustandsauslöser** auf
einer davon hast: Zurückzukommen ist eine Zustandsänderung, und die Automation
feuert. Die Ereignis-Entität der Aktionstaste trifft die Leute am häufigsten,
weil eine zurückkehrende Ereignis-Entität ihr letztes Ereignis wiederherstellt
und genau so aussieht, als wäre die Taste noch einmal gedrückt worden.
Schließe den Übergang aus:

```yaml
trigger:
  - platform: state
    entity_id: event.your_device_action_button
    not_from:
      - unavailable
      - unknown
```

Mit dem Gerät ist nichts falsch, wenn das passiert, und es passiert nur, wenn
du das Wakeword änderst.

### Arbitration window
Bei mehr als einem Echo startete das Wakeword in Hörweite von zweien früher
zwei konkurrierende Gespräche. Jetzt **antwortet das erste Gerät, das dich
hört, sofort**, und jedes andere Gerät, das dasselbe Wort innerhalb dieses
Fensters erkennt (Standard 700 ms), tritt still zurück.

Es gibt **keine Verzögerungskosten**: Der Gewinner beansprucht das Gespräch
sofort, statt das Fenster abzuwarten, ein einzelnes Aufwachen ist also genau
so schnell wie vorher. Das Fenster entscheidet nur, wie lange danach ein
zweites Gerät noch als „dieselbe Äußerung" zählt. `0` schaltet es ab, und es
gilt nie, wenn nur ein Gerät online ist.

Eine frühere Fassung wartete das Fenster stattdessen ab und gab das Gespräch
dem Gerät, das dich am *besten* gehört hatte. Das wurde verworfen: Es belastete
jedes Aufwachen mit rund 364 ms, selbst ohne Konkurrenz, und Felddaten zeigten,
dass der Gewinner nach Signal-Rausch-Abstand ein *schlechteres* Transkript
lieferte als das Gerät, das dich schlicht zuerst gehört hatte.

### Sensitivity (Precise ↔ Eager)
Die Vertrauensschwelle, die der Erkenner überschreiten muss.

- Richtung **Precise**: weniger Fehlauslöser (Auslösen durch den Fernseher),
  aber es überhört dich womöglich manchmal.
- Richtung **Eager**: erwischt dich zuverlässiger, aber rechne mit
  gelegentlichen Geisteraktivierungen.

**Wie man es abstimmt**: Der Status-Reiter zählt **Beinahe-Treffer** — Momente,
in denen der Wert nah dran war und nicht auslöste. Wirst du überhört und die
Beinahe-Treffer steigen, geh eine Stufe Richtung Eager. Wacht es auf, wenn
niemand gesprochen hat, geh Richtung Precise.

### Barge-in
Lässt das Wakeword **den Assistenten mitten im Gespräch unterbrechen** — sag
das Wakeword, während er dir einen Absatz vorliest (oder noch über deine
letzte Frage nachdenkt), und er hält an und hört zu. **Schalte zuerst Echo
cancel (AEC) ein**: Barge-in funktioniert, indem die Mikrofone scharf bleiben,
während das Gerät spricht, und AEC ist das, was es davon abhält, sich selbst
zu hören.

Die **Barge-Schwelle** ist das Wake-Vertrauen, das während der Wiedergabe
nötig ist, und sie liegt entgegen der Intuition *unter* der normalen
Wake-Schwelle. Der Lautsprecher ist an den Mikrofonen viel lauter als du,
deine Stimme erreicht über der Wiedergabe also niedrigere Werte als in einem
ruhigen Raum — gemessen etwa 0,3 bis 0,5, gegen 0,5 bei einem gewöhnlichen
Aufwachen.

**Der Standard ist 0,25, angehoben von 0,05.** Der alte Wert war gegen kurze
Antworten gewählt und überlebte lange nicht: Bei der Bitte um eine Geschichte
erreichte die eigene Erzählung des Assistenten bis zu 0,18 und unterbrach sich
mitten im Satz. Durchgehende Sprache bietet schlicht mehr Gelegenheiten, kurz
wie ein Wakeword zu klingen. Zusätzlich sind jetzt zwei aufeinanderfolgende
Erkennungen nötig, was ein einzelnes verirrtes Frame harmlos macht.

Wenn sich eine Antwort je selbst abschneidet, hebe das an. Wenn das
Unterbrechen nicht mehr funktioniert, senke es — prüfe aber zuerst, ob AEC an
ist, denn das ist die häufigere Ursache.

**Während der stillen *Denkpause* gilt stattdessen die normale
Wake-Empfindlichkeit** — es spielt nichts, die niedrige Schwelle wird dort
also nicht gebraucht.

> **Das wurde erst in 2.21.0 wahr.** Bis dahin galt beim Nachdenken eine
> eigene, niedrigere Schwelle, und sie lag im Bereich, den gewöhnliche
> Raumgeräusche erreichen — Hintergrundgeräusche konnten also die Frage
> abbrechen, die du gerade gestellt hattest, und das Mikrofon wieder auf dich
> öffnen, bevor irgendetwas transkribiert war. Über 505 Beinahe-Treffer und 30
> echte Erkennungen auf dieser Hardware gemessen: Kein echtes Wakeword lag
> unter 0,502, und Rauschen erreichte 0,462. Die niedrigere Schwelle glich
> einen Fehler aus, der andernorts längst behoben war. Beachte, dass die
> **Barge-Schwelle** die Denkphase nie geregelt hat — sie anzuheben war also
> nicht die Lösung, und sie jetzt zu senken kostet hier nichts, denn die alte
> Stufe war stattdessen von der *Wake*-Schwelle abgeleitet.

**Was nach dem Unterbrechen passiert.** Das Gerät hört auf zu sprechen und
hört sofort zu — sag Wakeword und neuen Befehl in einem Atemzug, und es hört
beides, ohne auf eine zweite Aufforderung zu warten. Das funktioniert erst
seit 2.20.1 richtig: Davor schnitt die Unterbrechung die Antwort ab, aber der
folgende Befehl wurde nie aufgenommen, du musstest also warten und erneut
fragen.

**Eine Unterbrechung lässt sich nicht zurücknehmen.** Wenn du das Wakeword
sagst und dann still bleibst, ist die ursprüngliche Antwort weg statt
fortgesetzt — Home Assistant kann eine bereits verworfene Antwort nicht neu
starten. Das Gespräch endet einfach leise.

### Speex denoise
Lässt eine Rauschbereinigung über den Ton laufen, *nur für die
Wakeword-Bewertung* (deine eigentlichen Befehle bleiben unangetastet). In
Räumen mit dauerhaftem Hintergrundgeräusch (Fernseher, Klimaanlage) einen
Versuch wert, wenn die Wake-Erkennung dort unzuverlässig ist. Standardmäßig
aus — eine „ausprobieren und vergleichen"-Option.

### Wake word detection
Wer entscheidet, dass du das Wakeword gesagt hast. Drei Einstellungen:

- **Controller** (Standard) — der Dot streamt Ton, und der Controller hört zu.
  Was Revoice immer getan hat.
- **Both (compare)** — der Echo lässt *ebenfalls* dasselbe Modell über
  denselben Ton laufen und meldet, was er erkannt hätte, ohne darauf zu
  handeln. Er löst nie ein Gespräch aus. Das ist die Einstellung, mit der man
  anfängt: Sie sagt dir, ob die Erkennung auf dem Gerät auf deiner Hardware,
  in deinem Raum vertrauenswürdig ist, bevor irgendetwas davon abhängt.
- **On device** — der Echo entscheidet, und der Controller startet das
  Gespräch auf sein Wort hin.

**Warum du „On device" wollen könntest.** Die Wake-Entscheidung überquert dein
Netzwerk nicht mehr, wird also nicht von einem schlechten Moment auf der
Strecke verzögert. Auf einer grenzwertigen Verbindung ist das der Unterschied
zwischen einem Dot, der zügig reagiert, und einem, der unvorhersehbar hängt.

Sei dir im Klaren darüber, was es **nicht** tut:

- **Es verringert den Netzwerkverkehr nicht.** Der Ton streamt weiterhin
  durchgehend, weil der Controller den Rest des Gesprächs führt.
- **Es funktioniert nicht ohne Controller weiter.** Das Wakeword ist nur der
  erste Schritt; das Gespräch selbst braucht den Controller für Home
  Assistant, den Mikrofonstrom und die gesprochene Antwort. Ein Aufwachen,
  während der Controller weg ist, lässt den Ring leuchten und führt nirgendwo
  hin.

Der Controller hört weiterhin mit, was den Vergleich in **Activity** am Laufen
hält, damit du siehst, ob die beiden übereinstimmen. Das kostet nichts
*Zusätzliches* — es ist dieselbe Arbeit, die der Controller im Modus
**Controller** ohnehin tat —, aber es ist Arbeit, die nicht mehr zwingend
nötig ist, sobald du dem Gerät traust, und auf einer ausgelasteten
Home-Assistant-Maschine willst du sie vielleicht nicht bezahlen. **Both
(compare)** ist der Modus zum Messen; überlege, dorthin zurückzugehen, wenn du
die Zahlen willst, statt sie ewig laufen zu lassen.

Barge-in — eine Antwort durch Darüberreden zu unterbrechen — wird in jedem
Modus vom Controller bewertet und ist von dieser Einstellung unberührt.

Jede Zeile eines Sprachgesprächs in **Activity** zeigt beide Werte
nebeneinander, und die Aktivitäts-API je Gerät liefert eine Zusammenfassung
der Übereinstimmung (wie oft sie übereinstimmten, wie weit sie in
Millisekunden auseinanderlagen, und Überschreitungen, die das Gerät sah und
die nie ein Gespräch wurden).

**Vorbehalt bei mehreren Geräten.** Wenn mehrere Echos einander hören können,
stell vorerst nur einen auf **On device**. Die Regel, die verhindert, dass
zwei Dots gleichzeitig antworten, beurteilt Ansprüche noch danach, wann sie
eintreffen, und nicht danach, wann jeder Echo dich tatsächlich gehört hat —
ein Gerät, dessen Nachricht verzögert wurde, kann also gegen eines verlieren,
das dich schlechter gehört hat. Bei einem einzelnen so eingestellten Gerät
oder bei Echos, die einander nicht hören, gilt das nicht.

Drei Dinge, die du wissen solltest, bevor du Controller verlässt:

- **Es braucht Dateien auf dem Dot**, die nicht Teil der Firmware sind — ONNX
  Runtime plus die Wakeword-Modelle, etwa 15 MB, abgelegt unter
  `/data/local/share/revoice/oww`. Sie sind bewusst nicht im Firmware-Image,
  denn das würde sowohl den Download als auch den Platz verdoppeln, den jeder
  der beiden Firmware-Slots braucht. Bis sie da sind, tut die Einstellung
  nichts, und das Gerätelog sagt, welche Datei fehlt.
- **Es kostet dauerhaft etwa einen halben CPU-Kern**, weil der Wake-Strom
  immer läuft. Gemessen auf einem Echo Dot Gen 2, der die Kapazität dafür hat
  — die Mikrofonpipeline war über Stunden Nutzung unbeeinflusst, auch während
  der Musikwiedergabe —, aber aktiviere es **auf einem Gerät nach dem
  anderen** und beobachte das Feld **Resources** im Status-Reiter.
- **Es braucht aktuelle Firmware**, und die beiden Einstellungen brauchen
  unterschiedliche Jahrgänge: Das Bewerten kam vor dem Auslösen. Jede Option
  ist auf einem Echo, dessen Firmware sie nicht kann, deaktiviert und sagt das
  auch, statt so zu tun, als funktioniere sie.

---

## 03 — Microphones

Wie deine Stimme aufgenommen wird. Diese Einstellungen wurden sorgfältig
abgestimmt — die Voreinstellungen sind der einzige Teil, den die meisten
anfassen sollten.

### Aufnahmemuster (Omni / Front / Rear)
Der Dot hat 7 Mikrofone. Während eines Befehls kann er das Mikrofon
bevorzugen, das deiner Stimme am nächsten ist:

- **Omni** — für alles das mittlere Mikrofon nehmen. Die sichere Wahl; auch
  der Rückfall, falls die gerichtete Aufnahme sich je danebenbenimmt.
- **Front / Rear** — dauerhaft eine Seite bevorzugen. Für Dots an einer Wand
  oder neben einem Fernseher: die Aufnahme *weg* vom Geräusch richten.
- Mit eingeschalteter gerichteter Aufnahme und ohne feste Richtung wählt das
  Gerät bei jedem Aufwachen automatisch das Mikrofon — siehe den Abschnitt
  „Lock-back" im Pipeline-Dokument.

### Advanced (innerhalb des Abschnitts „Microphones")

**MICPGA / Digital gain** — Pegel der Hardwareverstärker in den Audiochips des
Dots, an Amazons eigene Werkseinstellungen angeglichen. *Lass die in Ruhe*,
außer du gräbst tief; falsche Werte können alle Mikrofone auf einmal
verzerren.

**Mic gain (dB)** — die Softwareverstärkung auf das rohe
24-Bit-Mikrofonsignal, bevor irgendetwas anderes es hört. Standard **24 dB**,
aus echten Messungen gewählt (die Rohaufnahme des Dots ist extrem leise — ohne
diese Anhebung scheiterte die Spracherkennung regelmäßig). Erhöhe sie nur,
wenn ein Gerät in einem sehr großen Raum immer noch leise misst; das Gerät
meldet in seinem Log „clipped"-Samples, wenn du zu weit gegangen bist. Senke
sie Richtung 0, wenn du je Übersteuern siehst.

**Beam angle / Beamforming** — die Rohregler hinter den Aufnahmemustern. Ein
Beam-Winkel von `-1` heißt „bei jedem Aufwachen automatisch wählen"; jede
andere Zahl legt die Aufnahmerichtung in Grad fest (0 = die Seite mit der
Lauter-Taste, im Uhrzeigersinn). Die Voreinstellungen setzen beides für dich.

**Noise suppression** — säubert den Ton, der an die Spracherkennung geht (und
nur den — das Wakeword-Zuhören bleibt unangetastet). Es nutzt einen kleinen
neuronalen Rauschentferner (DTLN), der auf dem Controller läuft, den Dot
belastet es also nicht. Hilft am meisten gegen *gleichmäßiges* Rauschen —
Lüfter, Klimaanlage, Gerätebrummen — in Räumen, aus denen Transkripte
verstümmelt zurückkommen. Es entfernt weder andere Sprechende noch den
Fernseher; dafür ist es das Werkzeug, den Beamformer von ihnen wegzurichten.
Standardmäßig aus — schalte es je Gerät ein und vergleiche Transkripte.

Die Unterdrückung ist auf 20 dB begrenzt, eine Passage, die der Entferner für
Rauschen hält, wird also deutlich heruntergedrückt statt ganz entfernt. Bevor
es diese Grenze gab, konnte er leise Sprache völlig verstummen lassen — ein
Wort oder zwei verschwanden mitten im Satz, statt dumpf zu klingen.

**Echo cancel (AEC)** — bringt den Mikrofonen bei, *die eigene Stimme des
Dots* von dem abzuziehen, was sie hören. Nutzen: Das Gerät kann dich während
und direkt nach seinen eigenen Antworten richtig hören (Rückfragen
funktionieren viel besser), seine eigene Sprache kann die Zuhörlogik nicht
verwirren, und es ist die Voraussetzung für Barge-in. Standardmäßig aus;
schalte es je Gerät ein und prüfe, dass die Zeilen `[aec] att=` im Gerätelog
während einer Antwort eine steigende Dämpfung zeigen. Zwei Stellschrauben:

- **AEC delay** — die Ausrichtung zwischen dem, was gespielt wurde, und dem,
  was die Mikrofone hörten. **Lass es bei 0** — das ist der gemessene
  richtige Wert für diese Hardware (die Pufferung der Mikrofonpipeline nimmt
  die Lautsprecherlatenz selbst auf). Es anzuheben kann die Auslöschung
  stillschweigend ganz abschalten.
- **AEC tail** — wie viel Raumhall die Auslöschung modelliert. Standard
  300 ms; in großen, hallenden Räumen Richtung 500 anheben.

**Save utterances** — bewahrt den Ton der letzten Sprachgespräche auf, damit
du *hören* kannst, was zur Transkription geschickt wurde. Der Reiter
**Activity** zeigt dann an jedem Gespräch mit Aufnahme ein ▶ (hier abspielen)
und ein ⤓ (WAV herunterladen).

Gespeichert wird der Ton **genau so, wie die Spracherkennung ihn bekommen
hat** — wenn **Noise suppression** an ist, hörst du also die gesäuberte
Fassung und nicht das rohe Mikrofon. Das ist Absicht: Wenn ein Transkript
falsch zurückkommt, ist die einzige Aufnahme, die das erklären kann, die, die
der Erkenner tatsächlich gehört hat.

Das ist die ehrliche Art, „ist mein Mikrofon gut?" zu beantworten. Ohne sie
rätst du aus einem verstümmelten Transkript, und das kann dir nicht sagen, ob
der Raum laut war, die Verstärkung zu niedrig oder der Rauschentferner ein
Wort zerkaut hat. Dreißig Sekunden Zuhören klären das meist — und es ist die
einzige sinnvolle Art, **Mic gain**, die Aufnahmemuster oder **Noise
suppression** gegeneinander zu testen, weil du dieselbe Phrase vorher und
nachher vergleichen kannst.

**Standardmäßig aus, und vor dem Einschalten des Nachdenkens wert.** Das ist
die einzige Einstellung, die erkennbare Sprache auf dem Controller speichert.
Was aufbewahrt wird: die **letzten 10 Gespräche je Gerät**, als schlichte
WAV-Dateien im Datenordner des Controllers, jeweils überschrieben, sobald
neuere kommen. Gespeichert wird nur der Ton, der zur Erkennung geschickt wurde
— nie das immer laufende Wakeword-Zuhören, das durchgehend verworfen und
nirgends geschrieben wird.

Die Einstellung wieder auszuschalten stoppt neue Aufnahmen sofort, **lässt die
bereits gespeicherten aber liegen** — bewusst, damit das Ausschalten keine
Proben zerstört, die du gerade halb verglichen hast. Sie bleiben, bis neuere
Aufnahmen sie verdrängen (wofür die Einstellung wieder an sein muss) oder du
das Gerät löschst, was seine Aufnahmen mitnimmt. Um sie früher loszuwerden,
lösche die Dateien aus dem Ordner `data/recordings/` des Controllers.

Ein länger zurückliegendes Gespräch zeigt womöglich keine Schaltflächen — das
heißt nur, dass seine Aufnahme aus den letzten 10 herausgefallen ist und der
Gesprächsverlauf sie überlebt hat.

---

## 04 — Ring

Die Farben, die der LED-Ring während Gesprächen benutzt. Szenen wirken sofort
und können sich je Gerät unterscheiden. Auf aktueller Firmware (v2.9+)
animiert das Gerät den Ring selbst — der Controller sendet je Zustandswechsel
eine Anweisung „spiele diese Animation", der Spinner bleibt also unabhängig
von WLAN oder Controller-Last vollkommen flüssig, und während eine Antwort
gesprochen wird, **pulsiert der Ring im Takt des Tons** (die Helligkeit folgt
dem tatsächlichen Pegel aus dem Lautsprecher). Verschwindet der Controller je
mitten im Gespräch, läuft der Ring in ein Zeitlimit, statt ewig zu drehen.
Ältere Firmware fällt auf vom Controller gezeichnete Einzelbilder zurück.

- **Standard** — das klassische Grün.
- **Airy** — ein blasses, ruhiges Himmelblau.
- **Malevolent** — tiefroter Zuhör-Ring mit glutfarbenem Spinner.
- **Pride** — ein rotierender Regenbogen.
- **Custom** — wähle eigene Farben für **Listening** (durchgehender Ring beim
  Aufnehmen) und **Thinking** (Spinner beim Verarbeiten).

Zwei Dinge ändern sich in keiner Szene: der **rote Mute-Ring** (Rot heißt
immer, dass die Mikrofone aus sind — eine Datenschutzanzeige, keine
Dekoration) und der cyanfarbene Lautstärkebogen. Die gerichtete Hervorhebung
„welches Mikrofon hört zu" passt sich ebenfalls automatisch an: Sie hellt die
Ringfarbe der Szene auf, statt Grün zu malen.

Der Lautstärkebogen hält den Ring etwa zwei Sekunden, damit eine
Gesprächsanimation ihn nicht sofort überschreibt — aber **ein Druck auf die
Aktionstaste bricht ihn sofort ab**, die Lautstärke zu ändern und dann mit dem
Gerät zu sprechen zeigt dir den Zuhör-Ring also unmittelbar.

### Wie ein Gespräch endet
Der Ring sagt dir, *warum* ein Gespräch aufgehört hat, über den Rhythmus statt
über die Farbe (Rot, Orange und Cyan bedeuten schon Mute, kein Controller und
Lautstärke):

- **Ein langsames Pulsieren** — das Gerät hörte zu und hat nichts gehört.
- **Ein paar schnelle Blinker** — etwas ging schief (Home Assistant hat einen
  Fehler gemeldet, oder es kam keine Sprache zurück).
- **Der Ring geht einfach aus** — normales Ende, oder du hast selbst
  abgebrochen.

Der Ring erlischt inzwischen auch dann, wenn der Ton *tatsächlich* fertig ist,
statt wenn der Controller schätzt, er müsste es sein. Auf einer langsamen
WLAN-Strecke konnte die alte Schätzung den Ring mehrere Sekunden löschen,
bevor der Dot zu Ende gesprochen hatte.

### Meter response (Advanced)
Während eine Antwort spielt, pulsiert der Ring mit dem Live-Lautsprecherpegel.
Das Feld **Advanced** hier formt, wie kräftig er pulsiert — das Gerät zeichnet
es lokal, Änderungen wirken also bei der nächsten Antwort, ohne Neustart:

- **Decay** — wie schnell er fällt. Höher folgt einzelnen Silben, niedriger
  liest sich als langsames Anschwellen.
- **Attack** — wie schnell er auf eine Spitze steigt.
- **Gamma** — Kontrast. Höher macht den Ausschlag sichtbarer.
- **Floor** — Helligkeit bei Stille. `0` wird zwischen Wörtern ganz dunkel.
- **Reference** — der Lautsprecherpegel, der auf volle Helligkeit abgebildet
  wird. Niedriger ist empfindlicher.
- **Curve** — unter `1` hebt leise Konsonanten ins Sichtbare.

Das sind Geschmackseinstellungen, und genau deshalb sind sie hier einstellbar
statt in die Firmware eingebacken. Die Voreinstellungen sind auf Sprache
abgestimmt; wenn der Ring zu statisch aussieht, hebe zuerst **Decay** und
**Gamma** an.

## 05 — Advanced

Alles in diesem Abschnitt betrifft **nur Gespräche per Tastendruck** (die
Aktionstaste antippen, um ohne Wakeword zu sprechen — ein *Halten* ist eine
eigene Geste, die stattdessen ein Ereignis in Home Assistant feuert).
Wakeword-Gespräche ignorieren all das — sie werden von der Spracherkennung
von Home Assistant geführt.

Bei stummem Mikrofon tut ein Antippen nichts, ein Halten feuert sein Ereignis
aber weiterhin: Das Mute schweigt Sprache, nicht die Taste.

### Aus dem Antippen ein Ereignis machen

**Tap fires an event** (`buttonSingleTapEvent`) — macht aus einem Antippen
ein Home-Assistant-Ereignis statt des Beginns eines Gesprächs, du kannst es
also mit allem belegen, was du willst. Damit startet das Gerät über die Taste
gar kein Sprachgespräch mehr; das Wakeword bleibt unangetastet, und ein Halten
feuert weiterhin `long`. Ein Antippen feuert auch bei Mute, aus demselben
Grund wie ein Halten — es ist ein Ereignis, keine Sprache.

Braucht Firmware v2.10.0 oder neuer. Auf älterer Firmware ist der Schalter
deaktiviert und sagt das auch.

**Binde zerstörerische Automationen an das Halten, nicht ans Antippen.** Die
Taste hat keine Authentifizierung, und ein Lautsprecher auf der
Küchenarbeitsplatte ist erheblich leichter versehentlich anzutippen als eine
Dreiviertelsekunde zu halten.

**Multi-tap window** (`buttonMultiTapMs`) — setz es über null, und Antipper
werden zu `single`, `double` oder `triple` gruppiert. Der Preis: *Jedes*
Antippen wird um dieses Fenster verzögert, denn ein Antippen kann erst dann
„single" heißen, wenn kein zweites folgt.

**Nimm 350 ms.** Unterhalb von etwa 300 ms kämpft es sowohl gegen menschliches
Timing — Doppeltipper liegen rund 150–400 ms auseinander — als auch gegen
Netzwerkschwankungen, denn der Abstand wird derzeit gemessen, wenn die
Antipper den Controller erreichen, und nicht auf dem Gerät. Auf einem
ausgelasteten oder entfernten Gerät brauchst du womöglich mehr. Null schaltet
die Gruppierung ab, und ein Antippen feuert sofort `single`.

### Gesprächsverarbeitung

**Auto gain (AGC)** — gleicht die Lautstärke deiner Stimme bei
Tastengesprächen automatisch aus, damit Flüstern und Rufen ähnlich
herauskommen. Hier harmlos; auf das Wakeword-Zuhören wird es bewusst nie
angewandt (eine automatische Verstärkung, die mit dem Raumgeräusch driftete,
war die Ursache eines „reagiert nach ein paar Tagen nicht mehr"-Fehlers, und
sie bleibt von diesem Pfad verbannt).

### Sprachgatter

Entscheidet, wann eine Äußerung per Tastendruck beginnt und endet:

- **Threshold** — wie laut als „Sprache" zählt. Gemessen in Einheiten vor der
  Verstärkung (die Mikrofonverstärkung ändert nicht, was diese Zahl bedeutet).
  Der Standard 0,001 wurde per Messung bestätigt; hebe ihn nur in wirklich
  lauten Räumen leicht an (0,003–0,005).
- **Speech gate (ms)** — wie viel durchgehende Sprache das Gatter öffnet.
  Höher = ignoriert kurze Geräusche, schneidet aber schnelle Sprechende ab.
- **Silence gate (ms)** — wie viel Stille dein Gespräch beendet. Höher = du
  kannst mitten im Satz Luft holen, ohne abgeschnitten zu werden; niedriger =
  flottere Antworten. Standard 900 ms; hebe auf ~1200 an, wenn du mitten im
  Gedanken abgeschnitten wirst.

Hinweis (v2.9.4): Diese beiden Zeiten verhalten sich jetzt genau so, wie
konfiguriert. Ältere Firmware wandte sie still rund 5× länger an, als die Zahl
sagte (ein Zählfehler gegen die echte Stapelgröße des Mikrofons) —
Tastengespräche hingen also vor dem Ende ein paar Sekunden in der Stille.
Wenn sich Gespräche nach einem Update flotter anfühlen, ist das der Grund, und
wenn jetzt langsam Sprechende abgeschnitten werden, hebe das Stillegatter an.

---

## 06 — Bluetooth

**Bluetooth proxy** — macht aus dem Dot einen
Home-Assistant-Bluetooth-Proxy. Das Gerät hört passiv auf Bluetooth-Low-Energy-Advertisements
(Anwesenheits-Beacons, BLE-Temperatur- und Feuchtesensoren, Handys und Uhren
für Raumanwesenheitssysteme wie Bermuda) und reicht sie an Home Assistant
weiter.

In Home Assistant erscheint der Proxy als **eigenes ESPHome-Gerät** (benannt
`<Bezeichnung> BT Proxy`), unabhängig vom Sprachassistenten — du kannst es
hinzufügen, entfernen oder ignorieren, ohne den Sprachsatelliten anzufassen.
Einmal hinzugefügt, speist sein Scanner die Bluetooth-Integration von HA
genauso, wie es ein ESP32-Bluetooth-Proxy täte, und ein Diagnosesensor zählt
empfangene Advertisements.

Zwei Dinge vor dem Aktivieren:

- Das Aktivieren **schaltet den Bluetooth-Chip des Dots dauerhaft von Androids
  Stack weg** (es übersteht Neustarts). Nichts, was Revoice nutzt, braucht
  Androids Bluetooth — aber das Koppeln als Bluetooth-Lautsprecher wie im
  Original ist auf diesem Gerät dann nicht mehr möglich.
- Der Proxy ist **nur empfangend** (passives Scannen). Geräte, die eine aktive
  Verbindung zum Auslesen brauchen (manche smarten Schlösser, ältere
  BLE-Geräte), werden nicht unterstützt — Sensoren auf Advertisement-Basis und
  Anwesenheitserkennung schon.

Die Diagnose liegt im Reiter **Status** des Geräts (Feld
„Bluetooth proxy"): Scannerzustand, gesehene Advertisements, Anzahl der Geräte
in der Nähe und ob Home Assistant verbunden ist und empfängt.

---

## WLAN (Geräteseite → Reiter „Config", oberster Abschnitt)

Ein Gerät in ein anderes WLAN umziehen, ohne ADB anzufassen. Der Abschnitt
oben im Reiter „Config" zeigt das aktuelle Netzwerk, das Signal und die IP,
lässt dich nach sichtbaren Netzwerken suchen und wechselt mit einem
Bestätigungsschritt.

Der Wechsel ist so entworfen, dass man sich **nicht aussperren kann**: Das
Gerät wendet die Änderung selbst an und muss drei Prüfungen bestehen — dem
Netzwerk beitreten, eine IP bekommen und **sich wieder mit diesem Controller
verbinden** —, bevor die Änderung behalten wird. Scheitert eine davon (falsche
Passphrase, DHCP-Probleme oder ein Netzwerk, das funktioniert, aber den
Controller nicht erreicht, etwa ein abgeschottetes Gäste-VLAN), stellt es
automatisch das vorherige Netzwerk wieder her und sagt dir warum. Selbst ein
Stromausfall mitten im Wechsel wird aufgefangen: Eine unbestätigte Änderung
wird beim Start zurückgerollt. Rechne mit etwa zwei Minuten, bis das Gerät
verschwindet und zurückkommt.

---

## Controller-Einstellungen (die Datei `.env`)

Diese werden einmal auf dem Server gesetzt und brauchen zum Ändern einen
Neustart des Controllers:

| Einstellung | Was es ist |
|---|---|
| `SERVER_IP` | Die LAN-IP des Controller-Rechners — die Adresse, zu der Geräte verbinden sollen. Leer lassen, um sie von diesem Host zu erkennen; der Controller weigert sich zu starten, statt eine geratene Adresse anzukündigen, und warnt, wenn die erkannte nach einer Container-Bridge aussieht. |
| `OWW_MODEL` / `OWW_THRESHOLD` | Startvoreinstellungen für Wakeword und Empfindlichkeit — die Werte im Dashboard überschreiben sie. |
| `DEVICE_APPROVAL` | `strict` (du gibst jedes neue Gerät frei — empfohlen) oder `auto`. |
| `SERVER_TLS_PORT` | Port für die verschlüsselte Geräteverbindung (wss) — Standard 8770, `0` schaltet ab. Geräte wechseln automatisch dorthin, sobald sie aufgespielte Zugangsdaten halten (Installation über den Assistenten oder die Schaltfläche **Secure link** im Status-Reiter des Geräts). |
| `REQUIRE_DEVICE_TLS` | Setze es auf `1` **erst, wenn jedes Gerät in seinem Status-Reiter „wss (TLS)" zeigt** — ab dann weist der Controller unverschlüsselte oder tokenlose Geräteverbindungen ab. |
| `EM_EXTRA_CA_CERT` | Pfad zu einem PEM-CA-Zertifikat, dem vertraut werden soll — nötig, wenn Home Assistant oder ein Medienserver, von dem du streamst, per HTTPS mit deiner eigenen internen Zertifizierungsstelle ausgeliefert wird. Siehe unten. |

Die vollständige Liste mit Kommentaren steht in `.env.example`.

### Verschlüsselte Geräteverbindung

Der Controller erzeugt beim ersten Start seine eigene Zertifizierungsstelle
(gespeichert in `tls/` neben der Datenbank) und nimmt neben den
unverschlüsselten auch verschlüsselte Geräteverbindungen an. Jedes Gerät
bekommt zwei Zugangsdaten — das CA-Zertifikat und einen privaten Token —,
automatisch installiert vom Einrichtungsassistenten oder mit der Schaltfläche
**Secure link** in seinem Status-Reiter auf ein bestehendes Gerät
aufgespielt. Ein Gerät mit Zugangsdaten verbindet sich ab seinem nächsten
Verbinden verschlüsselt; die Zeile **Link** im Status-Reiter zeigt, welchen
Modus jedes Gerät nutzt. Sobald die ganze Flotte `wss (TLS)` zeigt, setze
`REQUIRE_DEVICE_TLS=1`, um unverschlüsselte Verbindungen ganz auszusperren.

### Home Assistant hinter einer privaten Zertifizierungsstelle

Wenn Home Assistant per HTTPS mit einem Zertifikat deiner eigenen internen
Zertifizierungsstelle ausgeliefert wird, kann Revoice die gesprochene Antwort
nicht abholen, und **jedes Gespräch endet still** — der Controller startet
normal, der Echo wacht auf, und es kommt kein Ton. Nichts auf dem Bildschirm
erklärt das; der Fehler ist ein Zertifikatsprüffehler im Log.

**Probier das zuerst, denn es braucht kein Zertifikat.** Wenn Home Assistant
selbst weiterhin auf einfachem HTTP lauscht und etwas davor (ein Reverse
Proxy, Nginx Proxy Manager, Cloudflare) TLS übernimmt, setze die **interne
URL** von Home Assistant auf `http://<seine-adresse>:8123`. Home Assistant
baut die Audio-URL aus dieser Einstellung, sie wird also zu einem einfachen
lokalen Abruf, und das Problem verschwindet. Revoice ist in deinem eigenen
Netzwerk und der Sprung ist lokal, es geht also nichts verloren.

**Wenn Home Assistant selbst mit `ssl_certificate` konfiguriert ist**, gib
Revoice die Zertifizierungsstelle:

- **Add-on** — leg das CA-Zertifikat (PEM-Format) in den `ssl`-Ordner von Home
  Assistant und setze die Option **Private CA certificate** auf
  `/ssl/<Dateiname>`. Das Add-on liest diesen Ordner nur lesend.
- **Container** — häng das Zertifikat ein und setze `EM_EXTRA_CA_CERT` auf
  seinen Pfad *innerhalb* des Containers:

  ```yaml
  volumes:
    - /pfad/zu/internal-ca.crt:/certs/internal-ca.crt:ro
  environment:
    - EM_EXTRA_CA_CERT=/certs/internal-ca.crt
  ```

Es muss eine **PEM**-Datei sein — die Sorte mit
`-----BEGIN CERTIFICATE-----`. Wenn deine im DER-Format vorliegt, wandle sie
zuerst um:

```bash
openssl x509 -inform der -in ca.der -out ca.crt
```

Fehlt die Datei, ist sie nicht lesbar oder kein PEM, **weigert sich der
Controller zu starten und sagt, was davon** — statt zu starten und danach an
jedem Sprachgespräch mit einem Fehler zu scheitern, den niemand mit dieser
Einstellung in Verbindung bringt.

**Auch die Medienwiedergabe prüft.** Der Media Player holt seine Ströme —
Music Assistant, Radio, alles, was `play_media` bekommt — über ffmpeg, das
früher bei HTTPS-URLs *jedes* Zertifikat akzeptierte (eine
ffmpeg-Build-Voreinstellung, die niemand gewählt hätte). Medien-URLs werden
jetzt wie jeder andere Abruf geprüft. Ein per HTTPS mit privater
Zertifizierungsstelle ausgelieferter Strom braucht dieselbe Einstellung wie
oben; wird ein Strom abgewiesen, sagt das Log es und verweist hierher.

## Was dein Netzwerk verlässt

Revoice hat **keine Telemetrie**. Es gibt keine Nutzungsmeldung, keine
Analytics, keine Absturzberichte und keinen Installationszähler. Nichts
meldet, welche Funktionen du nutzt, wie viele Geräte du hast oder dass du es
überhaupt installiert hast. Das ist eine bewusste Entscheidung und kein
Versäumnis: Das Projekt existiert, um einen Cloud-Sprachassistenten aus deinem
Netzwerk zu nehmen, und still ein Nachhause-Telefonieren zu ergänzen würde den
Grund zunichtemachen, es zu betreiben.

Eine Folge, die man deutlich aussprechen sollte: **Niemand, auch nicht die
Entwickelnden, kann sagen, wie viele Leute Revoice nutzen.** Die Verbreitung
wird aus GitHub-Sternen und Download-Zahlen der Releases geraten, und das ist
der Handel, der hier eingegangen wird.

### Die eine ausgehende Verbindung

Der Controller kontaktiert einmal pro Stunde `api.github.com`, um zu fragen,
welches die neueste Version ist, damit das Dashboard dir sagen kann, dass ein
Update verfügbar ist, und dessen Notizen zeigen kann. Wenn du dich
entscheidest, ein Gerät zu aktualisieren, wird das Firmware-Binary in diesem
Moment von `github.com` geladen.

Das ist alles. Die Anfrage trägt keine Kennungen — es ist ein gewöhnlicher
nicht authentifizierter API-Aufruf —, aber wie jede Anfrage verrät sie GitHub
deine öffentliche IP, dieselbe Preisgabe wie bei einem `git clone` oder beim
Öffnen des Repositorys im Browser.

Setze `update_check_interval` (Sekunden, Standard `3600`) in der
Systemkonfiguration, um zu ändern, wie oft das passiert. Ein langes Intervall,
sagen wir `86400`, reduziert es auf einmal am Tag, und `0` schaltet es ganz ab
— der Controller macht dann überhaupt keine ausgehende Verbindung, und das
Dashboard zeigt, was es zuletzt über Releases wusste, statt nichts.

Das Wiedereinschalten wirkt ohne Neustart. Es abzuschalten deaktiviert nicht
die Schaltfläche **Check now** im Reiter „Updates": Das ist eine bewusste
Anfrage, sie fragt GitHub also weiterhin, wenn du sie drückst.

> **Vor Controller 2.21.0 tat `0` das Gegenteil** — es entfernte die Pause
> zwischen den Prüfungen, statt sie zu stoppen, der Controller fragte also
> durchgehend, bis GitHub ihn ratenbegrenzte. Wenn du es auf einer früheren
> Version auf `0` gesetzt und dort gelassen hast, behebt ein Update das; es
> gibt nichts rückgängig zu machen.

### Was nie hinausgeht

- **Sprachton und Transkripte.** Der Mikrofonton geht vom Gerät zu deinem
  Controller und weiter zu deinem Home Assistant, über dein LAN. Was danach
  passiert, ist, was deine Assist-Pipeline tut — wenn du HA für eine
  Cloud-Spracherkennung konfiguriert hast, schickt HA ihn dorthin. Revoice
  selbst schickt ihn nirgendwohin außer zu HA.
- **Gespeicherte Aufnahmen von Äußerungen** (`saveUtterances`, standardmäßig
  aus) — auf die Platte neben der Datenbank geschrieben und nie hochgeladen.
  Sie abzuspielen oder herunterzuladen ist **nur für Administratoren**,
  ebenso das Sehen des Transkripttexts eines Gesprächs: Beim
  Home-Assistant-Add-on erreicht jede Person im Haushalt das Dashboard,
  Konten mit Lesezugriff bekommen also Zeiten, Werte und Ausgänge eines
  Gesprächs ohne die Sprache. Durchgesetzt auf dem Server, nicht bloß in der
  Seite versteckt.
- **Geräte-Seriennummern, WLAN-Zugangsdaten, Netzwerknamen und die
  Konfiguration deiner Flotte.** Die leben nur in der Datenbank des
  Controllers.
- **Support-Bundles** werden nur gebaut, wenn du eines anforderst, und die
  Datei zu teilen ist deine Entscheidung. Sie schließen Sprache, Transkripte,
  Netzwerknamen und Kontonamen bewusst aus — siehe
  [support-bundle.md](support-bundle.md).
