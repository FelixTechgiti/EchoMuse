# Die Sprachpipeline, erklärt

Was zwischen deinem „Hey Rhasspy, mach das Licht aus" und dem Ausgehen des
Lichts tatsächlich passiert — Stufe für Stufe, in verständlichen Worten, mit
Nutzen und Kompromiss jeder Entwurfsentscheidung.

Die Ein-Satz-Fassung: **Der Dot ist bewusst einfach gehalten** — er nimmt
Klang so sauber wie möglich auf und schickt ihn weiter; die ganze Intelligenz
(Wakeword erkennen, entscheiden, wann du fertig gesprochen hast, dich
verstehen) sitzt im Controller und in Home Assistant, wo sie aktualisiert,
abgestimmt und beobachtet werden kann, ohne die Hardware anzufassen.

```
 DEINE STIMME
    │
    ▼
┌─ Auf dem Echo Dot ──────────────────────────────────────────┐
│  7 Mikrofone → Verstärkung → Echo-Auslöschung → Mikrofonwahl│
└──────────────────────────────│──────────────────────────────┘
                               │  durchgehender Audiostrom (WLAN)
                               ▼
┌─ Auf dem Controller ────────────────────────────────────────┐
│  Wakeword-Erkennung → Gesprächsführung → Klangbearbeitung   │
└──────────────────────────────│──────────────────────────────┘
                               │
                               ▼
┌─ In Home Assistant ─────────────────────────────────────────┐
│  Spracherkennung → Verstehen → Aktion → Sprachausgabe       │
└──────────────────────────────│──────────────────────────────┘
                               │  gesprochene Antwort
                               ▼
                   zurück zum Lautsprecher des Dots
```

---

## Stufe 1 — Sieben Mikrofone

Der Dot hat 6 Mikrofone im Ring plus 1 in der Mitte, alle zusammen
aufgenommen, 16.000-mal pro Sekunde in hochauflösendem 24-Bit-Audio.

**Nutzen:** aus allen Richtungen gleichzeitig hören, dazu das Rohmaterial für
die Frage, aus *welcher Richtung* du gesprochen hast.

**Haken:** Es sind winzige Mikrofone in einem kleinen Puck, der in deinem
Zimmer steht — sie hören den Fernseher, die Spülmaschine und den eigenen
Lautsprecher des Dots genauso deutlich wie dich. Der größte Teil des Rests
der Pipeline existiert, um damit umzugehen.

## Stufe 2 — Verstärkung („mic gain")

Die Rohaufnahme ist *extrem* leise — Messungen zeigten, dass normale Sprache
nur einen winzigen Bruchteil des verfügbaren Signalbereichs nutzt, und die
alte Verarbeitung warf den leisesten (informationsreichsten) Teil beim
Umwandeln für die Übertragung weg. Die Lösung: das volle 24-Bit-Signal *vor*
dieser Umwandlung um 24 dB (≈16×) anheben und so Details bewahren, die sonst
für immer verloren wären.

**Nutzen:** Diese eine Änderung brachte die Spracherkennung im echten Raum von
„scheitert bei jeder dritten Anfrage" auf „zuverlässig". Sie ist das
Fundament, auf dem alles Nachfolgende steht.

**Haken:** Eine feste Anhebung bedeutet, dass ein sehr lautes Ereignis (ein
Ruf direkt neben dem Gerät) an die Decke stoßen und kurz verzerren kann. Das
Gerät zählt diese „geclippten" Momente in seinem Log; in der Praxis erzeugt
selbst Fernsehen in Kinolautstärke null davon.

## Stufe 3 — Echo-Auslöschung (AEC)

Wenn der Dot spricht, hören seine Mikrofone seine eigene Stimme — laut. Die
AEC behält eine Kopie dessen, was der Lautsprecher genau abspielt, und zieht
sie rechnerisch von dem ab, was die Mikrofone hören. Übrig bleiben nur
*andere* Geräusche — etwa du, wie du dazwischengehst.

