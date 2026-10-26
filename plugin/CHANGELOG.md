# Changelog

Was sich am Plugin für dich ändert. Format nach [Keep a Changelog](https://keepachangelog.com/de/1.1.0/), Versionen nach [Semantic Versioning](https://semver.org/lang/de/).

## Unveröffentlicht

## 0.5.0 – 2026-10-26

### Neu

- Der Quellcode der Programme liegt jetzt im selben Repo wie das Plugin. Du kannst die Programme selbst nachbauen und mit denen in `bin/` vergleichen; die Anleitung steht in der README des Repos unter „Selbst nachbauen“.

### Doku

- Das README sagt genauer, was gesendet wird: Zahlen und Metadaten, nie Inhalte; dazu gehören auch Werkzeugnamen und kurze Texte in `extra`.
- Neu im README: Es gibt keinen zentralen Server, du betreibst deinen eigenen. Ein Abschnitt erklärt, was beim Sitzungsstart passiert, auch ohne Kopplung. Dazu Hinweise zu Firmenproxys ohne `--ca` und zu den Plattformen ohne fertige Programme.
- Neu: `SECURITY.md` sagt, wie du eine Sicherheitslücke vertraulich meldest.

## 0.4.2 – 2026-10-25

### Neu

- Diese Datei: Was sich am Plugin für dich ändert, steht ab jetzt in `CHANGELOG.md`; das README verlinkt sie.

## 0.4.1 – 2026-10-25

### Neu

- Abschnitt „Lizenz“ im README mit dem Hinweis, dass tokenlore kein Produkt von Anthropic ist.

### Behoben

- Unter Windows mit `core.autocrlf=true` scheiterte die Prüfung der Programme (`sha256sum -c`), weil Starter und `bin/SHA256SUMS` mit Windows-Zeilenenden ankamen. Sie kommen immer mit Unix-Zeilenenden (LF) an.

## 0.4.0 – 2026-10-25

### Geändert

- Das Plugin kommt aus dem öffentlichen Repo `tokenlore/tokenlore`, das nur die Plugin-Dateien mit den fertigen Programmen, `LICENSE`, `THIRD_PARTY_NOTICES` und die Feldliste `PROTOCOL.md` enthält. Installation und Update bleiben gleich (`tokenlore/tokenlore`, `tokenlore@tokenlore`). Das Repo hat eine neue Git-Historie: Schlägt bei dir `claude plugin marketplace update tokenlore` fehl, füge den Marktplatz einmal neu hinzu: `claude plugin marketplace remove tokenlore`, dann `claude plugin marketplace add tokenlore/tokenlore` und `claude plugin install tokenlore@tokenlore`.

### Sicherheit

- Shell-Zeichen wie `;`, `|` oder `$(…)` in den Angaben zu `/tokenlore:…`-Befehlen wertet die Shell nicht mehr aus. Dafür sind einfache Anführungszeichen und Apostrophe (`'`) in den Angaben nicht mehr erlaubt: `--name "Mein PC"` funktioniert weiter, `--name 'Mein PC'` und Apostrophe im Gerätenamen nicht.

### Neu

- Lizenz MIT (`LICENSE`); die Lizenzen der in die Programme eingebauten Bibliotheken stehen in `THIRD_PARTY_NOTICES`.
