# Support-Bundles

Wenn sich etwas seltsam verhält und der Grund nicht offensichtlich ist,
sammelt ein Support-Bundle die Diagnosen in einer Datei, die du an ein
[GitHub-Issue](https://github.com/wilbowes/EchoMuse/issues) hängen kannst.

**Settings → Support → Collect bundle**, dann Herunterladen — oder
`GET /api/support/bundle`, wenn dir die API lieber ist. Nur für
Administratoren.

Es ist eine schlichte JSON-Datei. **Öffne sie, bevor du sie verschickst** —
sie ist lesbar, und du solltest dich selbst vom Inhalt überzeugen können,
statt dieser Seite zu glauben.

## Was bewusst nicht darin steht

Ein Bundle ist dafür gedacht, an ein öffentliches Issue gehängt zu werden —
alles Private darin ist also in dem Moment öffentlich, in dem du es
verschickst, und zwar dauerhaft. Es ist als **Positivliste** gebaut: Jedes
Feld wird einzeln benannt, alles andere fällt weg. Eine neue Spalte in der
Datenbank ist ausgeschlossen, bis jemand sie bewusst aufnimmt — der
Fehlerfall ist, dass dem Support ein Feld fehlt, nie dass deine Daten
abfließen.

Ausgeschlossen, ohne Möglichkeit, sie aufzunehmen:

| Nicht enthalten | Warum |
|---|---|
| **Alles, was du gesagt hast** — Transkripte, `stt_text`, gespeicherter Ton | Sprache aus dem Inneren deines Hauses. Es gibt keinen Opt-in-Schalter, denn ein Schalter ist etwas, das Leute anklicken, und dieser lässt sich nicht mehr zurücknehmen, sobald die Datei öffentlich ist. |
| **Gerätebezeichnungen** | Die hast du geschrieben, und sie enthalten regelmäßig Namen — „Schlafzimmer – Sam" ist ein echtes Beispiel. Ersetzt durch `device-1`, `device-2` … |
| **Netzwerkkennungen** — WLAN-SSID, BSSID, IP-Adressen | Eine SSID lässt sich über öffentliche Wardriving-Datenbanken geolokalisieren; sie zu veröffentlichen verrät also ungefähr, wo du wohnst. |
| **Zugangsdaten** — Geräte-Token, ESPHome-PSKs, Passwort-Hashes, Anmeldesitzungen | Offensichtlich, aber ausgeschrieben, damit es prüfbar ist. |
| **Kontonamen des Dashboards** | Sie tauchen in Logzeilen wie „Shell session opened by …" auf, was sonst nichts hier abgefangen hätte. Ersetzt durch die Rolle des Kontos — `<admin>` —, denn das ist der Teil, der interessiert. |
| **Dateipfade** | Ein Datenverzeichnis heißt auf einer Bare-Metal-Installation `/home/<dein Name>/…`, deshalb meldet der Controller nur Größen. |
| **URLs und Zeichenketten in Anführungszeichen in Logzeilen** | Medien-URLs tragen Anbieterpfade und Sitzungstoken; Zeichenketten in Gesprächsspuren tragen Transkripte. |

Logzeilen aus Quellen, von denen bekannt ist, dass sie Sprache tragen, werden
**vollständig** verworfen statt bearbeitet — eine Zeile, die ein Transkript
zitiert, nur teilweise zu schwärzen wäre eine Wette auf einen regulären
Ausdruck.

## Was darin steht, und warum sich jeder Teil seinen Platz verdient

| Enthalten | Warum es gebraucht wird |
|---|---|
| Controller-Version, Schemaversion | Fast jede „ist das behoben?"-Frage beginnt hier. |
| Geräte-Seriennummern | Ohne sie korreliert nichts. Sie identifizieren deine Hardware für dich; darüber hinaus sagen sie nichts aus. |
| Firmware-Version, Rückfall-Slot, Freigabestatus | Sagt uns, ob eine Behebung auf dem Gerät überhaupt vorhanden ist. |
| **Fähigkeiten** (`mic`, `oww_shadow`, `ambient_light` …) | Entscheidet, welche Home-Assistant-Entitäten überhaupt existieren. „Der Lichtsensor tauchte nicht auf" wird hier in einer Zeile beantwortet. |
| Konfiguration — Schwellwerte, EQ, LED-Szenen, Wake-Modell | Verhalten, nicht Identität. Schlüssel, deren *Name* nach Zugangsdaten aussieht, werden ohnehin geschwärzt. |
| Gesprächs-Metadaten — Ausgang, Wake-Wert, Stufenlatenzen, Aussetzer | Was passiert ist und wie lange jede Stufe brauchte. Keine Worte, nur Zeiten und Ergebnisse. |
| Stündliche Messwerte pro Gerät — CPU, Speicher, Ablage, Temperatur, RSSI, RTT | Trends. Die Signalstärke ist dabei, der Name des Netzwerks nicht. |
| Die CPU des Controllers selbst (1 min / 5 min / 1 h), Speicher, Ablage, Laufzeit | Ein Gerät, dem Audio ausgeht, kann ein Host sein, dem CPU, Speicher oder Platte ausgehen. Die drei Zeitfenster trennen „gerade jetzt beschäftigt" von „vorhin beschäftigt", was unterschiedliche Antworten braucht. Nur Größen und Anzahlen, nie Pfade. |
| Wake-Zähler — Beinahe-Treffer, Verwürfe auf dem Gerät, Inferenzzeiten | Wakeword-Verhalten ganz ohne Ton. |
| Jüngste Logzeilen des Controllers, bereinigt | Was der Controller selbst getan hat — der Teil, der die meisten „er hat das Falsche gemacht"-Meldungen erklärt. Zitierter Text und URLs entfernt. |
| Jüngste Logzeilen je Gerät, bereinigt | Was jedes Gerät gemeldet hat. Sich wiederholende Speicherauszüge werden ausgedünnt, damit sie den Rest nicht verdrängen. |

Grob die letzten 24 Stunden, pro Gerät gedeckelt.

## Es durchsehen, bevor du es verschickst

Öffne die Datei und sieh dir den Anfang an: `redaction` benennt die Zusage,
und `devices[].name` sollte `device-1` lauten und nicht deine Raumnamen. Eine
Suche nach deinem WLAN-Namen oder nach etwas, das du gesagt hast, sollte leer
ausgehen.

Wenn du darin etwas findest, das du lieber nicht veröffentlichen würdest,
**ist das ein Fehler, und wir wollen davon hören** — bitte melde ihn privat
statt in einem öffentlichen Issue.

## Wenn stattdessen ein Einrichtungsschritt gescheitert ist

Ein Support-Bundle beschreibt ein Gerät, das der Controller bereits kennt.
Ein Dot mitten im Einrichtungsassistenten ist keines davon, also sammelt der
Assistent seine eigene Datei.

Wenn ein Schritt scheitert, liest er den Gerätezustand genau dann aus, und
neben dem Fehler erscheint eine Schaltfläche **Diagnose herunterladen**. Das
Sammeln passiert automatisch, denn bis man dich gebeten hätte, `getprop` von
Hand auszuführen, ist das Gerät meist erneut versucht oder neu gestartet
worden und der gescheiterte Zustand weg. Ob du sie teilst, bleibt deine
Entscheidung.

Sie enthält, was ein gescheiterter Schritt zur Erklärung braucht: welcher
Schritt, der Fehler, Build und Modell, ob Root und Paketverwaltung geantwortet
haben, freier Speicher und was das Funkmodul sieht. Es gelten dieselben Regeln
wie beim Bundle: keine Sprache, keine Netzwerknamen, keine Adressen.
WLAN-Scanergebnisse behalten Sicherheitsmerkmale und Frequenzen — den Teil,
der ein Scheitern erklärt —, während die Netzwerknamen durch `network-1`,
`network-2` und so weiter ersetzt werden und dasjenige, dem du beitreten
wolltest, markiert ist.

Derselbe Rat: Öffne sie, bevor du sie verschickst.

## Aufbewahrung

Das Bundle ist eine Datei auf deinem Rechner. Durch das Erzeugen wird nichts
irgendwohin hochgeladen; es landet dort, wo du es hinlegst, und Löschen
genügt. Erzeuge lieber ein neues, statt alte aufzuheben — sie sind nur neben
einem aktuellen Problem nützlich.