**Nutzen:** Rückfragen funktionieren richtig (das Gerät hört dich über dem
Ausklang seiner eigenen Antwort), und seine eigene Sprache kann die
Zuhörlogik weder auslösen noch verwirren. Sie ist auch die Voraussetzung für
**Barge-in** — den Assistenten mitten im Satz mit dem Wakeword unterbrechen
(siehe die Einstellung „Barge-in" im Konfigurationsleitfaden).

**Haken:** Sie entfernt nur den *eigenen* Klang des Dots — gegen den
Fernseher tut sie nichts (das ist ein anderes Problem, siehe Stufe 8). Sie
kommt ausgeschaltet, bis du sie eingeschaltet und geprüft hast. (Seit v2.7.8
bleibt die Auslöschung zwischen Antworten „warm", statt jedes Mal neu zu
lernen — wenn Barge-in früher eine erhobene Stimme brauchte, sollte es das
nicht mehr tun.)

## Stufe 4 — Mikrofonwahl („Beamforming" + „Lock-back")

Beim ruhigen Zuhören nutzt das Gerät immer das mittlere Mikrofon — es hört
alle Richtungen gleich, also funktioniert das Wakeword, wo immer du stehst.
Wachst du es *tatsächlich* auf, wechselt das Gerät auf das Ringmikrofon, das
dir zugewandt ist: Es hört dich etwas besser und den Rest des Raums etwas
schlechter.

Der feine Teil ist das *Wie* der Auswahl: Bis der Controller das Wakeword
erkannt hat, ist eine halbe Sekunde vergangen, und der Klang deines
Aussprechens ist verklungen. Also führt das Gerät durchgehend ein
Zwei-Sekunden-Gedächtnis darüber, wie viel Schallenergie aus welcher Richtung
kam, und schaut beim Eintreffen des Wakewords **zurück** in dieses Gedächtnis,
um zu finden, woher das Wakeword wirklich kam — nicht, woher gerade jetzt
Klang kommt. Außerdem bewertet es Richtungen nach *plötzlicher Änderung* statt
nach roher Lautstärke, damit eine Stimme einen dauerhaft lauten Fernseher
schlägt.

**Nutzen:** bessere Spracherkennung durch das auf dich gerichtete Mikrofon —
und die LED-Richtungsanzeige zeigt tatsächlich auf dich.

**Haken:** Ein ausgewähltes Mikrofon ist eine maßvolle Verbesserung, kein
magisches Teleobjektiv. Und in der Lücke zwischen Gesprächen kann die eigene
Sprache des Geräts in diesem Zwei-Sekunden-Gedächtnis nachhallen —
Folgegespräche bekommen die schwächere Fassung dieser Funktion, bis die
Arbeit an Barge-in und AEC reift.

## Stufe 5 — Der durchgehende Strom

Alle 32 Millisekunden geht der verarbeitete Ton per WLAN an den Controller.
Immer. Es gibt bewusst **kein** „nur senden, wenn es nach Sprache klingt"
vor diesem Strom.

**Nutzen:** Die Wakeword-Erkennung sieht gleichmäßiges, ununterbrochenes
Audio, was ihre Genauigkeit messbar verbessert — und es gibt keine Logik auf
dem Gerät, die driften, den Raum falsch einschätzen oder über Tage
schlechter werden kann (beides ist bei früheren, klügeren Entwürfen
tatsächlich passiert; langweilig hat gewonnen).

Dieser ununterbrochene Strom macht es außerdem möglich, *dieselbe* Erkennung
auf dem Dot selbst laufen zu lassen und beide auf bytegleichem Audio zu
vergleichen — genau das tut der experimentelle Bewertungsmodus auf dem Gerät,
ohne auf das Ergebnis reagieren zu dürfen. Es ist auch der Grund, warum ein
„klingt nach Sprache"-Gatter vor diesem Strom schwieriger wäre, als es
aussieht: Die internen Puffer der Erkennung setzen Kontinuität voraus, und
zusammengesetzte gegatterte Stücke drücken ihre Werte messbar.

**Haken:** dauerhaft rund 32 KB/s pro Gerät in deinem WLAN — etwa ein Sechstel
dessen, was das Streamen der *Antwort* braucht, in der Praxis also in keinem
Heimnetz ein Thema. Und zur Klarheit in Sachen Privatsphäre: Der Strom geht an
*deinen* Controller in *deinem* LAN und nirgendwo sonst.

## Stufe 6 — Wakeword-Erkennung

Der Controller lässt openwakeword, ein kleines neuronales Netz, über den
Strom jedes Geräts laufen und bewertet jeden Moment: „Wie sehr klang das nach
dem Wakeword?" Wird die Empfindlichkeitsschwelle überschritten, beginnt das
Gespräch.

Sind mehrere Geräte online, antwortet der **erste** Echo, der dich hört,
sofort; jedes andere Gerät, das dasselbe Wort innerhalb des
**Arbitrierungsfensters** erkennt (standardmäßig 700 ms, einstellbar), tritt
still zurück, und sein Ring erlischt, sobald das andere Gerät das Gespräch
für sich beansprucht. Eine Äußerung, eine Antwort, auch in Hörweite zweier
Geräte — und ohne zusätzliche Verzögerung, weil der Gewinner das Gespräch
sofort beansprucht, statt das Fenster abzuwarten.

Ein früherer Entwurf wartete das Fenster stattdessen ab und gab das Gespräch
dem Gerät, das dich am *besten* gehört hatte. Er wurde aus zwei gemessenen
Gründen verworfen: Er belastete jedes Aufwachen mit rund 364 ms, selbst ohne
Konkurrenz, und der Gewinner nach Signal-Rausch-Abstand lieferte ein
*schlechteres* Transkript als das Gerät, das dich schlicht zuerst gehört
hatte.

**Nutzen:** Weil das auf dem Controller statt auf dem Dot läuft, kannst du
Wakeword und Empfindlichkeit live im Dashboard ändern, jeden Treffer *und*
jeden Beinahe-Treffer im Reiter „Status" sehen, und künftige Verbesserungen
brauchen keine Firmware-Updates.

**Haken:** Es ist eine Wahrscheinlichkeit, keine Gewissheit — der
Empfindlichkeitsregler ist ein Kompromiss zwischen Fehlauslösern und
Nichterkennungen, den du auf deinen Raum abstimmst (der Beinahe-Treffer-Zähler
existiert genau dafür, diese Abstimmung informiert statt gefühlsmäßig zu
machen).

## Stufe 7 — Das Gespräch („Turn")

Beim Aufwachen: Die LED wird grün, die Mikrofonwahl des Geräts richtet sich
auf dich aus, und der Controller leitet deinen Ton an Home Assistant weiter,
das entscheidet, wann du aufgehört hast zu sprechen (das übernimmt dessen
eigene Sprachaktivitätserkennung — mit einer Rückfallsicherung im Controller,
die nach 5 Sekunden still beendet, wenn ein Fehlwecken bedeutete, dass
niemand sprach; gemessen am erfassten Grundgeräuschpegel dieses Raums).

**Nutzen:** Das Erkennen des Sprechendes („ist der Nutzer fertig?") erledigt
die gut gepflegte Erkennung von Home Assistant statt selbstgebauter Logik,
und die Fehlwecken-Sicherung passt sich jedem Raum von allein an — ein ruhiges
Arbeitszimmer und ein lautes Wohnzimmer verhalten sich beide vernünftig, ohne
dass du etwas einstellst.

**Es gibt eine zweite Sicherung, für den Fall, dass diese Erkennung gar nicht
erst anspringt.** Home Assistant muss etwa eine drittel Sekunde Sprache hören,
bei der es sich sicher ist, bevor es entscheidet, dass du angefangen hast zu
sprechen — und bevor es das entscheidet, kann es nicht entscheiden, dass du
aufgehört hast. Ein kurzer Befehl wie „Stopp" nimmt diese Hürde womöglich nie;
dann wartet Home Assistant sein eigenes Fünfzehn-Sekunden-Limit ab und meldet
das Gespräch, als hättest du einfach zu Ende gesprochen. Nichts, was es
sendet, sagt etwas anderes — deshalb sah das lange so aus, als wäre der Echo
langsam.

Also achtet der Controller auf genau dieses Muster — Sprache gehört, und Home
Assistant hat immer noch nicht gesagt, dass es das bemerkt hat — und beendet
das Gespräch etwa eine Sekunde nach deinem Verstummen selbst. Wann immer die
eigene Erkennung von Home Assistant funktioniert, entscheidet weiterhin sie;
ihr Urteil ist besser als unseres. Upstream gemeldet als
home-assistant/core#181747.

**Haken:** In einem lauten Raum hängt die Erkennung manchmal einen Takt zu
lange, und der Ausklang eines Fernsehdialogs reist mit in die Spracherkennung
(gelegentlich siehst du einen fremden Satzfetzen an dein Transkript
angehängt). Den Ton vor der Spracherkennung zu säubern ist die nächste
geplante Verbesserung dafür.

## Stufe 8 — Spracherkennung, Verstehen, Aktion

Die Assist-Pipeline von Home Assistant übernimmt: Deine Sprache wird Text
(Whisper oder welche Spracherkennung du konfiguriert hast), der Text wird
Absicht („ausschalten + Küchenlicht"), die Aktion passiert, und eine Antwort
wird formuliert.

**Nutzen:** Das ist alles gewöhnliche, gut dokumentierte
Home-Assistant-Maschinerie — jede STT-, LLM- und TTS-Option, die HA
unterstützt, funktioniert, und Revoice muss nichts davon wissen.

**Haken:** Hier geht auch die meiste *Zeit* hin (Transkription und
Antworterzeugung sind die langsamen Schritte, besonders auf bescheidener
Hardware), und hier landen letztlich die Transkriptionsfehler durch
Hintergrundgeräusche. Bessere Mikrofone und saubereres Audio helfen; ein gutes
STT-Modell ersetzen sie nicht.

## Stufe 9 — Die Antwort

Der Antwortton kommt durch den Controller zurück, der den Klang bearbeitet
und ihn zum Dot streamt, der ihn abspielt, während eine Kopie an die
Echo-Auslöschung geht (Stufe 3), damit die Mikrofone ihn abziehen können. Der
Ton kommt in der nativen Rate der Hardware an: Der Satellit sagt Home
Assistant, welches Format der Lautsprecher will (48 kHz mono), sodass neuere
HA-Versionen an der Quelle umwandeln; ffmpeg deckt beim Dekodieren alles
Übrige ab.

Während der Wiedergabe pulsiert der Ring im Takt, und er erlischt, wenn der
Dot meldet, dass er *tatsächlich* fertig ist — nicht, wenn der Controller
schätzt, er müsste es sein. Die alte Schätzung konnte den Ring auf einer
langsamen WLAN-Strecke mehrere Sekunden vor dem Verstummen des Lautsprechers
löschen; nur das Gerät weiß, wann sein eigener Puffer leerläuft.

Die Bearbeitung hat drei Stufen, in dieser Reihenfolge: **Equalizer**, dann
**Bass-Schutz**, dann **Limiter** — alle aus dem Konfigurationsleitfaden. Die
Reihenfolge ist wichtig. Der Schutz entfernt tiefe Frequenzen, die der kleine
Lautsprecher gar nicht erzeugen kann, und genau das lässt die Mitten klar
statt kastig klingen; das vor dem Limiter zu tun heißt, dass der Limiter nicht
die ganze Antwort herunterhält, um Bassspitzen unterzubringen, die ohnehin
niemand gehört hätte. Gemessen kommen die Mitten mit eingeschaltetem Schutz
sogar etwas *lauter* heraus als ohne.

**Nutzen:** Zentral angewandte Bearbeitung heißt, dass jedes Gerät gleichen,
abgestimmten Klang bekommt, live im Dashboard änderbar — und nichts davon
kostet den Dot CPU, was auf Hardware zählt, die schon eine Mikrofonpipeline
und womöglich ein Wakeword-Modell betreibt.

Die Antwort wird **gestreamt, während Home Assistant sie noch erzeugt**: Sie
läuft durch ffmpeg und zum Dot hinaus, sowie sie ankommt, statt erst
vollständig geholt und dekodiert zu werden. Eine lange Antwort fängt etwa im
selben Moment an zu sprechen wie eine kurze, statt dich auf die Synthese des
letzten Worts warten zu lassen, bevor du das erste hörst. Alle drei Stufen
tragen ihren Zustand über die Stücke hinweg, es knackt also nicht an den
Nahtstellen.

**Haken:** Eine Antwort per Stimme zu unterbrechen (**Barge-in**)
funktioniert, wenn es aktiviert ist — sag das Wakeword darüber, und die
Antwort bricht ab —, aber es ist standardmäßig aus und hängt davon ab, dass
AEC an und abgestimmt ist (Stufe 3): Die Mikrofone bleiben während der
Wiedergabe scharf, und die Echo-Auslöschung ist das, was das Gerät davon
abhält, sich selbst zu wecken. Unterbrechen durch *bloßes Reden* (ohne
Wakeword) wird bewusst nicht versucht.

## Jenseits von Sprache — Musik

Jeder Echo erscheint in Home Assistant als **Media Player**, auf dem man
wirklich etwas abspielen kann: `media_player.play_media`, der HA-Medienbrowser,
Music Assistant, Radiostreams. Der Controller dekodiert mit ffmpeg, was immer
du ihm hinwirfst, und streamt es zum Lautsprecher, dabei ein paar Sekunden
voraus, damit ein WLAN-Schluckauf keine hörbare Lücke wird. Pause und Stopp
sind trotzdem sofort — sie warten nicht, bis dieser Puffer leerläuft, sie
werfen ihn weg.

Das Wakeword über Musik zu sagen macht sie **leiser** (Ducking): Die Musik
sinkt zu einem leisen Bett unter der Antwort und kommt danach wieder hoch. Sie
pausiert nicht. Das ist wichtig, weil diese paar Sekunden Vorlauf beim
Sprechen schon im Dot liegen, das Absenken also auf dem Gerät passieren muss
— und weil sich ein Flow-Stream von Music Assistant nicht spulen lässt, eine
Pause dich also früher so viel kostete, wie das Gespräch dauerte, und dich
mitunter im nächsten Titel absetzte. Die Stimme selbst wird nie leiser
gedreht, nur das Bett darunter. Wie weit es absinkt, entscheidest du
(**Ducking**, im Abschnitt „Playback") — eine Geschmacksfrage, die man am
besten im echten Raum nach Gehör trifft.

Damit das Aufwecken über Musik zuverlässig klappt, aktiviere AEC und Barge-in
(Stufe 3): Dieselbe Echo-Auslöschung, mit der du die eigene Stimme des
Assistenten unterbrechen kannst, lässt ihn dich über einem Lied hören.

Ältere Firmware, die die beiden Ströme nicht mischen kann, fällt auf das
frühere Verhalten zurück — Pause fürs Gespräch, danach weiter.

---

## Entwurfsprinzipien, falls du dich fragst „warum ist das so?"

1. **Einfaches Gerät, kluger Controller.** Alles, was driften, falsch
   einschätzen oder abgestimmt werden muss, lebt dort, wo es beobachtet und
   aktualisiert werden kann, ohne Hardware anzufassen. Der Dot nimmt auf,
   verstärkt, löscht sein eigenes Echo und streamt — das war's.
2. **Messen, nicht verändern.** Der Controller verfolgt den
   Grundgeräuschpegel jedes Raums und nutzt ihn für *Entscheidungen* (spricht
   jemand?), schreibt den Ton auf dem Weg zur Spracherkennung aber nie um.
   Adaptives Am-Audio-Herumdrehen ist die Quelle der schlimmsten Fehler in der
   Geschichte dieses Systems.
3. **Langweilig und durchgehend schlägt klug und gegattert.** Der immer
   laufende, unbearbeitete Wakeword-Strom ersetzte einen klügeren Entwurf, der
   über Tage schlechter wurde. Im Zweifel wählt die Pipeline die
   vorhersehbare Option.

## Wie ein gesundes Gespräch aussieht

Wenn sich dein Echo langsam anfühlt, ist die nützliche Frage, *welche Stufe*
langsam ist — die Antwort schickt dich jedes Mal zu einer völlig anderen
Komponente. Diese Werte sind auf dem Referenzaufbau unten gemessen, du hast
also etwas zum Vergleichen statt eines Gefühls.

Ein Befehl „mach das Bürolicht an", Ende zu Ende:

| Stufe | Was es ist | Referenz |
|---|---|---|
| Aufwachen → erstes Frame | der Echo beginnt zu streamen | **3 ms** |
| Sprechen | du redest, bis HAs Erkennung sagt, du hast aufgehört | 3,1 s |
| **Spracherkennung** | Whisper macht aus Audio Text | **1,8 s** |
| **Absicht** | Home Assistant entscheidet, was du meintest, und erzeugt Sprache | **0,04 s** |
| Abholen | die gesprochene Antwort herunterladen | 0,4 s |
| Wiedergabe | die Antwort, gesprochen | 2,2 s |
| **gesamt** | Wakeword bis fertig | **7,1 s** |

Das meiste davon bist du beim Sprechen und der Assistent beim Antworten. Der
Teil, den ein langsames System aufbläht, ist die **Spracherkennung**, und sie
ist die Stufe, die am empfindlichsten darauf reagiert, wie viel CPU die
Maschine hat: Auf zwei geteilten Kernen brauchten dieselben Befehle **4,8 s im
Median und bis zu 20,5 s**, gegen 2,1 s im Median und eine Spanne von 1,6–2,6 s
auf vier Kernen. Sonst hat sich in der Pipeline nichts geändert.

Eine lokale Absicht wie eine Lampe oder ein Timer löst sich in
**Millisekunden** auf. Wenn deine *Absichts*-Stufe Sekunden statt
Millisekunden braucht, leitest du wahrscheinlich über einen Konversationsagenten
(ein LLM) statt über die eingebauten Absichten von Home Assistant — das ist
eine Entscheidung und kein Fehler, aber es lohnt zu wissen, welche du
getroffen hast.

Zwei Dinge, die man vor dem Vergleichen wissen sollte:

- **Die erste Anfrage nach einem Neustart ist immer langsam**, weil das
  Sprachmodell erst bei Bedarf geladen wird. Verwirf sie.
- **Diese Zahlen sind eine Referenz, kein Ziel.** Eine langsamere Maschine ist
  nicht kaputt. Der Punkt ist, „meine Spracherkennung braucht 15 Sekunden" von
  „mein Echo antwortet nicht" unterscheiden zu können, denn nur eines davon
  hat mit Revoice zu tun.

### Der Referenzaufbau

| | |
|---|---|
| Home Assistant | OS 18.2, Core 2026.8.3, in einer VM |
| CPU / RAM | 4 vCPU, 8 GB |
| Spracherkennung | Whisper-Add-on, `faster-whisper`, Modell `auto` |
| Sprachausgabe | Piper |
| Revoice-Controller | Home-Assistant-Add-on |
| Geräte | 2 × Echo Dot 2. Generation, Wakeword auf dem Gerät, 2,4-GHz-WLAN |

Home Assistant, Whisper, Piper, Music Assistant und der Revoice-Controller
teilen sich diese vier Kerne. Die Spracherkennung ist davon mit weitem Abstand
die hungrigste — wenn du weitere Add-ons auf derselben Kiste betreibst, ist
sie diejenige, die es zu spüren bekommt.
