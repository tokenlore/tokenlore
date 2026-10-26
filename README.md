# tokenlore

tokenlore ist ein Plugin für Claude Code. Es liest die Nutzungszahlen deiner lokalen Claude-Code-Sitzungen und schickt **Zahlen und Metadaten, nie Inhalte** an deinen eigenen tokenlore-Server. Einen zentralen tokenlore-Server gibt es nicht: Jeder betreibt seinen eigenen, und das Plugin sendet nur an den Server, mit dem du es koppelst. Den Server gibt es im Repo [tokenlore/tokenlore-server](https://github.com/tokenlore/tokenlore-server).

## Installation

In einer `claude`-Sitzung im Terminal:

```
/plugin marketplace add tokenlore/tokenlore
/plugin install tokenlore@tokenlore
```

Was gesendet wird, Koppeln, Befehle, Aktualisieren und Deinstallieren: [plugin/README.md](plugin/README.md). Bitte vor dem Koppeln den Abschnitt [Was wird gesendet?](plugin/README.md#was-wird-gesendet) lesen.

## Aufbau

| Ort | Inhalt |
|---|---|
| `plugin/` | das Plugin, wie Claude Code es installiert: Befehle, Hook, Starter und die fertigen Programme in `plugin/bin/` mit Prüfsummen in `plugin/bin/SHA256SUMS`. Nur dieser Ordner wird in dein Plugin kopiert. |
| Wurzel | der Go-Quellcode der Programme (`cmd/`, `protocol/`, `source/`, …), die Bauskripte in `scripts/` und der Marktplatz-Eintrag `.claude-plugin/marketplace.json` |
| [`protocol/doc.md`](protocol/doc.md) | das Lieferformat mit allen Feldern und Grenzen (im Plugin als `PROTOCOL.md`) |

Die Commits „Version x.y.z“ enthalten den Quellcode und die daraus gebauten Programme. Ältere Versionen aus der Zeit, bevor der Quellcode in dieses Repo kam, enthalten nur das Plugin.

## Selbst nachbauen

Du kannst prüfen, dass die Programme in `plugin/bin/` genau aus diesem Quellcode stammen. Der Bau ist reproduzierbar: gleiche Quellen und gleiche Go-Version ergeben dieselben Dateien, Byte für Byte.

Du brauchst:

- Go genau in der Version aus [`GO_TOOLCHAIN`](GO_TOOLCHAIN) (`go version` prüfen); eine andere Version ergibt andere Prüfsummen, und `build.sh` bricht dann ab. Hast du eine andere Go-Version installiert, lädt Go die passende selbst: `GOTOOLCHAIN=$(cat GO_TOOLCHAIN) bash scripts/verify-build.sh`.
- bash (unter Windows Git Bash) und git.

```bash
git clone https://github.com/tokenlore/tokenlore
cd tokenlore
git checkout v<version>          # die Version, die du installiert hast
bash scripts/verify-build.sh     # baut neu und vergleicht mit plugin/bin
```

Erwartet: `OK: Programme entsprechen dem Quelltext`. Weicht eine Datei ab, meldet das Skript welche.

## Sicherheit

Sicherheitslücken bitte vertraulich melden, siehe [plugin/SECURITY.md](plugin/SECURITY.md).

## Lizenz

MIT, siehe [LICENSE](LICENSE). Die Programme enthalten Bibliotheken anderer Lizenzen; deren Texte stehen in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

tokenlore ist kein Produkt von Anthropic und nicht mit Anthropic verbunden. „Claude“ und „Claude Code“ sind Marken von Anthropic.
