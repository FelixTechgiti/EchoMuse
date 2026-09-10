# Revoice-Dokumentation

Dokumentation für Anwenderinnen und Anwender, geschrieben zum Lesen ohne
Technikstudium. Gedacht als Keimzelle eines späteren Wikis — Screenshots und
Durchläufe sind willkommen.

| Dokument | Worum es geht |
|---|---|
| [Schnellstart](quickstart.md) | Von null bis zum Gespräch mit deinem Dot: Controller installieren, Ersteinrichtung, Gerät freigeben, Home Assistant anbinden, Alltag. |
| [Konfigurationsleitfaden](configuration.md) | Jede Einstellung im Dashboard verständlich erklärt — was sie tut, wann man sie anfasst und wie man sie abstimmt. Am Ende steht [was dein Netzwerk verlässt](configuration.md#was-dein-netzwerk-verlässt) — es gibt keine Telemetrie, und die eine ausgehende Verbindung wird beim Namen genannt. |
| [Die Sprachpipeline, erklärt](voice-pipeline.md) | Wie deine Stimme von den Mikrofonen zu Home Assistant und zurück reist, Stufe für Stufe, mit Nutzen und Haken jeder Entwurfsentscheidung. |
| [FAQ](faq.md) | Kurze Antworten und Umgehungen für das, was am häufigsten aufkommt — verweigertes Rooting, gescheiterte Assistentenschritte, Update-Probleme, Wakeword-Abstimmung, Privatsphäre. Vor einem Issue hier nachsehen. |
| [Abnahmetests](uat.md) | Eine Checkliste, um zu bestätigen, dass Revoice auf deiner Hardware tut, was es verspricht — und wie du meldest, was nicht funktioniert. Enthält die bekannten Fehler, die kein weiteres Issue brauchen. |
| [Umzug auf das Home-Assistant-Add-on](migrate-to-addon.md) | Eine bestehende Docker-Installation aufs Add-on umziehen, ohne Geräte, Einstellungen oder Home-Assistant-Entitäten zu verlieren. Lies den Teil über `tls/`, bevor du anfängst. |

Tiefere technische Referenzen liegen anderswo:

- [support-bundle.md](support-bundle.md) — was in einem Support-Bundle steckt,
  was bewusst fehlt, und wie du es prüfst, bevor du es weitergibst.
- [rooting.md](rooting.md) — was ein Gerät braucht, bevor Revoice es benutzen
  kann. Der Exploit selbst ist R0rt1z2s Arbeit im XDA-Forum, und jener Thread
  ist maßgeblich; hier steht, wo Revoice übernimmt und was der Assistent für
  dich erledigt.
- [agent-access.md](agent-access.md) — wie eine Automation den Controller
  durch Home Assistant hindurch ansteuert: Ingress-Authentifizierung, was
  Lesezugriff schon kann, was Adminrechte braucht, und der eine Schritt, den
  ein Mensch von Hand machen muss.
- [device-controller-interface.md](device-controller-interface.md) — der
  Wire-Contract, den ein Geräte-Binary umsetzt: die drei WebSocket-Ebenen,
  Fähigkeitsaushandlung, `/control`-Nachrichten, `/data`-Frames, Config-Push,
  Link-Authentifizierung und das Board-Profil `crown`. Vor dem Bau von
  Bindings für ein neues Board lesen.
- [audio-states.md](audio-states.md) — wem der Lautsprecher gehört und was auf
  der Leitung liegt: die zwei Audio-Ebenen, Ducking, Flush-Semantik und die
  offenen Fragen, wie Sprache, Musik, Durchsagen und Wecker zusammenspielen.
- [led-ring-states.md](led-ring-states.md) — das Zustandsmodell des Rings:
  Besitzer-Priorität, Verbindungsverfügbarkeit und die Ereignistabellen für
  Tasten und Audio.
- [SETUP.md](../SETUP.md) — Architekturreferenz: wie das Mikrofonarray, die
  Audiopipeline und das Geräte/Controller-Protokoll tatsächlich arbeiten,
  dazu Fehlersuche. Keine Einstiegsanleitung.
- [JOURNAL.md](../JOURNAL.md) — das Entwicklungstagebuch: eine lange,
  chronologische Aufzeichnung dessen, was gebaut wurde, was kaputtging und was
  wir falsch gemacht haben. Auf Englisch.
- [CLAUDE.md](../CLAUDE.md) — Orientierung im Code für Entwickelnde (und
  KI-Assistenten). Auf Englisch.
