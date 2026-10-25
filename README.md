# tokenlore (Claude-Code-Plugin)

Das Plugin liest die lokalen Claude-Code-Sitzungsdateien und schickt **nur Zahlen und Metadaten** an deinen eigenen tokenlore-Server, nie Inhalte. Den Server gibt es im Repo [tokenlore/tokenlore-server](https://github.com/tokenlore/tokenlore-server).

**Inhalt:** [Was wird gesendet?](#was-wird-gesendet) · [Voraussetzungen](#voraussetzungen) · [Installation](#installation) · [Benutzung](#benutzung) · [Ablage und Schlüssel](#ablage-und-schlüssel) · [Deinstallieren](#deinstallieren) · [Lizenz](#lizenz)

## Was wird gesendet?

Bitte vor dem Koppeln lesen. Die Feldliste mit allen Grenzen steht in [`PROTOCOL.md`](PROTOCOL.md) und ist die maßgebliche Quelle.

**Was das Plugin liest:** die Sitzungsdateien von Claude Code unter `~/.claude/projects/` (nur die Nutzungsangaben jeder API-Anfrage), `stats-cache.json` (Tageswerte für den Altbestand) und die Kontokennung aus `~/.claude.json`.

**Was gesendet wird:**

- Pro API-Anfrage: Anfrage-, Nachrichten- und Sitzungs-ID, Zeitpunkt (UTC) und Ortszeit-Versatz, Modellname, Token-Zahlen (Eingabe, Ausgabe, Cache, Denken), Anzahl Websuchen und -abrufe, `stop_reason`, ob die Anfrage von einem Subagenten stammt, die Liste benutzter Werkzeuge mit Anzahl, Konto-ID.
- Pro Sitzung: Sitzungs-ID, Projekt-Kennung (siehe unten), Claude-Code-Version, Konto-ID.
- Pro Gerät: der Gerätename, den du beim Koppeln vergibst, Betriebssystem, Architektur, Zeitzone, Plugin-Version.
- Altbestand: einmalig pro Gerät Tageswerte aus `stats-cache.json` (Datum, Modell, Tokens bzw. Nachrichten, Sitzungen, Werkzeugaufrufe).
- Unbekannte neue Zahlenfelder aus den Nutzungsangaben dürfen in einem flachen Bereich `extra` mitkommen (Zahlen oder kurze Texte, begrenzte Anzahl).

**Was das Gerät nie Richtung tokenlore-Server verlässt:** Nachrichteninhalte, Werkzeug-Eingaben und -Ausgaben, Dateipfade, Projektnamen im Klartext, der Name aus dem Claude-Konto. Warnungen des Plugins (sie können Pfade und Projektordner enthalten) gehen nie an den Server.

**Was Claude mitliest:** Die Ausgabe eines `/tokenlore:…`-Befehls landet in Claudes Kontext. `/tokenlore:status` zeigt deshalb nur die Anzahl der Warnungen. `/tokenlore:status --warnings` zeigt die Texte (Claude liest dann die Pfade mit), `/tokenlore:status --next` das ganze nächste Paket (Claude liest dann Konto-IDs und Werkzeugnamen mit).

**Werkzeugnamen:** Sie werden voll gesendet, auch die von MCP-Servern (`mcp__<server>__<werkzeug>`). Der Servername kann einen Firmen- oder Kundennamen enthalten. Wenn dich das stört, nutze solche Server nicht auf gekoppelten Geräten oder koppele nicht.

**E-Mail-Adresse des Kontos (Standard: aus):** `/tokenlore:email` zeigt die Stufen.

- `off` sendet nichts und löscht eine gespeicherte Adresse auf dem Server.
- `short` sendet eine gekürzte Form (erste zwei Zeichen und Domain); die volle Adresse verlässt das Gerät nicht.
- `full` sendet die volle Adresse.

**Projekt-Kennung:** Ordnernamen können Kunden- oder Firmennamen enthalten. Gesendet wird nur ein HMAC-SHA256 des Ordnernamens mit einem geheimen Salz, das dein Server beim Koppeln ausgibt. Ohne Kopplung bleibt die Kennung leer, und das Plugin liefert nichts.

**Vorschau:** `/tokenlore:status --next` zeigt genau das nächste Paket, das gesendet würde.

## Voraussetzungen

| Was | Wozu | Prüfen |
|---|---|---|
| Claude Code | Plugin installieren und ausführen | `claude --version` |
| Ein tokenlore-Server mit HTTPS | Ziel der Lieferung | `curl -s https://<server>/healthz` gibt `ok` aus (ohne Umschreibregeln `…/index.php/healthz`) |
| Windows/amd64, Linux/amd64, Linux/arm64 oder macOS/arm64 | Fertige Programme gibt es nur dafür | `/tokenlore:status` meldet keinen Plattformfehler |

Das Plugin bringt die fertigen Programme mit (`bin/`, Prüfsummen in `bin/SHA256SUMS`) und lädt keinen Programmcode aus dem Netz nach. Auf anderen Plattformen meldet der Starter das und tut nichts.

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

## Benutzung

### Koppeln

1. Auf der Webseite des Servers unter „Geräte“ einen Kopplungscode erzeugen. Dort steht der fertige Befehl `/tokenlore:connect …` mit Code und Ablaufzeit; ist auf dem Server unter „Team“ → „Server“ ein CA-Zertifikat hinterlegt, steht auch dessen Fingerabdruck (`--ca`) schon darin.
2. Diesen Befehl in Claude Code auf dem Gerät eingeben, z. B.:

   ```
   /tokenlore:connect https://tokenlore.example.org 123456 --name "Mein PC"
   /tokenlore:connect https://tokenlore.lan 123456 --ca <fingerabdruck> --name "Mein PC"
   ```

   Die erste Zeile gilt für einen Server mit öffentlich beglaubigtem Zertifikat (z. B. Let's Encrypt), die zweite für einen Server mit eigener CA. Ohne `--name` ist der Rechnername der Gerätename. Für Leerzeichen im Namen nimmst du doppelte Anführungszeichen. Einfache Anführungszeichen und Apostrophe (`'`) sind in den Angaben nicht erlaubt.
3. Prüfen: `/tokenlore:status` zeigt die Verbindung statt „Nicht verbunden“.

**Achtung:** Tippe die Zeile selbst ab oder prüfe sie genau, und füge nur Zeilen ein, die dein eigener Server ausgegeben hat. Eine fremde Zeile koppelt dein Gerät mit einem fremden Server.

**Server-Adresse:** `https://` ist Pflicht. Ein Pfad ist erlaubt, wenn der Server nicht an der Wurzel liegt, z. B. `https://example.org/tokenlore` oder, bei Webspace ohne Umschreibregeln, `https://example.org/tokenlore/index.php`. Die Aufrufpfade werden angehängt (`…/index.php/api/plugin/ingest`). Query, Fragment und Benutzerteil sind nicht erlaubt.

**Mit `--ca`** (Server mit eigener CA): Das Plugin holt das CA-Zertifikat, vergleicht den Fingerabdruck und vertraut danach nur dieser CA (Kettenprüfung mit Hostnamen-Prüfung, kein Proxy, keine Weiterleitungen). Ein leerer Fingerabdruck wird abgelehnt, und der Server muss in der Kopplung denselben Fingerabdruck bestätigen.

**Ohne `--ca`** (Server mit öffentlich beglaubigtem Zertifikat): Das Plugin vertraut den System-Zertifikaten des Geräts (mit Hostnamen-Prüfung, ohne Proxy, ohne Weiterleitungen) und speichert keine CA. Ein Fingerabdruck in der Kopplungsantwort wird dann nicht geprüft; meldet der Server einen, koppelt das Plugin und weist mit einer Hinweiszeile auf `--ca` hin. Nutzt dein Server eine eigene CA, gib `--ca` an: Ohne den Fingerabdruck ließe sich ein untergeschobener Server nicht erkennen.

**Gespeicherte CA:** Eine mit `--ca` gekoppelte Verbindung vertraut immer nur dieser CA und fällt nie auf die System-Zertifikate zurück.

**Altbestand und Neu-Kopplung:** Der Altbestand aus `stats-cache.json` geht nur einmal raus, auch wenn du das Gerät neu koppelst; der Server speichert ihn je Gerät, unter einer neuen Geräte-ID zählte er sonst doppelt. Als geliefert gilt er, sobald der Server das Paket mit ihm angenommen hat; `connect` meldet das. Koppelst du mit einem anderen oder geleerten Server, hänge `--resend-legacy` an, damit er ihn einmal bekommt. Am selben Server doppelt `--resend-legacy` den Altbestand.

### Befehle

| Befehl | Wirkung |
|---|---|
| `/tokenlore:connect <https://server> <code> [--ca <fingerabdruck>] [--name <gerätename>] [--resend-legacy]` | Gerät koppeln |
| `/tokenlore:sync` | sofort abgleichen |
| `/tokenlore:status [--next] [--warnings]` | Verbindung und Abgleich zeigen, mit `--next` das nächste Paket, mit `--warnings` die Texte der Warnungen |
| `/tokenlore:email [off\|short\|full]` | E-Mail-Stufe zeigen oder setzen |
| `/tokenlore:disconnect` | Gerät trennen |

Bei jedem Sitzungsstart merkt sich der Hook das angemeldete Konto und startet den Abgleich in einem abgekoppelten Prozess. Claude Code wartet darauf nie (Notbremse 2 s, auch ohne Netz).

## Ablage und Schlüssel

- **Datenverzeichnis:** `$TOKENLORE_DATA_DIR`, sonst `<Benutzerkonfigurationsordner>/tokenlore` (Windows `%AppData%\tokenlore`, Linux `~/.config/tokenlore`, macOS `~/Library/Application Support/tokenlore`). Dort liegen der Merkzettel `state.json`, die Verbindung `connection.json`, der Stand des letzten Abgleichs `status.json`, `session-accounts.jsonl` und gegebenenfalls `device.key`.
- **Cache-Ordner:** `<Benutzer-Cache-Ordner>/tokenlore` (Windows `%LocalAppData%\tokenlore`, Linux `~/.cache/tokenlore`, macOS `~/Library/Caches/tokenlore`) mit der Sperrdatei `tokenlore.lock`.
- **Geräteschlüssel:** im Schlüsselspeicher des Systems (Windows-Anmeldeinformationsverwaltung, macOS-Schlüsselbund, Linux Secret Service), sonst in der Datei `device.key` (nur für den Benutzer lesbar) im Datenverzeichnis. Er steht nie in `settings.json`, einer Befehlszeile, Ausgabe oder einem Log.
- **Grenze des Schutzes:** Der Schlüsselspeicher verschlüsselt den Schlüssel im Ruhezustand, schirmt ihn aber **nicht** gegen andere Programme desselben Benutzers ab. Wer als dein Benutzer Code ausführen kann, kann ihn auslesen. Ein ungewollt weitergegebener Schlüssel lässt sich auf dem Server sperren.
- **Eigenes Datenverzeichnis:** Setzt du `TOKENLORE_DATA_DIR`, nimm nur ein Verzeichnis, das ausschließlich dir gehört und in das kein anderer Benutzer schreiben kann. Ein Ordner außerhalb deines Benutzerprofils (Windows) oder Home-Verzeichnisses (Linux, macOS), etwa unter `/tmp` oder auf einem geteilten Laufwerk, kann lockere Rechte erben oder schon von jemand anderem angelegt sein. Am sichersten ist der Standard.

## Deinstallieren

1. `/tokenlore:disconnect` entfernt Schlüssel und Verbindung auf dem Gerät. Das Gerät danach auch auf dem Server sperren, sonst bleibt sein Schlüssel dort gültig.
2. Plugin entfernen: `/plugin uninstall tokenlore@tokenlore`
3. Datenverzeichnis und Cache-Ordner (siehe oben) löschen.

## Lizenz

MIT, siehe [LICENSE](LICENSE). Die Programme in `bin/` enthalten Bibliotheken anderer Lizenzen; deren Texte stehen in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

tokenlore ist kein Produkt von Anthropic und nicht mit Anthropic verbunden. „Claude“ und „Claude Code“ sind Marken von Anthropic.
