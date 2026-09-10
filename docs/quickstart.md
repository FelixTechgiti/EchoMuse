# Revoice-Schnellstart

Revoice macht aus einem Amazon Echo Dot (2. Generation) einen **vollständig
lokalen Sprachassistenten** — ohne Amazon-Konto, ohne Cloud, ohne Ton, der
dein Haus verlässt. Der Dot wird zum „Satelliten": Seine Mikrofone und sein
Lautsprecher werden von einem kleinen Server gesteuert (dem **Controller**),
der auf einem Rechner in deinem Netzwerk läuft und seinerseits mit Home
Assistant spricht — dort passiert das eigentliche „Mach das Licht an".

Diese Anleitung bringt dich von null bis zum Gespräch mit deinem Dot.
Programmierkenntnisse brauchst du nicht — wo etwas wirklich Technisches
unvermeidlich ist (das einmalige Rooten des Dots), verweisen wir auf die
ausführliche Anleitung, statt so zu tun, als sei es einfach.

---

## Was du brauchst

| Ding | Wofür |
|---|---|
| Amazon Echo Dot 2. Generation („biscuit") | Die Hardware, die umgewidmet wird. Gebraucht ist sie günstig. |
| Ein Rechner, der durchläuft (Heimserver, NAS, Raspberry-Pi-Klasse oder besser) | Betreibt den Controller. Docker empfohlen. |
| Home Assistant | Erledigt die eigentliche Assistenzarbeit: Spracherkennung, Verstehen, Sprachausgabe. Eine funktionierende [Assist-Pipeline](https://www.home-assistant.io/voice_control/) muss bereits eingerichtet sein. |
| Einmalig ein USB-Kabel und ein Laptop | Zum einmaligen Entsperren und Flashen des Dots. |

## Schritt 1 — Den Dot rooten (einmalig, pro Gerät)

Der Dot kommt fest an Amazons Software gebunden. Ihn zu entsperren heißt,
veränderte Firmware über USB zu flashen — das ist der einzige wirklich
fummelige Teil des Projekts, dauert beim ersten Mal etwa eine Stunde und ist
Schritt für Schritt in [rooting.md](rooting.md) dokumentiert, das für den
Exploit selbst auf R0rt1z2s Thread im XDA-Forum verweist.

Die gute Nachricht: Das Dashboard hat einen **Einrichtungsassistenten** (Dot
in den USB-Port des Laptops stecken, Dashboard in Chrome öffnen, den
Schritten folgen), der nach dem ersten Entsperren fast alles automatisiert.

Scheitert ein Schritt, bietet der Assistent **Diagnose herunterladen** an —
eine Datei zum Anhängen an ein Issue. Sie hält den Gerätezustand genau im
Moment des Fehlers fest und spart die Rückfragerunde, in der du sonst Dinge
von Hand ausführen sollst. Wenn du das Gerät zwischendurch abziehst, kannst
du weitermachen: **Neu verbinden** steht auf jedem Schritt zur Verfügung.
Beachte, dass das Kabel die einzige Stromquelle des Dots ist — Abziehen
startet ihn also neu, und er kommt in Android zurück. Der Assistent sagt es
dir, wenn der Schritt, auf dem du bist, den Recovery-Modus brauchte.

Das machst du pro Gerät genau einmal. Alles danach — Updates, Konfiguration,
sogar ein Terminal — läuft per WLAN über das Dashboard.

## Schritt 2 — Den Controller starten

Zwei Wege, beide mit derselben Software. Wenn du Home Assistant schon hast,
ist das Add-on weniger Arbeit; sonst nimm Docker auf einem Rechner, der
durchläuft.

<details open>
<summary><b>Als Home-Assistant-Add-on</b></summary>

Einstellungen → Add-ons → Add-on-Store → ⋮ → **Repositories**,
`https://github.com/FelixTechgiti/Revoice` einfügen, dann **Revoice** aus dem
Store installieren. Die README hat ein Badge, das das Repository mit einem
Klick hinzufügt.

Das Dashboard erscheint als **Panel in der Seitenleiste** — es wird durch
Home Assistant hindurch erreicht, also gibt es keinen zusätzlichen Port zu
öffnen und keine zweite Adresse zu merken. Es ist *ausschließlich* so
erreichbar: Das Add-on weist Verbindungen ab, die nicht über Home Assistant
kommen.

Einstellungen, die sonst in `.env` stünden, sind stattdessen Add-on-Optionen
(Reiter „Konfiguration"). Lass `server_ip` leer, außer der Controller wählt
die falsche Adresse — siehe [Wenn etwas nicht funktioniert](#wenn-etwas-nicht-funktioniert).

Deine Daten liegen im eigenen Speicher des Add-ons und überstehen Updates.

**Wenn du Geräte von einem früheren Controller hast**, tragen sie dessen
Zertifizierungsstelle und weigern sich, einer neuen zu vertrauen. Kopiere das
alte Verzeichnis `data/tls/` (alle vier Dateien) in das Datenverzeichnis des
Add-ons, bevor du sie verbindest — sonst kommen sie überhaupt nicht durch.

</details>

<details>
<summary><b>Mit Docker, auf einem beliebigen durchlaufenden Rechner</b></summary>

Mit dem fertigen Image (nichts zu kompilieren):

```bash
mkdir revoice && cd revoice
curl -O https://raw.githubusercontent.com/FelixTechgiti/Revoice/main/controller/docker-compose.deploy.yml
curl -o .env https://raw.githubusercontent.com/FelixTechgiti/Revoice/main/controller/.env.example
# Optional: SERVER_IP in .env auf die LAN-IP dieses Rechners setzen. Leer
# gelassen wird sie erkannt, und die benutzte Adresse steht im Startlog.
docker compose -f docker-compose.deploy.yml up -d
```

Später aktualisieren: `docker compose -f docker-compose.deploy.yml pull && docker compose -f docker-compose.deploy.yml up -d`. Deine Geräte, Benutzer und Einstellungen liegen in `./data` und überstehen Updates.

<details>
<summary>Alternative: aus dem Quelltext bauen (nötig für Wakeword-Inferenz auf NVIDIA-GPU)</summary>

```bash
git clone https://github.com/FelixTechgiti/Revoice.git
cd Revoice/controller
cp .env.example .env
# Optional: SERVER_IP auf die LAN-IP dieses Rechners setzen. Leer gelassen wird sie erkannt.
docker compose up -d --build
```

Hinweis: `docker-compose.yml` fordert eine NVIDIA-GPU an (für
onnxruntime-gpu). Auf einem Rechner ohne GPU entferne den `deploy:`-Block und
das Build-Argument `GPU: "1"` — oder nimm einfach das fertige Image oben, das
reine CPU-Version ist.

</details>

</details>

Das war's. Der Controller betreibt jetzt zwei Dinge:

- ein **Dashboard** unter `http://<SERVER_IP>:8768` — deine Schaltzentrale
- einen Dienst, den die Dots in deinem Netzwerk von allein finden (auf der
  Geräteseite ist keine IP zu konfigurieren)

## Schritt 3 — Dein Administratorkonto anlegen

**Beim Home-Assistant-Add-on** gibt es nichts anzulegen. Öffne das
**Revoice-Panel** in der Seitenleiste, und du bist bereits als dein
Home-Assistant-Benutzer angemeldet — Home Assistant hat dich authentifiziert,
ein zweites Passwort wäre ein Schloss an einer bereits verschlossenen Tür.
Wer das Panel als Erstes öffnet, wird Revoice-Administrator; alle danach
bekommen Lesezugriff, bis ein Administrator sie unter **Einstellungen →
Benutzer** hochstuft.

Die Rollen gehören Revoice und werden **nicht** aus Home Assistant übernommen
— HA-Administrator zu sein macht dich nicht zum Revoice-Administrator.
Lesezugriff ist eine echte Einschränkung und keine Formalie: Aufnahmen und
der Transkripttext eines Gesprächs sind Administratoren vorbehalten, denn
dieses Dashboard zu erreichen ist nicht dasselbe, wie Sprache aus dem Inneren
des Hauses anvertraut zu bekommen.

**Mit Docker** öffnest du `http://<SERVER_IP>:8768`. Bei einer frischen
Installation siehst du die Echo-Grafik mit einem **pulsierenden bernsteinfarbenen
Ring** und ein Einrichtungsformular.

Es fragt nach einem **Setup-Token** — einem Einmalcode, der in den Logs des
Controllers steht, damit nur du (die Person, die die Serverlogs lesen kann)
den Controller übernehmen kannst:

```bash
docker logs revoice-controller
```

Suche den umrahmten Token nahe dem Anfang, füge ihn ein, wähle Benutzername
und Passwort — fertig. Ab da zeigt die Seite einen **grünen Ring** und eine
normale Anmeldung.

## Schritt 4 — Dein Gerät freigeben

Wenn ein gerooteter Dot startet, findet er den Controller selbst und bittet
um Aufnahme. Neue Geräte erscheinen im Dashboard als **ausstehend** — nichts
funktioniert, bis du ihnen einen Namen gibst und auf **Freigeben & zur Flotte
hinzufügen** klickst. (Das ist Absicht: Nichts tritt deinem Sprachnetzwerk
bei, ohne dass du es sagst.)

Nach der Freigabe verbindet sich der Dot vollständig: Du siehst ihn als
**online**, mit Lautstärke, Einstellungen und Live-Status.

## Schritt 5 — Mit Home Assistant verbinden

Der Controller lässt jeden Dot wie einen **ESPHome-Sprachsatelliten**
aussehen — etwas, mit dem Home Assistant von Haus aus umgehen kann, ohne
Zusatz-Integrationen:

1. In Home Assistant: **Einstellungen → Geräte & Dienste → Integration
   hinzufügen → ESPHome**, dann die **IP des Controllers** und den **Port**
   des Geräts eintragen: 16001 für das erste Gerät, 16002 für das zweite und
   so weiter (der Port jedes Geräts steht auf seiner Dashboard-Seite). Ein
   Integrationseintrag pro Gerät.
2. Weise das neue Gerät deiner Assist-Pipeline zu (Einstellungen →
   Sprachassistenten).

Das Gerät erscheint in HA als **`<Name> Voice Assistant`** (z. B. „Wohnzimmer
Voice Assistant") mit dem Modell „Echo Dot Gen 2 (biscuit)" — der
Bluetooth-Proxy taucht, falls aktiviert, getrennt als `<Name> BT Proxy` auf.

> **Automatische Erkennung:** Läuft Home Assistant im **selben Subnetz** wie
> der Controller, sollten die Geräte auch von allein als erkannte
> „revoice-…"-Einträge auftauchen (behoben in v2.7.5 — frühere Versionen
> haben sich unvollständig angekündigt, und HA hat sie stillschweigend
> ignoriert, sodass nur die manuelle Eingabe blieb). Bereits von Hand
> hinzugefügte Geräte erscheinen nicht noch einmal als Fund — HA weiß, dass
> es sie hat. Liegt HA in einem **anderen Subnetz oder VLAN**, kann die
> Erkennung diese Grenze nicht überschreiten (sie nutzt rein lokales
> Multicast), und die manuelle Eingabe bleibt der normale Weg — immer noch
> eine einmalige Sache von 30 Sekunden pro Gerät.

## Schritt 6 — Mit ihm sprechen

Sag das Wakeword — standardmäßig **„Hey Rhasspy"**, im Dashboard änderbar,
siehe [configuration.md](configuration.md) — und sprich dann normal weiter:

> „Hey Rhasspy … mach das Küchenlicht aus."

Der LED-Ring sagt dir, was gerade passiert:

| Ring | Bedeutung |
|---|---|
| Aus | Ruhe, wartet auf das Wakeword |
| Grün | Wakeword gehört, nimmt deinen Befehl auf |
| Hellgrünes Segment | Aus welcher Richtung du seiner Meinung nach sprichst |
| Drehend | Denkt nach (Home Assistant verarbeitet) |
| Cyanfarbener Bogen | Lautstärke, 2 Sekunden lang nach einem Lautstärkedruck (auch mitten in einer Antwort) |
| Dauerhaft rot | Mikrofone stumm (die physische Mute-Taste — seit v2.7.4 auf Hardwareebene). Mute mitten im Gespräch bricht außerdem ab, was der Assistent gerade tat |

## Alltägliches

- **Updates**: Erscheint eine neue Revoice-Version, zeigt das Dashboard ein
  Update-Abzeichen — ein Klick aktualisiert das Gerät über WLAN. Die
  Versionshinweise stehen daneben, du kannst also lesen, was sich geändert
  hat, statt nach der Versionsnummer zu urteilen. Geht ein Update schief,
  fällt das Gerät automatisch auf seine vorherige Version zurück. **Alle
  ausrollen** aktualisiert die ganze Flotte auf einmal; das läuft im
  Hintergrund, du kannst den Dialog also schließen und ihn über die Pille in
  der Kopfzeile wieder öffnen, um den Fortschritt zu sehen (die Schaltfläche
  selbst tritt zur Seite, bis die Flotte durch ist).
- **Einstellungen**: Alles Einstellbare liegt im Dashboard, entweder
  flottenweit (Zahnradsymbol) oder pro Gerät. Siehe
  [configuration.md](configuration.md).
- **Terminal**: Jede Geräteseite hat ein vollwertiges Fernterminal (für
  Neugierige; du *brauchst* es nie).
- **Lautstärke**: Tasten am Dot, Schieberegler im Dashboard oder die
  Media-Player-Karte in Home Assistant — alle bleiben synchron.
- **Unterbrechen**: Mit aktiviertem Barge-in sagst du das Wakeword, während
  er spricht, und er hält an und hört zu. Die Mute-Taste schneidet ihn
  ebenfalls sofort ab (und schaltet stumm).
- **Bluetooth-Proxy** (optional): Jeder Dot kann zusätzlich als
  Home-Assistant-Bluetooth-Proxy dienen — er nimmt passiv
  BLE-Advertisements auf (Anwesenheits-Beacons, BLE-Sensoren) und reicht sie
  als *eigenes* ESPHome-Gerät an HA weiter, unabhängig vom Sprachassistenten.
  Aktivierbar pro Gerät im Reiter „Config" (Abschnitt Bluetooth); in HA
  erscheint er als „<Name> BT Proxy". Siehe
  [configuration.md](configuration.md).

## Wenn etwas nicht funktioniert

1. Ist das Gerät im Dashboard **online**?
2. Kommt das Wakeword an? Der Reiter „Activity" zeigt die letzten
   Wakeword-Treffer und „Beinahe-Treffer" (Momente, in denen es fast
   ausgelöst hätte) — wenn du Beinahe-Treffer siehst, schiebe die
   Empfindlichkeit eine Stufe hoch (siehe configuration.md).
3. Schlechte Transkriptionen? Siehe den Mikrofonabschnitt in
   [voice-pipeline.md](voice-pipeline.md) — Raumgeräusche und Abstand zum
   Sprecher sind die üblichen Verdächtigen. Um das Raten zu beenden, schalte
   **Save utterances** ein (Config → Microphones → Advanced) und *hör dir an*,
   was der Dot gehört hat — der Reiter „Activity" bekommt dann eine
   Wiedergabetaste an jedem Gespräch. Standardmäßig aus, weil dabei Sprache
   auf deinem Server gespeichert wird; was genau aufbewahrt wird, steht in
   [configuration.md](configuration.md).
4. Der Abschnitt zur Fehlersuche in [SETUP.md](../SETUP.md) deckt die
   tieferen Dinge ab.
5. Immer noch fest, und du möchtest fragen? Der Reiter **Support** im
   Dashboard lädt eine einzelne Diagnosedatei herunter, die du an ein
   GitHub-Issue hängen kannst — Versionen, Gerätezustand, jüngste Logs und
   die Zustellstatistiken, die Audioprobleme aus der Ferne diagnostizierbar
   machen. Sie ist als Positivliste gebaut: keine Transkripte, keine
   Aufnahmen, keine Netzwerknamen, keine Kontonamen, und Gerätebezeichnungen
   werden durch Pseudonyme ersetzt. [support-bundle.md](support-bundle.md)
   listet genau auf, was darin steht, damit du vor dem Teilen nachsehen
   kannst.
