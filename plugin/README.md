# tokenlore (Claude-Code-Plugin)

Das Plugin liest die lokalen Claude-Code-Sitzungsdateien und schickt **Zahlen und Metadaten, nie Inhalte** an deinen eigenen tokenlore-Server. Zu den Metadaten gehören auch Texte wie Werkzeugnamen und kurze Werte in `usage` (siehe [Was wird gesendet?](#was-wird-gesendet)).

Einen zentralen tokenlore-Server gibt es nicht: Jeder betreibt seinen eigenen, und das Plugin sendet nur an den Server, mit dem du es koppelst. Den Server gibt es im Repo [tokenlore/tokenlore-server](https://github.com/tokenlore/tokenlore-server).

**Inhalt:** [Was wird gesendet?](#was-wird-gesendet) · [Archiv](#archiv) · [Voraussetzungen](#voraussetzungen) · [Installation](#installation) · [Benutzung](#benutzung) · [Ein Rechner, ein Gerät](#ein-rechner-ein-gerät) · [Ablage und Schlüssel](#ablage-und-schlüssel) · [Deinstallieren](#deinstallieren) · [Lizenz](#lizenz) · [Änderungen](CHANGELOG.md)

## Was wird gesendet?

Bitte vor dem Koppeln lesen. Die Feldliste mit allen Grenzen steht in [`PROTOCOL.md`](PROTOCOL.md) und ist die maßgebliche Quelle. <!-- doku-ok: Ziel liegt im veröffentlichten Paket -->

**Was das Plugin liest:** die Sitzungsdateien von Claude Code unter `~/.claude/projects/` (nur die Nutzungsangaben jeder API-Anfrage), `stats-cache.json` (Tageswerte für die Vorgeschichte), die Kontokennung und E-Mail-Adresse aus `~/.claude.json` und die eigene Kennungsdatei `~/.claude/tokenlore-id` (siehe [Ein Rechner, ein Gerät](#ein-rechner-ein-gerät)).

**Erst ins Archiv, dann zum Server:** Jede fertige Anfrage schreibt das Plugin zuerst als signierte Zeile in sein [Archiv](#archiv) auf diesem Rechner. Gesendet wird nur aus dem Archiv, Zeile für Zeile unverändert. So geht nichts verloren, wenn Claude Code alte Sitzungsdateien löscht.

**Was gesendet wird:**

- Pro API-Anfrage: Anfrage-, Nachrichten- und Sitzungs-ID, die Kennungen der Protokollzeile und ihrer Vorgängerin (`uuid`, `parentUuid`), Zeitpunkt (UTC) und Ortszeit-Versatz, Modellname und angefordertes Modell, `effort`-Stufe, Startweg (`entrypoint`, etwa `cli`), Claude-Code-Version, `stop_reason`, ob die Anfrage von einem Subagenten stammt, die Liste benutzter Werkzeuge mit Anzahl, Konto-ID und der ganze Block mit den Nutzungsangaben (`usage`: Token-Zahlen für Eingabe, Ausgabe, Cache und Denken, Websuchen und -abrufe, dazu kurze Angaben wie `service_tier` oder `speed`). Texte in `usage` kürzt das Plugin auf 64 Zeichen.
- Pro Anfrage dazu die Signatur dieser Zeile (siehe unten).
- Pro Sitzung: Sitzungs-ID, Claude-Code-Version, Konto-ID, abgeleitet aus den Anfragen.
- Pro Gerät: der Gerätename, den du beim Koppeln vergibst, Betriebssystem, Architektur, Zeitzone, Plugin-Version.
- Beim Koppeln: die Plugin-Version, der öffentliche Signaturschlüssel dieses Rechners mit einem Besitznachweis (seine Signatur über die Server-Kennung und den Kopplungscode; sie zeigt, dass der Rechner den privaten Schlüssel hat, und verrät ihn nicht), die öffentlichen Teile früherer Signaturschlüssel und `device_source`, die Kennung dieses Rechners für genau diesen Server. `device_source` ist ein HMAC-SHA256 der Server-Kennung (`server_id`, die der Server vor dem Koppeln nennt) mit der Kennung aus `tokenlore-id` als Schlüssel. Jeder Server bekommt eine andere, aus ihr lässt sich die Kennung nicht zurückrechnen, und ein Umzug des Servers auf eine neue Adresse ändert sie nicht.
- Vorgeschichte: die Tageswerte aus `stats-cache.json` (Datum, Tokens je Modell, Nachrichten, Sitzungen, Werkzeugaufrufe und das Datum, bis zu dem Claude Code die Datei fertig gerechnet hat), nur für Tage ohne einzelne Anfragen in den Sitzungsdateien. Das Plugin übernimmt sie einmal ins Archiv, signiert wie die Anfragen.

**Was das Gerät nie Richtung tokenlore-Server verlässt:** Nachrichteninhalte, Werkzeug-Eingaben und -Ausgaben, Dateipfade, Projektnamen, eine Projekt-Kennung, Git-Zweige, Arbeitsordner, der Name aus dem Claude-Konto, der private Signaturschlüssel. Warnungen des Plugins (sie können Pfade und Projektordner enthalten) gehen nie an den Server.

**Neue Felder in den Sitzungsdateien:** Das Plugin übernimmt nur Felder einer festen Liste. Taucht in den Sitzungsdateien ein neues Feld auf, nimmt es das Feld nicht auf und nennt nur seinen Namen in `/tokenlore:status`.

**Signatur:** Jede Zeile im Archiv trägt eine Ed25519-Signatur mit dem Signaturschlüssel dieses Rechners. Der Server nimmt nur Zeilen an, deren Signatur zu einem Schlüssel passt, den dieses Gerät beim Koppeln gemeldet hat, und speichert die Signatur mit. Eine kopierte oder nachträglich geänderte Zeile kommt so nicht an. Der private Schlüssel bleibt auf dem Rechner (siehe [Ablage und Schlüssel](#ablage-und-schlüssel)). Wer an deinem Rechner deinen Schlüssel auslesen kann, kann damit auch eigene Zeilen signieren: Die Signatur schützt vor Kopien und Versehen, nicht vor Absicht.

**Was Claude mitliest:** Die Ausgabe eines `/tokenlore:…`-Befehls landet in Claudes Kontext. `/tokenlore:status` zeigt deshalb nur die Anzahl der Warnungen. `/tokenlore:status --warnings` zeigt die Texte (Claude liest dann die Pfade mit), `/tokenlore:status --next` das ganze nächste Paket (Claude liest dann Konto-IDs und Werkzeugnamen mit).

**Werkzeugnamen:** Sie werden voll gesendet, auch die von MCP-Servern (`mcp__<server>__<werkzeug>`). Der Servername kann einen Firmen- oder Kundennamen enthalten. Wenn dich das stört, nutze solche Server nicht auf gekoppelten Geräten oder koppele nicht.

**E-Mail-Adresse des Kontos (Standard: aus):** Im Archiv steht die volle Adresse einmal je Konto in `konten.jsonl`; was davon gesendet wird, entscheidet die Stufe. `/tokenlore:email` zeigt die Stufen.

- `off` sendet nichts und löscht eine gespeicherte Adresse auf dem Server.
- `short` sendet eine gekürzte Form (erste zwei Zeichen und Domain); die volle Adresse verlässt das Gerät nicht.
- `full` sendet die volle Adresse.

**Zu altes Plugin:** Jeder Server nennt die älteste Plugin-Version, die er annimmt. Ist dein Plugin älter, lehnt er Koppeln und Liefern ab; `/tokenlore:status` zeigt dann „Bitte tokenlore auf mindestens … aktualisieren“ mit dem Befehl dazu. Die Daten warten im Archiv und gehen nach dem Aktualisieren raus.

**Vorschau:** `/tokenlore:status --next` zeigt genau das nächste Paket aus dem Archiv, das gesendet würde.

## Archiv

Das Archiv ist die einzige Quelle für den Server. Es hält jede Anfrage dieses Rechners fest, auch wenn Claude Code die Sitzungsdateien löscht (Standard nach 30 Tagen), und lässt sich jederzeit erneut liefern, etwa an einen neuen Server.

**Was darin steht:** je Anfrage eine Zeile mit genau den Angaben, die unter [Was wird gesendet?](#was-wird-gesendet) für eine Anfrage stehen, samt Signatur; die Vorgeschichte aus `stats-cache.json`; je Claude-Konto Konto-ID und volle E-Mail-Adresse. Keine Inhalte, keine Pfade, keine Projekte. Eine Anfrage kommt erst hinein, wenn sie fertig ist: wenn in derselben Sitzungsdatei eine spätere Anfrage folgt, die Datei 5 Minuten ruht oder die Sitzung endet. Eine Zeile wird nie geändert.

**Wo:** im Datenverzeichnis (siehe [Ablage und Schlüssel](#ablage-und-schlüssel)) unter `archiv/rechner-<kennung>/`, `<kennung>` ist die Kennung aus `tokenlore-id`. Nur dein Benutzer kann den Ordner lesen.

| Datei | Inhalt |
|---|---|
| `JJJJ-MM.jsonl` | Anfragen des laufenden Monats (nach UTC), eine Zeile je Anfrage |
| `JJJJ-MM.jsonl.gz` und `.sig` | ein abgeschlossener Monat, gepackt, mit Signatur über die ganze Datei |
| `JJJJ-MM.nachtrag.jsonl` | Anfragen, die erst nach dem Packen ihres Monats fertig wurden |
| `vorgeschichte.jsonl` | Tageswerte aus `stats-cache.json`, einmal übernommen |
| `konten.jsonl` | je Konto Konto-ID und volle E-Mail-Adresse |
| `sitzungen.jsonl` | je Sitzung ein Eintrag mit dem beim Start angemeldeten Konto; geht nie an den Server; Einträge älter als 90 Tage fallen weg, höchstens 10 000 bleiben, auch ohne Verbindung |
| `geliefert.json` | wie viele Zeilen jeder Datei schon beim Server sind; neues Koppeln setzt nur diese Datei zurück, dann geht das ganze Archiv noch einmal |

Vor dem Senden prüft das Plugin jede Zeile selbst: Passt die Signatur nicht (etwa nach einer Änderung von Hand) oder verletzt die Zeile eine Grenze, geht sie nicht mit, und `/tokenlore:status` nennt die Zahl ausgesonderter Zeilen. Die übrigen gehen trotzdem raus.

Das Plugin liefert nur aus dem Ordner seiner eigenen Kennung. Ein Ordner mit fremder Kennung, etwa aus einer Kopie, bleibt liegen und geht nie raus.

**Größe:** Eine Zeile ist etwa 1,4 KB groß. 10 000 Anfragen im Monat ergeben also etwa 14 MB für den laufenden Monat; gepackt braucht ein abgeschlossener Monat etwa ein Siebtel davon (gemessen an rund 2 500 echten Anfragen).

**Löschen:** `/tokenlore:disconnect` lässt Archiv und Signaturschlüssel stehen; beide gehören zum Rechner, nicht zur Verbindung. Löschst du den Ordner `archiv`, legt das Plugin ihn beim nächsten Abgleich neu an, übernimmt alles, was noch in den Sitzungsdateien und in `stats-cache.json` steht, und meldet es in `/tokenlore:status`; auf dem Server zählt dabei nichts doppelt. Anfragen, deren Sitzungsdateien Claude Code gelöscht hat, liegen danach allein auf dem Server. Ganz entfernen: siehe [Deinstallieren](#deinstallieren).

## Voraussetzungen

| Was | Wozu | Prüfen |
|---|---|---|
| Claude Code | Plugin installieren und ausführen | `claude --version` |
| Ein tokenlore-Server mit HTTPS | Ziel der Lieferung | `curl -s https://<server>/healthz` gibt `ok` aus (ohne Umschreibregeln `…/index.php/healthz`) |
| Windows/amd64, Linux/amd64, Linux/arm64 oder macOS/arm64 | Fertige Programme gibt es nur dafür | `/tokenlore:status` meldet keinen Plattformfehler |

Für Windows/arm64 und macOS/Intel (amd64) gibt es keine fertigen Programme. Das Plugin bringt die fertigen Programme mit (`bin/`, Prüfsummen in `bin/SHA256SUMS`) und lädt keinen Programmcode aus dem Netz nach. Der Quellcode liegt im selben Repo; wie du die Programme selbst nachbaust und vergleichst, steht unter [Selbst nachbauen](https://github.com/tokenlore/tokenlore#selbst-nachbauen). Auf anderen Plattformen meldet der Starter das und tut nichts.

**Nur in Claude Code:** Das Plugin läuft in Claude Code auf deinem Rechner. Im Chat von Claude (claude.ai im Browser, Chat in der Desktop-App, Handy-App) tut es nichts: Dort laufen keine Hooks, und es gibt keine Sitzungsdateien, die das Plugin lesen könnte. Steuerst du vom Handy aus eine Claude-Code-Sitzung auf deinem Rechner (Remote Control), läuft sie auf dem Rechner, und das Plugin zählt sie dort. Claude Code in der Cloud (claude.ai/code) ist nicht geprüft.

## Installation

1. In einer `claude`-Sitzung im Terminal:

   ```
   /plugin marketplace add tokenlore/tokenlore
   /plugin install tokenlore@tokenlore
   ```

   Oder in der Shell: `claude plugin marketplace add tokenlore/tokenlore`, dann `claude plugin install tokenlore@tokenlore`.
2. Prüfen: `/tokenlore:status` antwortet mit „Nicht verbunden. Erst /tokenlore:connect ausführen.“

### Aktualisieren

- **Automatisch:** in einer `claude`-Sitzung im Terminal `/plugin` → Reiter *Marketplaces* → *tokenlore* → *Enable auto-update*. Einmal pro Gerät; im Code-Tab der Desktop-App gibt es `/plugin` nicht. Claude Code holt neue Versionen dann kurz nach Sitzungsbeginn; aktiv werden sie mit `/reload-plugins` oder in der nächsten Sitzung.
- **Von Hand:** `claude plugin marketplace update tokenlore`, dann `claude plugin update tokenlore@tokenlore`, danach `/reload-plugins`.

Was sich in jeder Version ändert, steht in [CHANGELOG.md](CHANGELOG.md).

## Benutzung

### Koppeln

1. Auf der Webseite des Servers unter „Geräte“ einen Kopplungscode erzeugen. Dort steht der fertige Befehl `/tokenlore:connect …` mit Code und Ablaufzeit; ist auf dem Server unter „Team“ → „Server“ ein CA-Zertifikat hinterlegt, steht auch dessen Fingerabdruck (`--ca`) schon darin.
2. Diesen Befehl in Claude Code auf dem Gerät eingeben, z. B.:

   ```
   /tokenlore:connect https://tokenlore.example.org 123456 --name "Mein PC"
   /tokenlore:connect https://tokenlore.lan 123456 --ca <fingerabdruck> --name "Mein PC"
   ```

   Die erste Zeile gilt für einen Server mit öffentlich beglaubigtem Zertifikat (z. B. Let's Encrypt), die zweite für einen Server mit eigener CA. Ohne `--name` ist der Rechnername der Gerätename eines neuen Geräts; beim Neukoppeln bleibt dann der Name auf dem Server. Für Leerzeichen im Namen nimmst du doppelte Anführungszeichen. Einfache Anführungszeichen und Apostrophe (`'`) sind in den Angaben nicht erlaubt.
3. Prüfen: `/tokenlore:status` zeigt die Verbindung statt „Nicht verbunden“.

**Achtung:** Tippe die Zeile selbst ab oder prüfe sie genau, und füge nur Zeilen ein, die dein eigener Server ausgegeben hat. Eine fremde Zeile koppelt dein Gerät mit einem fremden Server.

**Server-Adresse:** `https://` ist Pflicht. Ein Pfad ist erlaubt, wenn der Server nicht an der Wurzel liegt, z. B. `https://example.org/tokenlore` oder, bei Webspace ohne Umschreibregeln, `https://example.org/tokenlore/index.php`. Die Aufrufpfade werden angehängt (`…/index.php/api/plugin/ingest`). Query, Fragment und Benutzerteil sind nicht erlaubt.

**Mit `--ca`** (Server mit eigener CA): Das Plugin holt das CA-Zertifikat, vergleicht den Fingerabdruck und vertraut danach nur dieser CA (Kettenprüfung mit Hostnamen-Prüfung, kein Proxy, keine Weiterleitungen). Ein leerer Fingerabdruck wird abgelehnt, und der Server muss in der Kopplung denselben Fingerabdruck bestätigen.

**Ohne `--ca`** (Server mit öffentlich beglaubigtem Zertifikat): Das Plugin vertraut den System-Zertifikaten des Geräts (mit Hostnamen-Prüfung, ohne Proxy, ohne Weiterleitungen) und speichert keine CA. Ein Fingerabdruck in der Kopplungsantwort wird dann nicht geprüft; meldet der Server einen, koppelt das Plugin und weist mit einer Hinweiszeile auf `--ca` hin. Nutzt dein Server eine eigene CA, gib `--ca` an: Ohne den Fingerabdruck ließe sich ein untergeschobener Server nicht erkennen. Bedenke auch: Die System-Zertifikate können von einem Firmenproxy oder einer Sicherheitssoftware ergänzt worden sein, die HTTPS-Verbindungen mitlesen. Willst du das ausschließen, gib `--ca` an.

**Gespeicherte CA:** Eine mit `--ca` gekoppelte Verbindung vertraut immer nur dieser CA und fällt nie auf die System-Zertifikate zurück.

**Neu koppeln:** Ein weiteres `/tokenlore:connect` mit einem frischen Code koppelt dieses Gerät neu, auch nach dem Sperren auf dem Server oder nach einer Neuinstallation des Plugins. Der Server erkennt den Rechner und bleibt beim selben Gerät; `connect` meldet dann „Bestehendes Gerät neu gekoppelt“. Einzelheiten unter [Ein Rechner, ein Gerät](#ein-rechner-ein-gerät).

### Befehle

| Befehl | Wirkung |
|---|---|
| `/tokenlore:connect <https://server> <code> [--ca <fingerabdruck>] [--name <gerätename>]` | Gerät koppeln oder neu koppeln; ein [kopierter Ordner](#kopierter-konfigurationsordner) wird dabei ein eigenes Gerät |
| `/tokenlore:sync` | sofort abgleichen |
| `/tokenlore:status [--next] [--warnings]` | Verbindung und Abgleich zeigen, mit `--next` das nächste Paket, mit `--warnings` die Texte der Warnungen |
| `/tokenlore:email [off\|short\|full]` | E-Mail-Stufe zeigen oder setzen |
| `/tokenlore:disconnect` | Gerät trennen |

### Was beim Sitzungsstart passiert

Das Plugin meldet bei Claude Code einen Hook an, der bei jedem Sitzungsstart läuft, **auch ohne Kopplung**:

1. Er liest die Sitzungs-ID, die Claude Code ihm übergibt, und die Kontokennung des angemeldeten Kontos aus `~/.claude.json`, und hängt beide als eine Zeile an `sitzungen.jsonl` im Archivordner an (siehe [Archiv](#archiv)). Eine Sitzung gehört genau einem Konto; so behält sie es, auch wenn du dich danach mit einem anderen Konto anmeldest. Ohne Kennungsdatei (nie gekoppelt) schreibt er nichts.
2. Nur wenn das Gerät gekoppelt ist, startet er den Abgleich in einem abgekoppelten Prozess. Ohne Kopplung endet er nach Schritt 1, ohne Netzverkehr.
3. Ist das Gerät gekoppelt und gibt es ein Problem, das du lösen musst, zeigt er eine Meldung, die mit „tokenlore:“ beginnt (siehe [Meldungen](#meldungen)).

Das Konto einer Anfrage steht im Archiv. Hat ihre Sitzung dort schon ein Konto, gilt dieses; sonst `ownerAccountUuid` aus der Sitzungsdatei, sonst das Konto beim Sitzungsstart, sonst das beim Archivieren angemeldete Konto.

Claude Code wartet darauf nie (Notbremse 2 s, auch ohne Netz).

### Was beim Sitzungsende passiert

Ein zweiter Hook läuft, wenn eine Sitzung endet (beim Beenden, bei `/clear` und beim Wechsel mit `/resume`). Ist das Gerät gekoppelt und liegt der letzte Abgleich mindestens 5 Minuten zurück, startet er den Abgleich in einem abgekoppelten Prozess. So steht die eben beendete Sitzung auf dem Server, ohne dass du eine neue startest. Die letzte Anfrage dieser Sitzung und ihrer Subagenten gilt dabei sofort als fertig. Kam der letzte Abgleich vor weniger als 5 Minuten, gleicht der Hook nicht ab: Die übrigen Anfragen der Sitzung kommen mit dem nächsten Abgleich, ihre letzte Anfrage mit dem ersten Abgleich, der mindestens 5 Minuten nach der letzten Zeile ihrer Sitzungsdatei läuft. Nichts geht dabei verloren, und nichts zählt doppelt, auch nicht, wenn du die Sitzung später fortsetzt.

Claude Code wartet darauf nie (Notbremse 1 s, auch ohne Netz).

### Meldungen

Der Abgleich läuft im Hintergrund. Was du selbst beheben musst, zeigt das Plugin beim Sitzungsstart und in `/tokenlore:status`. Muss neu gekoppelt werden, nennt die Meldung die Schritte: auf `<Server>/settings/add-device` einen Code holen und den Befehl, den die Webseite anzeigt, in Claude Code einfügen. Vorher trennen (`/tokenlore:disconnect`) ist nicht nötig; `connect` ersetzt die alte Verbindung.

| Meldung | Bedeutung | Was tun |
|---|---|---|
| „Dieser Ordner stammt von einem anderen Rechner oder einer anderen Kopplung …“ | Die Kennungsdatei `tokenlore-id` passt nicht zu der, mit der gekoppelt wurde, oder sie stammt von einem anderen Rechner, etwa weil der Datenordner oder `~/.claude` kopiert wurde. Bis zum Neukoppeln geht nichts an den Server, weder Anfragen noch Vorgeschichte; nichts geht verloren, alles Offene folgt nach dem Koppeln. | Neu koppeln. |
| „Dieses Gerät wurde anderswo neu gekoppelt …“ | Dasselbe Gerät wurde mit einem neueren Schlüssel gekoppelt; der Schlüssel hier ist gesperrt. Das passiert etwa, wenn ein kopierter Datenordner samt Schlüssel auf einem anderen Rechner neu gekoppelt wurde. | Soll dieser Rechner weiter liefern: neu koppeln. |
| „Dieses Gerät ist auf dem Server gesperrt …“ | Gerät oder Schlüssel wurde auf der Webseite gesperrt. | Soll es wieder liefern: neu koppeln. Es bleibt dasselbe Gerät mit seinen Zahlen. |
| „Der Server kennt den Schlüssel dieses Geräts nicht …“ | Der Server kennt den Schlüssel nicht, etwa nach einem Neuaufsetzen des Servers. | Neu koppeln. |
| „Der Server kennt den Signaturschlüssel dieses Geräts nicht mehr …“ | Ein anderes Gerät hat beim Koppeln denselben Signaturschlüssel gemeldet und ihn so übernommen, etwa weil der Datenordner samt `sign.key` auf einen anderen Rechner kopiert wurde. Bis zum Neukoppeln geht nichts an den Server; deine Zahlen warten im Archiv. | Neu koppeln; danach verliert das andere Gerät den Schlüssel. Fehlt dabei auch `sign.key`, siehe den Hinweis „… ältere Archivzeilen gehen nicht an den Server …“. | <!-- doku-ok: Wortlaut der Meldung -->
| „Signaturschlüssel fehlt, bitte neu koppeln.“ | Der private Signaturschlüssel fehlt oder passt nicht zum gemerkten öffentlichen Teil. Bis zum Neukoppeln geht nichts an den Server. | Neu koppeln; dabei entsteht ein neuer Schlüssel, ältere Zeilen prüft der Server weiter mit dem ersetzten. |
| „Dein tokenlore-Server ist älter als dieses Plugin …“ | Der Signaturschlüssel fehlt, und der Server ist zu alt für dieses Plugin; Neukoppeln scheitert dort. So zeigt es sich nach dem Update von einem Plugin ohne Signaturschlüssel. Bis dahin geht nichts an den Server. | Den Betreiber bitten, den Server zu aktualisieren, danach neu koppeln. Deine Zahlen warten auf dem Rechner. |
| „Die Datenbank des Servers stammt von einer älteren Version …“ | Der Server läuft mit einer neuen tokenlore-Version, seine Datenbank stammt aber von einer älteren. Bis der Betreiber tokenlore dort neu einrichtet, nimmt der Server nichts an; deine Zahlen warten im Archiv. | Dem Betreiber des Servers Bescheid geben; die Anleitung steht auf der Webseite des Servers. Danach neu koppeln. |
| „Bitte tokenlore auf mindestens … aktualisieren …“ | Der Server nimmt diese Plugin-Version nicht mehr an. Die Daten warten im Archiv. | Den Befehl aus der Meldung ausführen (siehe [Aktualisieren](#aktualisieren)); danach geht alles Offene raus. Neu koppeln ist nicht nötig. |

Dazu kommen Hinweise des Servers zum letzten Abgleich, als Zeile `Hinweis: …` in `/tokenlore:status`, nach `/tokenlore:sync` und beim nächsten Sitzungsstart. Zu tun gibt es dabei nichts:

| Hinweis | Bedeutung |
|---|---|
| „… Anfragen waren schon auf dem Server …“ | Der Server hatte diese Anfragen schon, etwa nach einem neuen Koppeln oder einem neu angelegten Archiv. Er zählt sie nicht doppelt. |
| „… Anfragen lagen schon bei einem anderen Benutzer …“ | Jede Anfrage zählt auf dem Server nur einmal, beim Benutzer, der sie zuerst geliefert hat. Das passiert etwa, wenn jemand einen Claude-Ordner kopiert hat. Der Server nennt nur die Anzahl, nie den anderen Benutzer. Neue Arbeit auf diesem Rechner zählt normal. |
| „… Tage aus der Zeit vor dem Koppeln hat der Server nicht übernommen …“ | Ein anderes Gerät hat für diese Tage genau dieselben Zahlen aus `stats-cache.json` geliefert; sie zählen dort. Das Plugin merkt sich die Tage in `geliefert.json` und schickt sie nicht erneut, bis du neu koppelst. |
| „… ältere Archivzeilen gehen nicht an den Server …“ | Diese Zeilen sind mit einem früheren Signaturschlüssel dieses Rechners signiert, den der Server einem anderen Gerät zuordnet (etwa per kopiertem `sign.key` übernommen, hier verloren). Sie bleiben unverändert im Archiv und gehen nicht mit; nie Geliefertes davon fehlt auf dem Server. Neue Anfragen gehen weiter, nichts zählt doppelt. Das Plugin merkt sich solche Schlüssel bis zum nächsten Koppeln; die Zahl steht in `geliefert.json`. Den Hinweis zeigen `status`, `sync` und `connect`. | Ist das andere Gerät derselbe Rechner: beide auf der Webseite unter „Geräte“ zusammenlegen, danach dieses Gerät neu koppeln; dann gehen die Zeilen mit. Nur neu koppeln reicht nicht. | <!-- doku-ok: Fachbegriff früherer Signaturschlüssel -->

In `/tokenlore:status` steht ein fremder Ordner oder Rechner als Zeile `Achtung: …`, ein abgelehnter Schlüssel als `Letzter Fehler: …` oder, wenn danach ein anderer Fehler kam, als `Schlüssel abgelehnt: …`. Ist die Systemkennung des Rechners nicht lesbar, liefert das Plugin trotzdem und zeigt in `/tokenlore:status` eine Zeile `Warnung: …`, weil es dann einen kopierten Ordner nicht erkennen kann.

## Ein Rechner, ein Gerät

Ein Rechner bleibt auf jedem Server ein Gerät, egal ob du es sperrst, neu koppelst oder das Plugin neu installierst. Genauer: Gerät ist ein Claude-Konfigurationsordner (`~/.claude`, oder der Ordner aus `CLAUDE_CONFIG_DIR`), erkannt an der Kennungsdatei `tokenlore-id` darin.

- **Neu koppeln:** Ein neues `/tokenlore:connect` an einem Server, der diesen Rechner kennt, koppelt dasselbe Gerät neu. Seine Zahlen bleiben, alle älteren Schlüssel dieses Geräts sind danach gesperrt, und eine Sperre auf der Webseite ist aufgehoben. Der Gerätename auf dem Server bleibt, etwa ein auf der Webseite vergebener; nur mit `--name` bekommt das Gerät den neuen Namen.
- **Server zieht um:** Erkannt wird der Server an seiner Kennung (`server_id`), nicht an seiner Adresse. Zieht er mit seiner Datenbank auf eine neue Adresse um, koppelst du mit der neuen Adresse neu und bleibst dasselbe Gerät.
- **Anderer Server:** Auf einem anderen Server wird der Rechner ein eigenes Gerät dort. Der Signaturschlüssel bleibt; frühere Signaturschlüssel gehen beim Koppeln mit, damit der Server auch ältere Zeilen prüfen kann.
- **Nach jedem Koppeln:** Das ganze Archiv mit der Vorgeschichte aus `stats-cache.json` geht noch einmal an den Server, auf jedem Server, jede Zeile einmal. Was der Server schon hat, lässt er unverändert. So kommt alles überall vollständig an und zählt nie doppelt, auch nicht nach mehrfachem Koppeln.
- **Zusammenlegen:** Zeigen zwei Geräte auf dem Server dieselben abgeschlossenen Tage, ist es wahrscheinlich derselbe Rechner. Die Webseite fragt dann „Zusammenlegen?“; zusammengelegt wird nur nach deiner Bestätigung. Danach liefert das Plugin ohne neue Kopplung weiter.
- **Anderer Rechner:** Ein anderer Rechner ist immer ein eigenes Gerät, auch beim Wechsel auf einen neuen Rechner; der Server zählt beide zusammen. Das alte Gerät sperrst du auf der Webseite.
- **Prüfung vor jedem Liefern:** Vor jedem Abgleich vergleicht das Plugin die Kennungsdatei mit der Kopplung und die Systemkennung mit der in der Kennungsdatei. Passt eins nicht, liefert es nichts und meldet es (siehe [Meldungen](#meldungen)). Geklonte virtuelle Maschinen oder Container mit derselben Systemkennung erkennt die Prüfung nicht.

### Die Kennungsdatei

`tokenlore-id` liegt im Claude-Konfigurationsordner, meist `~/.claude/tokenlore-id`. Das Plugin legt sie beim ersten Koppeln an und löscht sie nie, auch nicht bei `/tokenlore:disconnect` oder beim Deinstallieren. Sie ist eine kleine JSON-Datei mit diesen Feldern:

| Feld | Bedeutung |
|---|---|
| `id` | Die Kennung dieses Ordners: 32 Hex-Zeichen. Das Plugin leitet sie aus `machine` und dem echten Pfad des Ordners ab, mit aufgelösten Symlinks (HMAC-SHA256 mit festem Schlüssel); ist die Systemkennung nicht lesbar, ist sie zufällig. Eine vorhandene Kennung bleibt, wie sie ist, auch eine zufällige. Aus ihr und der Server-Kennung (`server_id`) berechnet das Plugin `device_source`. |
| `machine` | Prüfsumme (HMAC-SHA256 mit festem Schlüssel) der Systemkennung dieses Rechners (Linux `/etc/machine-id`, macOS die Hardware-UUID, Windows die `MachineGuid`). Daran erkennt das Plugin, ob der Ordner von einem anderen Rechner kopiert wurde. Leer, wenn die Systemkennung nicht lesbar ist. |
| `predecessor` | Bei einem kopierten Ordner die Kennung des Rechners, von dem er stammt; sonst leer. |

Eine Kopie der Datei steht im Merkzettel `state.json`. Fehlt die Datei oder ist sie unlesbar, stellt `/tokenlore:connect` sie aus der Kopie wieder her. Weichen Datei und Kopie voneinander ab, gilt die Datei. Fehlt die Kopie auch (etwa weil du Datei und Datenverzeichnis gelöscht hast), leitet `/tokenlore:connect` die Kennung wieder aus der Systemkennung ab: Derselbe Ordner auf demselben Rechner bekommt dieselbe Kennung und bleibt auf dem Server dasselbe Gerät, auch wenn du ihn über einen Symlink erreichst. Der Ordner eines anderen Benutzers desselben Rechners bleibt ein eigenes Gerät.

Ist die Datei unlesbar, bleibt sie als `tokenlore-id.kaputt` daneben liegen. Nur wenn die alte Kennung zufällig war oder die Systemkennung nicht lesbar ist, entsteht dabei eine andere Kennung, und der Rechner wird beim nächsten Koppeln ein neues Gerät; auf der Webseite lässt er sich dann mit dem alten zusammenlegen.

### Kopierter Konfigurationsordner

Stammt die Kennungsdatei von einem anderen Rechner (du hast `~/.claude` kopiert oder aus einer Sicherung geholt), liefert das Plugin nichts, bis du neu koppelst. `/tokenlore:connect` macht diesen Rechner dann ohne Rückfrage zu einem eigenen Gerät: Er bekommt eine neue Kennung; die, von der er stammt, steht als `predecessor` in der Datei. Er bekommt ein eigenes Archiv; das Archiv des ursprünglichen Rechners liefert er nie. Anfragen, die schon der ursprüngliche Rechner geliefert hat, zählt der Server nicht doppelt, und Vorgeschichte-Tage mit genau denselben Zahlen weist er ab. Der ursprüngliche Rechner liefert unverändert weiter, solange der Datenordner nicht mitkopiert wurde.

Lag darin auch `sign.key`, übernimmt der neue Rechner beim Koppeln diesen Signaturschlüssel: Der ursprüngliche meldet dann „Der Server kennt den Signaturschlüssel dieses Geräts nicht mehr …“ und holt ihn mit erneutem Koppeln zurück, worauf der andere ihn verliert. Hat der ursprüngliche Rechner danach auch `sign.key` verloren, bekommt er beim Koppeln einen neuen Schlüssel und liefert neue Anfragen weiter; ältere Zeilen mit dem übernommenen Schlüssel gehen nicht mit (Hinweis „… ältere Archivzeilen gehen nicht an den Server …“). Ist das andere Gerät derselbe Rechner, lege beide auf der Webseite unter „Geräte“ zusammen und kopple danach neu; dann gehen die älteren Zeilen mit. Kopiere den Datenordner deshalb nicht auf einen anderen Rechner. <!-- doku-ok: Wortlaut der Meldung -->

## Ablage und Schlüssel

- **Datenverzeichnis:** `$TOKENLORE_DATA_DIR`, sonst `<Benutzerkonfigurationsordner>/tokenlore` (Windows `%AppData%\tokenlore`, Linux `~/.config/tokenlore`, macOS `~/Library/Application Support/tokenlore`). Dort liegen der Merkzettel `state.json`, die Verbindung `connection.json`, der Stand des letzten Abgleichs `status.json`, das Archiv `archiv/` (siehe [Archiv](#archiv)) und gegebenenfalls `device.key` und `sign.key`.
- **Kennungsdatei:** `tokenlore-id` im Claude-Konfigurationsordner, meist `~/.claude/tokenlore-id` (siehe [Die Kennungsdatei](#die-kennungsdatei)); eine Kopie steht im Merkzettel.
- **Cache-Ordner:** `<Benutzer-Cache-Ordner>/tokenlore` (Windows `%LocalAppData%\tokenlore`, Linux `~/.cache/tokenlore`, macOS `~/Library/Caches/tokenlore`) mit der Sperrdatei `tokenlore.lock`.
- **Geräteschlüssel:** im Schlüsselspeicher des Systems (Windows-Anmeldeinformationsverwaltung, macOS-Schlüsselbund, Linux Secret Service), sonst in der Datei `device.key` (nur für den Benutzer lesbar) im Datenverzeichnis. Er steht nie in `settings.json`, einer Befehlszeile, Ausgabe oder einem Log.
- **Signaturschlüssel:** genauso abgelegt wie der Geräteschlüssel, unter eigenem Eintrag, sonst in der Datei `sign.key` (nur für den Benutzer lesbar) im Datenverzeichnis. Er entsteht beim ersten Koppeln und bleibt bei `/tokenlore:disconnect` und jedem weiteren Koppeln stehen. Fehlt er, erzeugt das Plugin beim Liefern keinen neuen, sondern meldet „bitte neu koppeln“.
- **Grenze des Schutzes:** Der Schlüsselspeicher verschlüsselt den Schlüssel im Ruhezustand, schirmt ihn aber **nicht** gegen andere Programme desselben Benutzers ab. Wer als dein Benutzer Code ausführen kann, kann ihn auslesen. Ein ungewollt weitergegebener Schlüssel lässt sich auf dem Server sperren.
- **Eigenes Datenverzeichnis:** Setzt du `TOKENLORE_DATA_DIR`, nimm nur ein Verzeichnis, das ausschließlich dir gehört und in das kein anderer Benutzer schreiben kann. Ein Ordner außerhalb deines Benutzerprofils (Windows) oder Home-Verzeichnisses (Linux, macOS), etwa unter `/tmp` oder auf einem geteilten Laufwerk, kann lockere Rechte erben oder schon von jemand anderem angelegt sein. Am sichersten ist der Standard.

## Deinstallieren

1. `/tokenlore:disconnect` entfernt Schlüssel und Verbindung auf dem Gerät. Das Gerät danach auch auf dem Server sperren, sonst bleibt sein Schlüssel dort gültig.
2. Plugin entfernen: `/plugin uninstall tokenlore@tokenlore`
3. Datenverzeichnis und Cache-Ordner (siehe oben) löschen.

Die Kennungsdatei `~/.claude/tokenlore-id` bleibt liegen. Installierst du das Plugin später wieder, koppelt dieser Rechner als dasselbe Gerät. Soll er ein neues Gerät werden, lösche auch sie.

## Lizenz

MIT, siehe [LICENSE](LICENSE). Die Programme in `bin/` enthalten Bibliotheken anderer Lizenzen; deren Texte stehen in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES). <!-- doku-ok: Ziele liegen im veröffentlichten Paket -->

tokenlore ist kein Produkt von Anthropic und nicht mit Anthropic verbunden. „Claude“ und „Claude Code“ sind Marken von Anthropic.
