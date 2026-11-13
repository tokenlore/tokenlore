# Changelog

Was sich am Plugin für dich ändert. Format nach [Keep a Changelog](https://keepachangelog.com/de/1.1.0/), Versionen nach [Semantic Versioning](https://semver.org/lang/de/).

## Unveröffentlicht

## 0.9.1 – 2026-11-13

### Geändert

- `/tokenlore:status` nennt unter „Neue Felder in den Sitzungsdateien“ keine Felder mehr, die das Plugin nach einem Update liest oder absichtlich auslässt. <!-- doku-ok: Chronik -->

## 0.9.0 – 2026-11-13

### Neu

- `/tokenlore:menu` öffnet ein Menü: oben stehen dein Gerät und der letzte Abgleich, darunter Status im Detail, E-Mail-Stufe ändern und Plugin aktualisieren. Aktualisieren und E-Mail-Stufe zeigen das Ergebnis zuerst, etwa „✓ Plugin ist aktuell (0.8.0)“ mit den ausgeführten Befehlen darunter; der Status nennt oben in einer Zeile Gerät und letzten Abgleich. Nach dem Aktualisieren steht `/reload-plugins` im Eingabefeld; Enter lädt die neue Version.
- Je Anfrage gehen auch die angeforderte Cache-Dauer, wie lange Werkzeug-Eingaben gestreamt wurden und das Modell nachgedacht hat, Plugin und Agent, denen Claude Code die Anfrage zuordnet, und der Grund eines Cache-Fehlschlags an deinen Server.
- API-Fehler wie ein erreichtes Nutzungslimit kommen ins Archiv und an den Server: Fehlerart, HTTP-Status und die Angaben zum Limit, nie der Fehlertext.
- Kennt dein Server diese Angaben nicht, hält die Lieferung vor der ersten solchen Zeile an. `/tokenlore:sync`, `/tokenlore:status` und der Sitzungsstart sagen dir das; nach dem Aktualisieren des Servers geht der Rest beim nächsten Abgleich.

### Geändert

- `/tokenlore:email` meldet alte und neue Stufe, etwa „✓ E-Mail-Stufe: Aus → Voll“.
- `/tokenlore:status` und `/tokenlore:email` stehen nicht mehr in der Liste beim Tippen von `/`; das Menü führt zu beiden. Getippt laufen sie wie gewohnt.

## 0.8.0 – 2026-11-13

### Neu

- Stammt dein Konfigurationsordner von einem anderen Rechner (etwa aus einer Sicherung), fragt `/tokenlore:connect` in einem Auswahlfenster, ob dieser Rechner das alte Gerät ist: Ja, Nein oder Später, mit Empfehlung und den Folgen je Antwort. Ohne Fenster nennt der Befehl die Frage als Text; antworten kannst du dann mit `--vorgaenger gleich`, `--vorgaenger neu` oder `--vorgaenger spaeter`. Ohne Antwort wird nicht gekoppelt.
- Die Frage ist kurz: zwei Zeilen, und jede Antwort sagt in einem Halbsatz, was passiert, etwa „Ja: dieser Rechner wird „Laptop““. Die empfohlene Antwort steht zuerst. Nach deiner Wahl wiederholt die Meldung deine Antwort.
- `/tokenlore:status`, `/tokenlore:sync`, `/tokenlore:disconnect` und `/tokenlore:email` mit Stufe zeigen ihre Ausgabe direkt. Claude schreibt sie nicht mehr ab; das spart Tokens und geht schneller.

## 0.7.0 – 2026-11-11

### Neu

- `/tokenlore:email` öffnet ein Auswahlfenster mit Aus, Kurz und Voll. Es zeigt die Adresse deines Kontos, ohne dass Claude sie liest. Überspringst du die Frage, bleibt alles, wie es war.

### Geändert

- `/tokenlore:email` schreibt keine Adresse mehr in den Chat.

### Behoben

- Arbeitest du in der Desktop-App mit einem anderen Konto als im Terminal, ordnet tokenlore jede Sitzung dem richtigen Konto zu, und jedes Konto bekommt seine eigene Adresse. Sitzungen, die schon im Archiv stehen, bleiben, wie sie sind.

## 0.6.0 – 2026-11-09

### Neu

- Ist dein Server älter als das Plugin, sagen `/tokenlore:sync`, `/tokenlore:status` und der Sitzungsstart „Dein tokenlore-Server ist älter als dieses Plugin …“ statt „bitte neu koppeln“; Neukoppeln klappt erst, wenn der Server aktualisiert ist.
- Ein Rechner bleibt ein Gerät: Das Plugin legt im Claude-Ordner eine Datei `tokenlore-id` an und erkennt deinen Rechner daran wieder. Koppelst du neu, etwa nach einer Neuinstallation oder nach dem Sperren, bleibt es auf dem Server dasselbe Gerät mit denselben Zahlen.
- Dein Altbestand (Zahlen von vor der Kopplung) kommt auf jedem Server vollständig an, auch wenn du ihn schon an einen anderen geschickt hast, und wird nachgeschickt, wenn er wächst. `--resend-legacy` brauchst du nicht mehr.
- Hast du deinen Claude-Ordner oder den tokenlore-Datenordner auf einen anderen Rechner kopiert, liefert das Plugin dort nichts, bis du neu koppelst. `/tokenlore:connect` macht den Rechner dann ohne Rückfrage zu einem eigenen Gerät.
- Hinweise vom Server: Lagen Anfragen schon bei einem anderen Benutzer oder hat der Server Tage aus der Zeit vor dem Koppeln nicht übernommen, weil ein anderes Gerät genau dieselben Zahlen hat, siehst du die Anzahl in `/tokenlore:status`, nach `/tokenlore:sync` und beim Sitzungsstart. Solche Tage schickt das Plugin nicht erneut, bis du neu koppelst oder sich ihre Zahlen ändern.
- Meldungen, nach denen du neu koppeln musst, sagen dir die Schritte: Code auf der Webseite holen, Befehl in Claude Code einfügen.
- Koppelst du ein Gerät neu, behält es seinen Namen auf dem Server; nur mit `--name` gibst du ihm einen neuen.
- Verständliche Meldungen, wenn dein Gerät anderswo neu gekoppelt oder gesperrt wurde.
- Archiv auf deinem Rechner: Das Plugin legt jede Anfrage zuerst unterschrieben in einem Archiv im tokenlore-Datenordner ab und liefert nur daraus. Löscht Claude Code alte Sitzungsdateien, bleiben ihre Zahlen im Archiv und kommen auf jedem Server an, mit dem du koppelst; nichts zählt doppelt. Wo das Archiv liegt, wie groß es wird und wie du es löschst, steht im README unter „Archiv“.
- Mehr Angaben je Anfrage, nie Inhalt: Das Plugin schickt eine feste Liste, etwa alle Token-Arten, Modell, Effort, Werkzeugnamen mit Anzahl und die Version von Claude Code. Taucht in Claude Code ein neues Feld auf, nennt `/tokenlore:status` seinen Namen; mitgeschickt wird es nicht.
- Nach dem Ende einer Sitzung kommen ihre Zahlen auf den Server, ohne dass du eine neue Sitzung startest, höchstens alle 5 Minuten. Kam der letzte Abgleich vor weniger als 5 Minuten, kommt die letzte Anfrage der Sitzung mit dem ersten Abgleich, der mindestens 5 Minuten nach dieser Anfrage läuft; nichts geht verloren, nichts zählt doppelt.
- Löschst du `tokenlore-id` und den tokenlore-Datenordner, bekommt dein Rechner beim nächsten Koppeln dieselbe Kennung zurück und bleibt auf dem Server dasselbe Gerät. Die Kennung entsteht aus der Systemkennung deines Rechners, die selbst nie dein Gerät verlässt, und dem Ort des Claude-Ordners; erreichst du ihn über einen Symlink, bleibt die Kennung dieselbe. Hatte dein Rechner noch eine zufällige Kennung aus einer früheren Version, wird er dabei einmal ein neues Gerät; das kannst du auf der Webseite mit dem alten zusammenlegen.
- Beim Koppeln weist das Plugin nach, dass es den Signaturschlüssel wirklich besitzt. So kann niemand deinen öffentlichen Schlüssel als seinen melden und dein Gerät aussperren.
- Hat ein anderes Gerät beim Koppeln deinen Signaturschlüssel übernommen, etwa weil du den Datenordner samt Schlüssel auf einen anderen Rechner kopiert hast, sagt dir das Plugin das beim Sitzungsstart, bei `/tokenlore:sync` und in `/tokenlore:status` und nennt die Schritte zum Neukoppeln.
- Hat ein anderes Gerät einen früheren Signaturschlüssel deines Rechners übernommen und fehlt dir dieser Schlüssel, liefert dein Rechner neue Zahlen trotzdem weiter. Ältere Zeilen mit dem übernommenen Schlüssel hält das Plugin zurück; sie bleiben in deinem Archiv, und `/tokenlore:status` und `/tokenlore:sync` sagen dir, wie viele es sind. Neu koppeln musst du dafür nicht. Ist das andere Gerät derselbe Rechner, lege beide auf der Webseite unter „Geräte“ zusammen und kopple danach neu; dann gehen auch die älteren Zeilen mit. `/tokenlore:connect` nennt nach dem ersten Abgleich dasselbe wie `/tokenlore:sync`, auch die Zahl dieser Zeilen.
- Ist das Plugin für deinen Server zu alt, sagt es „Bitte tokenlore auf mindestens … aktualisieren“ und nennt die Befehle dazu. Deine Zahlen warten so lange im Archiv.
- Zieht dein Server mit seiner Datenbank auf eine neue Adresse um, bleibt dein Rechner dort nach dem Neukoppeln dasselbe Gerät.
- Jeder Rechner hat einen eigenen Signaturschlüssel; der Server nimmt nur Zeilen mit gültiger Unterschrift an. Fehlt der Schlüssel, meldet das Plugin „Signaturschlüssel fehlt, bitte neu koppeln.“; deine älteren Zeilen bleiben gültig.
- Stammt die Datenbank deines Servers von einer älteren Version, zeigen `/tokenlore:connect`, `/tokenlore:sync`, `/tokenlore:status` und der Sitzungsstart die Meldung des Servers statt „Server ausgelastet“. Deine Zahlen warten im Archiv, bis der Betreiber den Server neu eingerichtet hat.
- Ist eine Zeile im Archiv beschädigt, etwa von Hand geändert, schickt das Plugin sie nicht mit und nennt ihre Anzahl; alles andere geht weiter raus.

### Geändert

- Braucht einen Server mit Gerätekennung; die Option `--resend-legacy` gibt es nicht mehr.
- Braucht einen tokenlore-Server, der Signaturen prüft. Nach dem Update koppelst du jeden Rechner einmal neu (`/tokenlore:connect` mit einem neuen Code); dabei entsteht der Signaturschlüssel, und alles, was Claude Code noch an Sitzungsdateien hat, kommt ins Archiv.
- Deine volle E-Mail-Adresse steht nur lokal im Archiv; was davon an den Server geht, entscheidet weiter `/tokenlore:email`.
- `sitzungen.jsonl` im Archiv hat je Sitzung nur einen Eintrag und bleibt auch ohne Verbindung klein: Einträge älter als 90 Tage fallen weg, höchstens 10 000 bleiben.
- Nimmt der Server einen Signaturschlüssel beim Koppeln nicht an, weil er dort einem anderen Gerät gehört, zeigt das Plugin eine Warnung.

### Entfernt

- Eine Projekt-Kennung wird nicht mehr gesendet.
- Alte Dateien früherer Versionen (`session-accounts.jsonl`, alte Angaben im Merkzettel und in `tokenlore-id`) räumt das Plugin selbst weg.

### Doku

- Das README sagt unter „Voraussetzungen“, dass das Plugin nur in Claude Code läuft und im Chat von Claude (Browser, Desktop-App, Handy) nichts tut.

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
