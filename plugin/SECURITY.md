# Sicherheit

## Unterstützte Versionen

Sicherheitskorrekturen gibt es nur für die jeweils neueste Version (der neueste Tag bzw. der Zweig `main`). Halte das
Plugin deshalb aktuell; wie, steht im [README](README.md#aktualisieren).

## Eine Lücke melden

Bitte melde Sicherheitslücken **nicht** in einem öffentlichen Issue, sondern vertraulich über GitHub: Reiter
„Security“ dieses Repos, „Report a vulnerability“. Nur die Betreuer des Projekts sehen die Meldung.

Hilfreich sind:

- betroffene Plugin-Version (Feld `version` in `.claude-plugin/plugin.json`) und Claude-Code-Version,
- Betriebssystem und Architektur,
- die Schritte, mit denen sich die Lücke nachstellen lässt, und was ein Angreifer damit erreicht.

Du bekommst eine Rückmeldung, sobald die Meldung gesichtet ist. Ist die Lücke bestätigt, erscheint die Korrektur als
neue Version; im [CHANGELOG](CHANGELOG.md) steht dann, was zu tun ist.

## Was in den Bereich fällt

- die Programme in `bin/`, der Starter und der Hook,
- die Befehle `/tokenlore:…`,
- alles, wodurch Inhalte das Gerät verlassen könnten, die laut [Was wird gesendet?](README.md#was-wird-gesendet) nie
  gesendet werden.

Lücken im Server meldest du im Repo [tokenlore/tokenlore-server](https://github.com/tokenlore/tokenlore-server). Fehler in
Claude Code gehören zu Anthropic.

## Was tokenlore bewusst nicht leistet

Die Grenzen, die das [README](README.md) offen nennt, sind keine Lücken, sondern beschriebene Eigenschaften. Beispiele:
Programme desselben Benutzers können den Geräteschlüssel lesen, und wer den Server betreibt, sieht alle gesendeten Zahlen
und Metadaten.
