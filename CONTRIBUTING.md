# Mitmachen

*English readers: this is a fork of [wilbowes/EchoMuse](https://github.com/wilbowes/EchoMuse).
If your bug is not fork-specific, please report it upstream — it reaches more
people there. Issues and pull requests on this fork are written in English; the
user-facing documentation is German.*

Revoice ist ein Fork von
[wilbowes/EchoMuse](https://github.com/wilbowes/EchoMuse). Die verbindlichen
Arbeitsregeln stehen in **[CLAUDE.md](CLAUDE.md)** — die liest Claude Code in
jeder Sitzung automatisch. Diese Seite ist die Kurzfassung für Menschen.

## Was hierher gehört und was nach Upstream

**Ein Fehler, der nicht fork-spezifisch ist, gehört nach Upstream.** Dort sitzen
mehr Geräte, mehr Nutzer und die Entwicklung, der dieser Fork folgt; ein Bericht
hier erreicht eine Person. Fork-spezifisch ist alles, was nur hier existiert:

- der Name **Revoice** und was er anfasst — Pfade unter
  `/data/local/etc/revoice`, `revoice.db`, das Image
  `ghcr.io/felixtechgiti/revoice-controller`, der TLS-Name `revoice-controller`
- die Ausgabekette **auf dem Gerät** (`device/internal/outchain`, Capability
  `output_chain`) — Upstream hat sie nicht und lässt den Controller formen
- **emOS**, AirPlay 2 und Spotify Connect auf dem Gerät
- alles rund um `-fx.N`-Versionen und `cut-release.yml`
- die deutsche Dokumentation

**Das Wertvollste, was du schicken kannst, ist ein Fehlerbericht mit
Support-Bundle** (Dashboard → Support → Bundle herunterladen). Es ist eine
Allowlist — keine Transkripte, keine Aufnahmen, keine Netzwerk- oder
Kontonamen — und enthält Controller-Log, Gerätelogs, Versionen, Konfiguration
und Link-Metriken. Eine Ferndiagnose kostete ohne so ein Bundle Tage pro Runde.

Hardware-Befunde sind das Zweitwertvollste: Die meisten von uns haben eine Art
Echo in einer Art Raum. Verhält sich dein Gerät anders, ist das ein eigenes
Issue wert.

## Bevor du anfängst

**Anmelden, bevor Code entsteht** — an diesem Repo können mehrere Sitzungen
parallel arbeiten, und doppelt gebaute Arbeit muss weg:

```bash
gh issue view <nr>            # ist schon jemand dran?
gh pr list --state open       # gibt es einen offenen PR dazu?
gh issue edit <nr> --add-assignee @me
gh issue comment <nr> --body-file /tmp/kommentar.md
```

Der **Kommentar** ist der wichtige Teil; die Zuweisung allein benachrichtigt
niemanden. Für alles, was größer ist als ein Einzeiler und noch kein Issue hat:
vorher eines anlegen.

**`--body-file` statt `--body`, und `-F` statt `-m`.** Issue-Texte und
Commit-Nachrichten dieses Projekts bestehen fast nur aus Symbolen, Pfaden und
Flags in Backticks — und überall dort, wo die Shell den Inhalt ersetzt, führt
sie das zwischen Backticks als Befehl aus. Der Aufruf meldet dabei Erfolg; der
Schaden steht mitten im Text. Die Fälle, die es schon gekostet hat, stehen in
[CLAUDE.md](CLAUDE.md), §2.

## Vor einem Pull Request

```bash
git fetch origin                            # vor JEDEM Push
cd controller && python -m pytest tests/    # braucht pytest numpy scipy pyyaml
cd device && go test ./... && go vet ./...
```

Beides läuft bei jedem Push in CI (`.github/workflows/ci.yml`).

**Bitte einen Test dazu, wenn deine Änderung reine Logik ist.** Die
Controller-Suite kann `em_controller` und `em_esphome` absichtlich nicht
importieren (die ziehen aiohttp, zeroconf und openwakeword herein), also werden
Entscheidungen, die Abdeckung brauchen, in ein eigenes Modul gezogen — siehe
`em_button`, `em_linkauth`, `em_turnclock`, `em_barge`. Diesem Muster zu folgen
ist der einfachste Weg zu einer schnellen Review.

Hardwareabhängiger Code ist auf dem Rechner nicht testbar, und niemand erwartet,
dass du das fälschst. Schreib dazu, worauf du es geprüft hast.

**Ein PR, der ein Issue erledigt, schließt es**: `Closes #nnn` im Rumpf, nicht
„Relates to #nnn" — sonst steht ein längst behobener Fehler weiter als offen und
wird als nächstes priorisiert.

**Keine Claude-Urheberschaft**, in keinem Feld: nicht im Commit-Autor, nicht als
Trailer, nicht als Autor von Issue, PR oder Kommentar. Begründung und die
Selbstprüfungen dafür stehen in [CLAUDE.md](CLAUDE.md), §5.

## Wenn du etwas als fertig meldest

Schreib dazu, **wie** du es geprüft hast — mit einer dieser drei Formeln, damit
sie durchsuchbar bleiben:

| Formel | heißt |
|---|---|
| am echten Gerät verifiziert | auf einem gerooteten Echo durchgespielt |
| in CI verifiziert | `go test` / `pytest` / `go vet`, keine Hardware |
| nicht verifiziert | nur gebaut, sonst nichts |

Die dritte wegzulassen, wenn sie zutrifft, ist der schlimmste Fall: Der nächste
verlässt sich dann auf etwas, das nie geprüft wurde.

## A few rules that will bite you

Diese fünf sind technisch und gelten hier wie bei Upstream — jede hat schon
jemandem einen schlechten Tag gekostet. Sie stehen auf Englisch, weil sie Code
beschreiben, der englisch ist.

- **`em_db.MIGRATIONS` is append-only.** The stored `schema_version` is an
  index into that list, so editing a deployed entry corrupts every database
  that already ran it. Add a new entry.
- **Negotiate by capability, not by version.** Devices announce what they
  implement; the controller reads `Device.capabilities`. Comparing version
  strings puts release history in the controller and misjudges dev builds.
- **Degrade to the old behaviour, never to a wrong answer.** A measurement
  that is absent stores as NULL, not 0 — "we did not measure this" and "this
  was zero" are different facts and the stats are read as if they are true.
- **Home Assistant entity keys are append-only.** HA keys its registry on
  them, so renumbering renames everyone's entities and breaks their
  automations.
- **Anything in `controller/device_payloads/` needs an update path**, and a
  test enforces it. A payload with no way to reach fielded devices means every
  user updates it by hand.

Stößt eine Änderung dagegen und es gibt wirklich keinen Weg daran vorbei, sag es
im PR — das ist eine Diskussion, keine Ablehnung.

## Generiertes und Releases

`controller-ea/` wird von `controller/tools/sync_channels.py` **erzeugt**. Nicht
von Hand ändern; ein Test fällt bei Abweichung um.

Versionen dieses Forks tragen ein `-fx.N` (`controller-v2.42.0-fx.1`,
`v2.15.0-fx.1`), weil Upstreams Tags durch den Sync hier landen und ein
gleichnamiger Tag beim nächsten Fetch kollidiert. Getaggt wird über
`.github/workflows/cut-release.yml`, nicht von Hand — die vollständige Prozedur
samt der Fallen, die sie gekostet hat, steht in [CLAUDE.md](CLAUDE.md) unter
„Releasing on this fork". Der Versionspin in `controller/config.yaml` gehört in
einen eigenen Release-PR: Supervisor liest ihn vom Standard-Branch, also ist er
in der Sekunde live, in der er merged — und bis der Tag gecuttet ist, kann das
Image noch nicht existieren.

## Lizenz

Mit einem Beitrag stimmst du zu, dass deine Arbeit unter der MIT-Lizenz in
[LICENSE](LICENSE) steht. Wer eine Fremdkomponente hinzufügt, trägt sie in
[NOTICE.md](NOTICE.md) ein und legt ihren Lizenztext neben den Code.
