# Ingest-Protokoll, Version 2

Ein Lieferpaket ist ein JSON-Objekt (`Batch`). Es enthält Zahlen und Metadaten, nie Nachrichteninhalte, Werkzeug-Ein- oder -Ausgaben, Dateipfade, Projektnamen oder eine Projekt-Kennung. Texte stehen nur in den Feldern, die die Tabellen unten als Text ausweisen, etwa Modell- und Werkzeugnamen oder kurze Werte in `usage`.

Jede Anfrage und jeder Tag der Vorgeschichte steht im Archiv des Plugins als signierte Zeile und geht unverändert so an den Server. Der Server nimmt nur Zeilen an, deren Signatur zu einem Signaturschlüssel des liefernden Geräts passt.

## Grenzen

| Grenze | Wert |
|---|---|
| Paketgröße (JSON) | ≤ 1 048 576 Byte |
| Anfragen pro Paket (`requests`) | ≤ 2000 |
| Vorgeschichte-Tage pro Paket (`legacy_days`) | ≤ 10 000 |
| Sitzungen pro Paket | ≤ 10 000 |
| Hülle | JSON-Objekt mit genau den Schlüsseln `z` und `s` |
| `z` | Text, gültiges UTF-8, ≤ 8192 Byte; ein JSON-Objekt (siehe Inhaltszeile), kompakt: kein Leerraum (Leerzeichen, Tab, Zeilenumbruch, Wagenrücklauf) außerhalb von Texten; kein Schlüssel doppelt in einem Objekt, auf keiner Ebene |
| `s` | Base64 (Standardalphabet, mit `=`), genau 88 Zeichen, ergibt 64 Byte |
| Signaturschlüssel (`sign_public_key`, Einträge von `sign_public_keys_previous`) | Base64 (Standardalphabet, mit `=`), genau 44 Zeichen, ergibt 32 Byte |
| `sign_public_keys_previous` | ≤ 16 Einträge |
| Signaturschlüssel je Gerät auf dem Server | ≤ 32 verschiedene (alle je gemeldeten zusammen, auch nach dem Zusammenlegen; der zuletzt gemeldete `sign_public_key` bleibt, darüber fallen die ältesten weg, siehe Koppeln) |
| `sign_public_key_proof` | Base64 (Standardalphabet, mit `=`), genau 88 Zeichen, ergibt 64 Byte |
| Modellname (`model`, `requested_model`, Schlüssel in `models`) | `^[A-Za-z0-9._-]{1,64}$` |
| `effort`, `entrypoint`, `stop_reason` | `^[A-Za-z0-9_.-]{1,32}$` |
| E-Mail | ≤ 254 Zeichen, enthält `@`, keine Steuerzeichen, Unicode-Formatzeichen (Cf, Zl, Zp, Co), Leerzeichen, `<>"` |
| Kennungen (`*_id`, `uuid`, `parent_uuid`) | `^[A-Za-z0-9._:-]{1,128}$`; `request_id` darf leer sein (manche Backends liefern keine) |
| Werkzeugname | `^[A-Za-z0-9._:-]{1,128}$`, ≤ 64 Werkzeuge pro Anfrage, Anzahl 1–10 000; andere Namen schreibt das Plugin als `_invalid` |
| `cc_version`, `plugin_version` | `^[0-9A-Za-z.+-]{1,32}$` |
| `min_plugin_version` | `^[0-9]+\.[0-9]+\.[0-9]+$` |
| Zählfelder in `usage` (Tabelle „Zählwerte“) | ganze Zahl 0 – 100 000 000; `web_search_requests`, `web_fetch_requests` 0 – 10 000 |
| `usage` sonst | JSON-Objekt; höchstens 4 Ebenen tief (`usage` selbst ist Ebene 1); ≤ 256 Werte insgesamt; Schlüssel `^[A-Za-z0-9_]{1,64}$`; Listen ≤ 64 Einträge; Werte endliche Zahl, `true`, `false`, `null`, Text ≤ 64 Zeichen ohne Steuer- und Unicode-Formatzeichen (Cf, Zl, Zp, Co), Objekt oder Liste |
| `utc_offset_min` | −840 – 840 |
| `ts` | `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`, nicht vor 2024-01-01 und nicht mehr als 5 min in der Zukunft; das Plugin prüft mit eigener Uhr, der Server mit seiner |
| `device` | `os` ∈ windows, linux, darwin; `arch` ∈ amd64, arm64; `tz` ladbarer IANA-Name, ≤ 64 Zeichen, nicht `Local`; `plugin_version` wie oben |
| Vorgeschichte-Tag | `date` `YYYY-MM-DD` von 2024-01-01 bis einschließlich morgen (UTC); Zahlen 0 – 10^12; ≤ 64 Modelle je Tag |
| `complete_before` | `^[0-9]{4}-[0-9]{2}-[0-9]{2}$` (10 Zeichen) und ein gültiges Kalenderdatum; darf in der Zukunft liegen |
| Gerätename | 1 – 64 Zeichen, gültiges UTF-8, keine Steuer- und Formatzeichen |
| Geräteschlüssel | `^tl_dk_[A-Za-z0-9_-]{43}$` |
| `device_source`, `server_id` | `^[0-9a-f]{64}$` (genau 64 Hex-Zeichen, klein) |

Plugin und Server prüfen jedes Paket nach diesen Regeln, jeder mit einer eigenen Umsetzung; diese Datei ist für beide maßgeblich. Die Umsetzung des Plugins steht in `protocol/validate.go` im Repo tokenlore/tokenlore. Das Plugin lässt ungültige oder falsch signierte Zeilen vor dem Senden weg (mit Warnung), damit eine einzelne Zeile nie den ganzen Abgleich blockiert. Bei einem Konto mit ungültiger Adresse sendet das Plugin das Konto ohne Adresse.

## Signierte Zeile

### Hülle

Jede Zeile im Archiv und jeder Eintrag in `requests` und `legacy_days` ist eine Hülle:

| Feld | Typ | Bedeutung |
|---|---|---|
| `z` | string | die Inhaltszeile: ein JSON-Objekt als Text, kompakt in einer Zeile |
| `s` | string | Ed25519-Signatur über die Inhaltszeile, Base64 |

**Signieren:** Ed25519 nach RFC 8032 (reines Ed25519, kein Ed25519ph), getrennte Signatur von 64 Byte. Signiert werden die Bytes `tokenlore-archiv-v1` + ein Zeilenumbruch (Byte `0x0A`) + die UTF-8-Bytes des dekodierten Werts von `z`, also der Inhaltszeile selbst, nicht ihrer maskierten Form im JSON der Hülle (dort steht `\"` statt `"`). Die Grenze von 8192 Byte gilt für diesen dekodierten Wert. Der Vorsatz sorgt dafür, dass eine Signatur nie für etwas anderes gilt. Go: `ed25519.Sign`; PHP: `sodium_crypto_sign_verify_detached($s, "tokenlore-archiv-v1\n" . $z, $schluessel)`.

**Lesen:** Der Empfänger dekodiert die Hülle, prüft die Signatur über den dekodierten Text `z` und liest die Felder danach aus genau diesem Text. Es gibt keine nachgebaute Byte-Form: Reihenfolge der Schlüssel und Schreibweise der Zahlen in `z` legt allein das Plugin beim Schreiben fest, und niemand rechnet sie nach. Ins Lieferpaket geht `z` unverändert. Zwei Formregeln prüft der Server zusätzlich, weil JSON-Leser sie verschieden behandeln: `z` ist kompakt (kein Leerraum außerhalb von Texten), und kein Objekt in `z` hat einen Schlüssel doppelt; sonst 400 mit dem Pfad der Zeile. Das Plugin schreibt `z` immer so (Go: `encoding/json` aus Strukturen und Maps).

**Beispielzeile** (eine Anfrage; Testschlüssel, nur für Beispiele und Tests):

```json
{"z":"{\"kind\":\"request\",\"request_id\":\"req_011CBeispiel0000000001\",\"message_id\":\"msg_01Beispiel000000000001\",\"session_id\":\"0d6c5e7a-1f2b-4c3d-9e8f-0a1b2c3d4e5f\",\"uuid\":\"5b1c2d3e-4f50-4617-8293-a4b5c6d7e8f9\",\"parent_uuid\":\"4a0b1c2d-3e4f-4506-9718-293a4b5c6d7e\",\"ts\":\"2026-10-23T21:30:00Z\",\"utc_offset_min\":120,\"model\":\"claude-opus-5-5\",\"requested_model\":\"claude-opus-5-5\",\"effort\":\"medium\",\"entrypoint\":\"cli\",\"cc_version\":\"2.1.309\",\"stop_reason\":\"tool_use\",\"subagent\":false,\"tools\":{\"Bash\":1,\"Read\":1},\"account_id\":\"6f1e2d3c-4b5a-4968-8776-655443322110\",\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":300,\"cache_read_input_tokens\":4000,\"output_tokens\":200,\"output_tokens_details\":{\"thinking_tokens\":50},\"server_tool_use\":{\"web_search_requests\":1,\"web_fetch_requests\":0},\"service_tier\":\"standard\",\"cache_creation\":{\"ephemeral_1h_input_tokens\":300,\"ephemeral_5m_input_tokens\":0},\"inference_geo\":\"global\",\"iterations\":[],\"speed\":\"standard\"}}","s":"6bNs4D9yjhmQ7DH+W2ylnrS58miWuqX2jVFYzUfN1BOWO3+lIqsxfFv/vfRcA/ugt8nG8X6aCPVEOGX21D9FDw=="}
```

Ein Vorgeschichte-Tag mit demselben Schlüssel:

```json
{"z":"{\"kind\":\"day\",\"date\":\"2026-03-14\",\"models\":{\"claude-opus-5-5\":182000,\"claude-haiku-4-5\":4100},\"activity\":{\"messages\":240,\"sessions\":3,\"tool_calls\":57},\"complete_before\":\"2026-10-20\"}","s":"dlyp5qNc5zqK1sfBwAE1iQjWOhCDb/2LL7IWwo+uJm0Znuf2a5BopXYaZkJcFf5nlo+mLnlAgE3nJCFmu71ACw=="}
```

Testschlüssel: privater Seed (hex) `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f`, öffentlicher Schlüssel (Base64) `A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=`. Beide Signaturen sind gültig; ändert sich ein Byte von `z`, ist sie es nicht mehr. Kennungen und Zahlen sind erfunden.

### Inhaltszeile: Anfrage

`kind` ist `request`. Eindeutig über (`request_id`, `message_id`), über alle Benutzer. Eine Anfrage steht im Archiv genau einmal und erst, wenn sie fertig ist: In derselben Protokolldatei folgt eine Zeile einer anderen Anfrage mit späterem Zeitpunkt, die Datei hat sich seit mindestens 5 Minuten nicht geändert, oder die Sitzung ist beendet (Hook `SessionEnd`; das gilt auch für die Dateien ihrer Subagenten). Tauchen danach noch Zeilen einer archivierten Anfrage auf, bleibt die Archivzeile unverändert.

Die Inhaltszeile enthält nur die Schlüssel dieser Tabelle; ein anderer Schlüssel ist ein Fehler. Optionale Felder fehlen ganz, wenn das Protokoll den Wert nicht hat; ein optionaler Text (`uuid`, `parent_uuid`, `requested_model`, `effort`, `entrypoint`, `cc_version`, `stop_reason`) fehlt auch, wenn er seine Grenze verletzt. Verletzt ein anderes Feld seine Grenze oder wird die Zeile länger als 8192 Byte, übernimmt das Plugin die Anfrage nicht und zählt sie als Warnung.

| Feld | Typ | Pflicht | Grenze | Herkunft im Protokoll von Claude Code |
|---|---|---|---|---|
| `kind` | string | ja | `request` | – |
| `request_id` | string | ja | Kennung, darf leer sein | `requestId` |
| `message_id` | string | ja | Kennung | `message.id` |
| `session_id` | string | ja | Kennung | `sessionId` |
| `uuid` | string | nein | Kennung | `uuid` der ersten Protokollzeile der Anfrage |
| `parent_uuid` | string | nein | Kennung | `parentUuid` der ersten Protokollzeile der Anfrage |
| `ts` | string | ja | siehe `ts` | `timestamp` der ersten Protokollzeile, in UTC |
| `utc_offset_min` | int | ja | −840 – 840 | Ortszeit-Versatz des Geräts zu diesem Zeitpunkt in Minuten |
| `model` | string | ja | Modellname, nicht `<synthetic>` | `message.model` |
| `requested_model` | string | nein | Modellname | `requestedModel` |
| `effort` | string | nein | wie `effort` | `effort` |
| `entrypoint` | string | nein | wie `entrypoint` | `entrypoint`, z. B. `cli` |
| `cc_version` | string | nein | wie `cc_version` | `version` (Claude-Code-Version) |
| `stop_reason` | string | nein | wie `stop_reason` | letzter nicht leerer `message.stop_reason` |
| `subagent` | bool | ja | – | Anfrage eines Subagenten (Datei unter `subagents/` oder `isSidechain: true`) |
| `tools` | {Name: Anzahl} | nein | Werkzeugname, Anzahl | Namen der `tool_use`-Blöcke aus `message.content` mit Anzahl; nur die Namen, nie die Eingaben |
| `account_id` | string | nein | Kennung | eine Sitzung hat genau ein Konto: hat die Sitzung schon ein Konto im Archiv (gleicher oder voriger Monat), dieses; sonst das Konto beim Sitzungsstart (`sitzungen.jsonl`), sonst `ownerAccountUuid`, sonst das Konto der Sitzung, die archiviert. Konto einer Sitzung: das, das die Desktop-App in `CLAUDE_CODE_ACCOUNT_UUID` übergibt, sonst `oauthAccount.accountUuid` aus `.claude.json` |
| `usage` | object | ja | siehe `usage` | der Block `message.usage` der Protokollzeile dieser Anfrage mit dem größten `output_tokens`, bei Gleichstand der letzten |

**`usage`** ist der ganze Block aus dem Protokoll, damit neue Token-Arten ohne neues Plugin erhalten bleiben. Vor dem Signieren bringt das Plugin ihn in die Grenzen: Texte über 64 Zeichen werden auf 64 Zeichen gekürzt, Listen über 64 Einträge auf 64 Einträge; Schlüssel mit anderem Muster, Objekte und Listen jenseits der vierten Ebene, Werte über 256 hinaus (gezählt in alphabetischer Reihenfolge der Schlüssel, ein Objekt oder eine Liste vor ihrem Inhalt), Texte mit Steuer- und Formatzeichen und nicht endliche Zahlen fallen weg, ebenso ein Zählwert `null` (er gilt dann als 0). Eine Zeile im Archiv verletzt die Grenzen deshalb nie.

**Zählwerte:** Plugin und Server lesen die Zahlen einer Anfrage so aus `usage`; fehlt ein Wert, gilt 0:

| Zählwert | Pfad in `usage` |
|---|---|
| `input` | `input_tokens` |
| `output` | `output_tokens` |
| `cache_create` | `cache_creation_input_tokens` |
| `cache_read` | `cache_read_input_tokens` |
| `cache_1h` | `cache_creation.ephemeral_1h_input_tokens` |
| `cache_5m` | `cache_creation.ephemeral_5m_input_tokens` |
| `thinking` | `output_tokens_details.thinking_tokens` |
| `web_search` | `server_tool_use.web_search_requests` |
| `web_fetch` | `server_tool_use.web_fetch_requests` |

Ein Zählwert, der da ist, aber keine ganze Zahl in seiner Grenze ist, ist ein Fehler. Die übrigen Schlüssel der obersten Ebene von `usage` mit Zahl oder Text als Wert (etwa `service_tier`, `speed`, `inference_geo`) gelten als `extra`: alphabetisch, höchstens 16.

**Nie in einer Inhaltszeile:** `message.content` (außer den Werkzeugnamen), `wireToolInputs`, `wireIngestContext`, `cwd`, `gitBranch` und jedes andere Feld außerhalb der Tabelle. Neue Felder in den Protokollen nimmt das Plugin nicht auf, es meldet nur ihren Namen in `status`: Schlüssel der obersten Ebene und von `message` (als `message.<Name>`) in Zeilen einer Anfrage, die weder auf seiner Liste bekannter noch auf seiner Sperrliste stehen; Namen mit anderen Zeichen als `A–Z`, `a–z`, `0–9`, `_` und `-` übergeht es, längere als 64 Zeichen kürzt es.

### Inhaltszeile: Vorgeschichte-Tag

`kind` ist `day`. Ein Tageswert aus `stats-cache.json` für einen Tag, an dem es keine Einzelanfrage gibt, verglichen nach dem lokalen Datum wie in `stats-cache.json`. Das Plugin übernimmt die Vorgeschichte einmal ins Archiv. Konto unbekannt.

| Feld | Typ | Pflicht | Grenze | Bedeutung |
|---|---|---|---|---|
| `kind` | string | ja | `day` | – |
| `date` | string | ja | siehe Vorgeschichte-Tag | lokales Datum aus `stats-cache.json` |
| `models` | {Modellname: Tokens} | nein | ≤ 64 Modelle, Tokens 0 – 10^12 | Tokens je Modell an diesem Tag |
| `activity` | object | nein | Felder `messages`, `sessions`, `tool_calls`, je 0 – 10^12, alle drei Pflicht | Aktivität an diesem Tag |
| `complete_before` | string | nein | siehe `complete_before` | `lastComputedDate` aus `stats-cache.json` beim Übernehmen |

Mindestens eines von `models` und `activity` ist da. Andere Schlüssel sind ein Fehler.

## Batch

| Feld | Typ | Bedeutung |
|---|---|---|
| `v` | int | Protokollversion, `2` |
| `device` | DeviceInfo | liefernde Maschine |
| `accounts` | Account[] | nur im ersten Paket einer Lieferung |
| `sessions` | Session[] | eine Sitzung steht im Paket ihrer ersten Anfrage |
| `requests` | Hülle[] | Anfragen, je Hülle eine Inhaltszeile mit `kind: request` |
| `legacy_days` | Hülle[] | Vorgeschichte-Tage, je Hülle eine Inhaltszeile mit `kind: day` |

Eine Hülle in `requests` mit `kind: day` oder in `legacy_days` mit `kind: request` ist ein Fehler.

## DeviceInfo

| Feld | Typ | Bedeutung |
|---|---|---|
| `os` | string | `windows`, `linux`, `darwin` |
| `arch` | string | `amd64`, `arm64` |
| `tz` | string | IANA-Zeitzone, z. B. `Europe/Berlin` |
| `plugin_version` | string | Version des Plugins |

## Account

| Feld | Typ | Bedeutung |
|---|---|---|
| `account_id` | string | `accountUuid` des Claude-Kontos |
| `email_mode` | string | `off`, `short` oder `full` |
| `email` | string | nur bei `short` (gekürzt, z. B. `ma…@beispiel.de`) oder `full` |

Die volle Adresse steht im Archiv einmal je Konto in `konten.jsonl`; die eingestellte Stufe entscheidet beim Senden, was davon in `email` geht. `email_mode: off` ⇒ der Server löscht eine gespeicherte Adresse dieses Kontos.

## Session

| Feld | Typ | Bedeutung |
|---|---|---|
| `session_id` | string | Sitzungs-ID von Claude Code |
| `cc_version` | string | Claude-Code-Version |
| `account_id` | string | Konto, falls bekannt |

Sitzungen haben keine eigene Archivzeile und keine Signatur: Das Plugin leitet sie aus den Inhaltszeilen der Lieferung ab, je `session_id` einmal, `cc_version` und `account_id` aus der Anfrage dieser Sitzung mit dem frühesten `ts`.

Eine Sitzung liegt auf dem Server genau einmal, über alle Benutzer: Sie gehört dem Benutzer, der sie zuerst geliefert hat (als Sitzung oder mit einer Anfrage darin). Liefert ein anderer Benutzer dieselbe `session_id`, übergeht der Server die Sitzung und alle Anfragen darin (siehe **Vorrang**).

Ein Konto wird bei einem Benutzer nur gespeichert, wenn er eine eigene Anfrage oder Sitzung mit dieser `account_id` hat (schon gespeichert oder im selben Paket). Konten im ersten Paket einer Lieferung, deren eigene Anfragen erst in einem späteren Paket kommen, entstehen deshalb mit der nächsten Lieferung, die die Kontenliste schickt.

## Prüfregeln des Servers

Der Server prüft eine Lieferung in dieser Reihenfolge und weist bei jedem Verstoß die ganze Lieferung ab, ohne etwas zu speichern:

1. Geräteschlüssel (401, siehe Liefern).
2. **Mindestversion:** Ist `device.plugin_version` älter als die Mindestversion des Servers, antwortet er mit 426 (siehe Mindestversion).
3. Paketform und Grenzen (400).
4. **Signatur je Zeile:** Jede Hülle in `requests` und `legacy_days` braucht eine Signatur `s`, die zu einem Signaturschlüssel des liefernden Geräts passt (siehe Signaturschlüssel eines Geräts). Fehlt `s`, ist es kein gültiges Base64 von 64 Byte oder passt es zu keinem Schlüssel, antwortet er mit 400 und dem Pfad der Hülle, etwa `requests[3].s`. Eine nachträglich geänderte Zeile, eine Zeile mit fremdem Schlüssel oder ohne Signatur kommt so nie in die Datenbank.
5. **Inhaltszeile:** `z` ist gültiges JSON, ein Objekt mit genau den erlaubten Schlüsseln und Werten in ihren Grenzen; sonst 400 mit Pfad, etwa `requests[3].z.usage.input_tokens`.

Danach speichert er je Anfrage und je Vorgeschichte-Tag die Inhaltszeile `z` und die Signatur `s` unverändert, dazu die daraus gelesenen Werte.

**Signaturschlüssel eines Geräts:** Der Server kennt je Gerät die Signaturschlüssel, die es beim Koppeln gemeldet hat, als `sign_public_key` oder in `sign_public_keys_previous`, auch die an gesperrten Geräteschlüsseln, höchstens 32 (siehe Koppeln, „Obergrenze je Gerät“).

Ein Signaturschlüssel gehört genau einem Gerät, über alle Geräte und Benutzer des Servers. Einen früheren Schlüssel, der schon einem anderen Gerät gehört, übernimmt der Server nicht; `sign_public_key` mit Besitznachweis bekommt immer das koppelnde Gerät (siehe Koppeln). <!-- doku-ok: Fachbegriff früherer Signaturschlüssel -->

Eine Zeile gilt, wenn einer der Schlüssel des Geräts passt; zuerst prüft der Server den Schlüssel, der am liefernden Geräteschlüssel gemeldet wurde. Beim Zusammenlegen zweier Geräte behält das Ziel die Signaturschlüssel beider, auch dann gilt die Obergrenze. Zeilen werden nie neu signiert: Alte Zeilen bleiben mit dem alten Schlüssel gültig.

**Wiederholte Lieferung:** Kommt eine Anfrage, die der Server schon gespeichert hat, erneut (gleiche `request_id` und `message_id`, vom selben Benutzer), ändert er nichts: Die zuerst gespeicherte Zeile bleibt samt Signatur, auch wenn die neue Zeile andere Zahlen trägt. Die Antwort zählt sie in `known_requests`. Dasselbe gilt für einen Vorgeschichte-Tag, den das Gerät schon hat: Die erste Zeile bleibt als Ganzes, ein Maximum je Feld gibt es nicht. Gespeicherte Werte und Signatur gehören immer zu genau einer Zeile.

**Vorrang:** Jede Anfrage liegt auf dem Server genau einmal. Wer sie zuerst liefert, besitzt sie; Sendungen eines anderen Benutzers ändern an ihr nichts. Gehört die Sitzung schon einem anderen Benutzer, übergeht der Server auch neue Anfragen darin. Das Übergangene wird nicht gespeichert und nicht abgelehnt: Die Lieferung gilt als erfolgreich, die Antwort nennt nur die Anzahl (`elsewhere_requests`), nie Benutzer, Gerät oder Zahlen des anderen. Dasselbe Gerät oder ein anderes Gerät desselben Benutzers ist kein anderer Benutzer: Dort gilt die wiederholte Lieferung.

### Vorgeschichte auf dem Server

Der Server verwendet einen Vorgeschichte-Tag nur für Tage (je Gerät und lokalem Datum), an denen dieses Gerät keine Einzelanfragen geliefert hat; das lokale Datum einer Anfrage ergibt sich aus `ts` und `utc_offset_min`. Sonst würde doppelt gezählt.

**Gleiche Tage anderer Geräte:** Hat das liefernde Gerät für `date` noch keine Zeile und hat ein anderes Gerät, gleich welchen Benutzers, für denselben Tag genau dieselben `models` (gleiche Modelle mit gleichen Tokens), weist der Server diesen Tag ab: Er ist dieselbe Vorgeschichte, etwa von einem kopierten `~/.claude`. Ein Tag ohne `models` oder nur mit Tokens 0 wird nie abgewiesen. Der Rest der Lieferung wird gespeichert; die Antwort nennt die abgewiesenen Tage (`legacy_rejected_days`). Verschiedene Werte am selben Tag auf zwei Geräten zählen beide. Werden zwei Geräte mit demselben Tag zusammengelegt, bleibt die Zeile mit der größeren Summe der Tokens in `models` als Ganzes samt Signatur.

**Abgeschlossene Tage:** `complete_before` sagt, bis wohin `stats-cache.json` beim Übernehmen fertig gerechnet war. Ein Tag gilt als abgeschlossen, wenn er vor diesem Datum und vor dem heutigen Tag (UTC) liegt; ein Datum in der Zukunft wirkt also nur bis heute. Fehlt das Feld, gilt der Tag nicht als abgeschlossen. Ein einmal abgeschlossener Tag bleibt abgeschlossen.

## Server-Kennung

`GET /api/plugin/info`, öffentlich, ohne Geräteschlüssel, mit Grenze je IP-Adresse.

Antwort 200, JSON:

- `server_id` (string): Kennung dieses Servers, 64 Hex-Zeichen, klein (32 zufällige Byte). Sie entsteht bei der Einrichtung, steht in der Datenbank und ändert sich nie; ein Umzug mit derselben Datenbank behält sie.
- `min_plugin_version` (string): die älteste Plugin-Version, die der Server annimmt, `x.y.z`

Fehler: 429 bei zu vielen Abrufen von derselben IP-Adresse, mit `Retry-After`; 503 bei alter Datenbank (siehe „Alte Datenbank“). Ohne Antwort 200 koppelt das Plugin nicht.

## Mindestversion

Die Mindestversion steht fest im Code des Servers. Versionen werden nach den drei Zahlen `x.y.z` am Anfang verglichen, Zahl für Zahl; ein Zusatz ab `-` oder `+` zählt nicht. Eine `plugin_version`, die nicht mit `x.y.z` beginnt, gilt als zu alt.

**Fehler 426** bei Koppeln und Liefern, wenn die gemeldete `plugin_version` älter ist:

`{"error":"plugin_outdated","min_plugin_version":"x.y.z"}`

Das Plugin zeigt dann „Bitte tokenlore auf mindestens x.y.z aktualisieren“ mit dem Befehl dazu; die Daten bleiben im Archiv und gehen nach dem Aktualisieren raus.

## Alte Datenbank

Stammt die Datenbank des Servers von einer Version vor dem Archiv, antwortet jeder Aufruf unter `/api/` (auch Server-Kennung, Koppeln und Liefern) mit 503:

`{"error":"<Meldung für Menschen>","reason":"old_database"}`

Erkannt wird der Fall allein an `reason`; den Wortlaut von `error` wertet das Plugin nicht aus. Es zeigt `error` bereinigt (ohne Steuerzeichen, gekürzt) bei `connect`, `sync`, in `status` unter „Letzter Fehler“ und beim Sitzungsstart, solange der letzte Abgleich daran scheiterte, und versucht es nicht sofort erneut; fehlt `error`, zeigt es einen festen Text. Ein 503 ohne `reason` `old_database` heißt: Server ausgelastet, später erneut versuchen (mit `Retry-After`).

## Koppeln

Vor dem Koppeln holt das Plugin `GET /api/plugin/info` und bildet daraus `device_source`. Dann `POST /api/plugin/pair` mit einem JSON-Objekt; das Plugin tauscht damit einen einmaligen Kopplungscode gegen einen Geräteschlüssel. Prüfregeln stehen unter „Grenzen“.

| Feld | Typ | Bedeutung |
|---|---|---|
| `code` | string | Kopplungscode aus der Webseite, 6 Ziffern |
| `device_name` | string | Gerätename |
| `device_name_explicit` | bool, optional | `true`, wenn der Name ausdrücklich gewählt wurde (`connect --name`); fehlt das Feld, gilt `false` |
| `os` | string | `windows`, `linux`, `darwin` |
| `arch` | string | `amd64`, `arm64` |
| `plugin_version` | string | Version des Plugins; älter als die Mindestversion ⇒ 426 |
| `device_source` | string | Kennung des Geräts für diesen Server (siehe unten) |
| `sign_public_key` | string | öffentlicher Ed25519-Schlüssel, mit dem dieses Gerät seine Zeilen signiert, Base64 |
| `sign_public_key_proof` | string | Besitznachweis für `sign_public_key`: Ed25519-Signatur über `server_id` und Kopplungscode, Base64 (siehe unten) |
| `sign_public_keys_previous` | string[], optional | öffentliche Teile früherer, ersetzter Signaturschlüssel dieses Geräts, je Base64; fehlt das Feld, gilt eine leere Liste |

**Berechnung von `device_source`:** HMAC-SHA256, Schlüssel ist die Kennung aus der Kennungsdatei `tokenlore-id` im Claude-Konfigurationsordner als Text (32 Hex-Zeichen, klein), Nachricht die `server_id` aus `GET /api/plugin/info` als Text (64 Hex-Zeichen, klein); gesendet wird das Ergebnis als 64 Hex-Zeichen, klein. Ohne die Kennung ist es nicht zurückzurechnen, und jeder Server bekommt ein anderes. Zieht der Server mit seiner Datenbank auf eine andere Adresse um, bleibt `device_source` gleich. Der Server speichert `device_source` nicht roh, sondern je Benutzer als Hex-HMAC-SHA256 mit einem eigenen Salz des Benutzers als Schlüssel, das den Server nie verlässt; derselbe Rechner ist so unter zwei Konten desselben Servers in der Datenbank nicht verknüpfbar.

**Signaturschlüssel:** Das Plugin erzeugt das Schlüsselpaar beim ersten Koppeln und behält es bei jedem weiteren Koppeln, auch nach `disconnect`. Den öffentlichen Teil legt es schon beim Erzeugen ab, weil er sich aus einem verlorenen privaten Schlüssel nicht ableiten lässt.

Nur wenn der private Schlüssel fehlt, entsteht beim Koppeln ein neuer. Den öffentlichen Teil des ersetzten merkt sich das Plugin und schickt ihn ab dann in `sign_public_keys_previous` mit, damit auch ein neuer Server alte Zeilen prüfen kann.

Der Server speichert `sign_public_key` am neuen Geräteschlüssel und jeden Eintrag von `sign_public_keys_previous` am Gerät; Schlüssel, die dieses Gerät schon hat, speichert er nicht doppelt.

- `sign_public_key` kommt mit Besitznachweis und geht immer an das koppelnde Gerät. Hat ein anderes Gerät, gleich welchen Benutzers, den Schlüssel (als früheren oder mit Nachweis), nimmt der Server ihn dort weg: Der neueste gültige Nachweis gewinnt. Zeilen mit diesem Schlüssel nimmt er danach nur vom koppelnden Gerät an; das andere holt ihn mit erneutem Koppeln zurück. <!-- doku-ok: Fachbegriff früherer Signaturschlüssel -->
- Einträge in `sign_public_keys_previous` kommen ohne Nachweis. Gehört einer schon einem anderen Gerät, gleich welchen Benutzers, übernimmt der Server ihn nicht, koppelt trotzdem und nennt ihn in `sign_keys_refused`; Zeilen mit diesem Schlüssel nimmt er von diesem Gerät nicht an.

**Obergrenze je Gerät:** Der Server hält je Gerät höchstens 32 verschiedene Signaturschlüssel (alle je gemeldeten zusammen), auch nach dem Zusammenlegen; er prüft jede gelieferte Zeile gegen alle Schlüssel des Geräts.

- Die Grenze weist keinen Schlüssel ab. Der zuletzt gemeldete `sign_public_key` (am neuesten aktiven Geräteschlüssel) bleibt immer; alle übrigen zählen als frühere, nach dem Zusammenlegen auch die an anderen aktiven Geräteschlüsseln. <!-- doku-ok: Fachbegriff früherer Signaturschlüssel -->
- Sind es nach dem Koppeln oder Zusammenlegen mehr als 32, fallen die ältesten weg, gemessen am Koppeln, bei dem der Server sie gespeichert hat (einen `sign_public_key` an jedem Geräteschlüssel, an dem er stand, also zuletzt beim letzten dieser Koppeln).
- Beim selben Koppeln gilt ein `sign_public_key` als neuer als die Einträge von `sign_public_keys_previous`, und unter diesen der zuletzt gesendete als der neueste (das Plugin sendet die ältesten zuerst).
- Neue Zeilen mit einem weggefallenen Schlüssel nimmt der Server von diesem Gerät nicht an; gespeicherte Zeilen bleiben.

**Besitznachweis:** `sign_public_key_proof` zeigt, dass das Gerät den privaten Teil von `sign_public_key` hat; wer einen fremden öffentlichen Schlüssel nur kennt, kann ihn so nicht als seinen melden und das echte Gerät aussperren. Es ist die Ed25519-Signatur (64 Byte, Base64 mit `=`) über die Bytes `tokenlore-pair-v2` + ein Zeilenumbruch (Byte `0x0A`) + die `server_id` aus `GET /api/plugin/info` (64 Hex-Zeichen, klein) + ein Zeilenumbruch + den Kopplungscode `code`, wie gesendet, mit dem privaten Teil von `sign_public_key`.

Der Nachweis gilt nur für diesen einen Server und nur einmal: Wer ihn auf einem Server mitliest, kann ihn auf keinem anderen verwenden.

Der Server merkt sich jeden angenommenen Nachweis (Signaturschlüssel und Code) und weist eine Wiederholung mit 400 und `path` `sign_public_key_proof` ab, auch wenn er denselben Code später neu vergibt; der Code bleibt dabei gültig. Der Code allein reicht dafür nicht, weil es nur 10^6 Codes gibt und ein eingelöster wieder frei wird. Derselbe Schlüssel koppelt mit neuem Code und neuem Nachweis wie gewohnt. Gegen das Koppeln mit einem falschen Server schützt er nicht: Meldet ein Server die `server_id` eines anderen, signiert das Plugin den Nachweis für diesen anderen, und der falsche Server kann ihn dort mit einem eigenen Code verwenden. Das echte Gerät holt den Schlüssel dann mit erneutem Koppeln zurück, weil der neueste gültige Nachweis gewinnt.

Der Server prüft ihn direkt nach dem Format von `sign_public_key` und vor dem Einlösen des Codes; fehlt er oder passt er nicht, antwortet er 400 mit `path` `sign_public_key_proof`, und der Code bleibt gültig.

PHP: `sodium_crypto_sign_verify_detached($nachweis, "tokenlore-pair-v2\n" . $server_id . "\n" . $code, $schluessel)`. Beispiel mit dem Testschlüssel der Beispielzeile, der `server_id` `0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef` und dem Code `123456`: `POAMORdew63MOoD3kpQ0xtbrIjADFFCt53mdL1X+2vwbfJ9Lj8f0zFXlqy7NHy9PKXVUO4cXuzgOpMU/VHZyDA==`.

Ein Rechner bleibt beim selben Benutzer ein Gerät: Kennt der Server `device_source` schon bei einem Gerät dieses Benutzers, koppelt er dieses Gerät neu (neuer Geräteschlüssel, alle alten Geräteschlüssel gesperrt, eine Sperre des Geräts aufgehoben, Betriebssystem und Architektur übernommen, Daten und bekannte Signaturschlüssel unverändert; den Namen ersetzt `device_name` nur bei `device_name_explicit: true`, sonst bleibt der bisherige, etwa auf der Webseite vergebene Name); sonst legt er ein neues Gerät an. Ein Kopplungscode für ein bestehendes Gerät koppelt genau dieses Gerät neu und übernimmt eine noch unbekannte Kennung.

Antwort 200, JSON:

- `device_id` (int): Nummer des Geräts
- `device_key` (string): Geräteschlüssel für das Liefern
- `ca_fingerprint` (string): Fingerabdruck der Stamm-CA, leer ohne hinterlegtes Zertifikat
- `repaired` (bool): `true`, wenn ein bestehendes Gerät neu gekoppelt wurde, sonst `false`
- `sign_keys_refused` (Liste von string): gemeldete Signaturschlüssel (Base64, wie gesendet), die nicht übernommen wurden, weil sie einem anderen Gerät gehören (siehe „Signaturschlüssel“); leer, wenn keine. Das Plugin zeigt dann eine Warnung (ist es der aktuelle Schlüssel) oder einen Hinweis (ist es ein früherer). Frühere Schlüssel daraus merkt es sich in `state.json` als `sign_keys_refused`, bis zum nächsten Koppeln, und sendet Archivzeilen mit ihnen nicht (siehe „Liefern aus dem Archiv“). <!-- doku-ok: Fachbegriff aktueller/früherer Signaturschlüssel -->

Das Plugin speichert `server_id` mit der Verbindung.

Fehler:

- 400: Feld fehlt, hat den falschen Typ (`device_name_explicit` kein Boolean, `sign_public_keys_previous` keine Liste von Texten, sonst kein Text) oder verletzt eine Prüfregel; ein fehlender oder falscher Besitznachweis mit `path` `sign_public_key_proof`, der Code bleibt dann gültig
- 401: Kopplungscode unbekannt, abgelaufen oder schon eingelöst
- 409 `{"error":"source_taken"}`: Der Code gilt für ein bestehendes Gerät, `device_source` gehört aber einem anderen Gerät desselben Benutzers; nichts wird geändert
- 426 `{"error":"plugin_outdated","min_plugin_version":"x.y.z"}`: `plugin_version` ist älter als die Mindestversion; der Code bleibt gültig
- 503 mit `reason` `old_database`: Datenbank des Servers zu alt (siehe „Alte Datenbank“)

## Liefern

**Wann:** Das Plugin liefert bei jedem Sitzungsstart, nach `connect`, mit `sync` und am Sitzungsende (Hook `SessionEnd`), dort nur, wenn der letzte Abgleich mindestens 5 Minuten zurückliegt. Gleicht der Hook am Sitzungsende selbst ab, kommt die letzte Anfrage der beendeten Sitzung sofort mit. Sonst kommen die übrigen Anfragen der Sitzung mit dem nächsten Abgleich und ihre letzte Anfrage mit dem ersten Abgleich, der mindestens 5 Minuten nach der letzten Zeile ihrer Sitzungsdatei läuft; einen verschobenen Abgleich gibt es nicht. Keine Anfrage geht dabei verloren, und keine zählt doppelt, auch nicht, wenn die Sitzung später fortgesetzt wird.

`POST /api/plugin/ingest` mit einem Batch und dem Geräteschlüssel als `Authorization: Bearer <Schlüssel>`. Ist der Schlüssel nicht gültig, antwortet der Server mit 401 und nennt den Grund:

`{"error":"unauthorized","reason":"repaired"|"revoked"|"unknown"}`

- `repaired`: Der Schlüssel ist gesperrt, weil das Gerät anderswo neu gekoppelt wurde.
- `revoked`: Gerät oder Schlüssel wurde in der Geräte-Ansicht gesperrt, oder der Benutzer ist auf dem Server gesperrt.
- `unknown`: Der Schlüssel ist dem Server unbekannt.

Weitere Fehler (siehe Prüfregeln des Servers): 426 `plugin_outdated`; 503 mit `reason` `old_database` (siehe „Alte Datenbank“); 400 `{"error":"<feste Meldung>","path":"<Pfad>"}`, etwa mit Pfad `requests[3].s` bei einer ungültigen Signatur. Eine Meldung oder ein Pfad enthält nie Werte aus dem Paket.

Passt eine Signatur zu keinem Signaturschlüssel des Geräts, steht zusätzlich `"reason":"sign_key_unknown"` im 400. Meist hat dann ein anderes Gerät den Schlüssel beim Koppeln übernommen, weil der neueste Besitznachweis gewinnt (siehe Koppeln). Das Plugin wertet dann den Pfad aus (`requests[i].s` oder `legacy_days[i].s` im abgelehnten Paket) und prüft, mit welchem eigenen Schlüssel diese Zeile signiert ist:

- Mit einem früheren Schlüssel: Das Plugin nimmt ihn in `sign_keys_refused` auf, sondert seine Zeilen aus (siehe „Liefern aus dem Archiv“) und liefert den Rest im selben Abgleich erneut. Das Paket war ganz abgewiesen, also zählt nichts doppelt. Neukoppeln allein hilft nicht, solange das andere Gerät den Schlüssel hält. Legt der Benutzer beide Geräte zusammen, gehört der Schlüssel dem verbleibenden Gerät; das nächste Koppeln leert `sign_keys_refused` und löscht `geliefert.json`, dann gehen die Zeilen mit. <!-- doku-ok: Fachbegriff früherer Signaturschlüssel -->
- Mit dem aktuellen Schlüssel, oder die Zeile ist nicht zuzuordnen: Das Plugin zeigt statt der Meldung des Servers einen eigenen Text mit den Schritten zum Neukoppeln, bei `sync`, in `status` und beim Sitzungsstart, und liefert bis zum Neukoppeln nichts. Erneutes Koppeln holt den Schlüssel zurück. <!-- doku-ok: Fachbegriff aktueller Signaturschlüssel -->

Antwort 200, JSON, alle Felder immer vorhanden:

- `requests`, `legacy_days`, `sessions`, `accounts` (int): Anzahl der Einträge im Paket, wie geliefert (auch Übergangenes und Doppeltes)
- `known_requests` (int): Anfragen dieses Pakets, die dieser Benutzer schon geliefert hatte und die der Server deshalb unverändert ließ
- `flagged_sessions` (Liste von string): Sitzungen des Pakets, die wegen auffällig vieler Anfragen markiert sind, gleich durch welche Lieferung, ohne Sitzungen eines anderen Benutzers, sortiert; die Lieferung gilt trotzdem
- `elsewhere_requests` (int): Anzahl verschiedener Anfragen (je `message_id`/`request_id` einmal) dieses Pakets, die schon einem anderen Benutzer gehören oder in seiner Sitzung liegen und deshalb nicht gezählt wurden; 0, wenn keine
- `legacy_rejected_days` (Liste von string `YYYY-MM-DD`): abgewiesene Vorgeschichte-Tage dieses Pakets, sortiert

Die Antwort nennt nie Benutzer, Gerät oder Zahlen des anderen. Dass etwas übergangen oder abgewiesen wird, verrät nur, dass genau diese Anfrage oder genau diese Werte schon anderswo auf dem Server liegen. Wird eine Lieferung auf mehrere Pakete verteilt, gelten die Zahlen je Paket. Die Webseite zeigt die übergangenen Anfragen und abgewiesenen Tage beim liefernden Gerät als Hinweis an; jede Anfrage und jeder Tag zählt dort je Gerät einmal, auch wenn das Plugin sie erneut schickt.

## Archiv des Plugins

Das Archiv ist die einzige Quelle jeder Lieferung. Es liegt im Datenordner des Plugins unter `archiv/rechner-<kennung>/`, wobei `<kennung>` die Kennung aus `tokenlore-id` ist; das Plugin liefert nur aus dem Ordner seiner eigenen Kennung. Ordner 0700, Dateien 0600.

| Datei | Inhalt |
|---|---|
| `JJJJ-MM.jsonl` | je Zeile eine Hülle mit einer Anfrage; Monat nach UTC von `ts`; wird nur ergänzt |
| `JJJJ-MM.jsonl.gz` | ein abgeschlossener Monat, mit gzip gepackt, danach unveränderlich |
| `JJJJ-MM.jsonl.gz.sig` | Signatur der gepackten Datei: Base64 der Ed25519-Signatur über `tokenlore-archiv-monat-v1` + `0x0A` + die Bytes der `.gz`-Datei, eine Zeile |
| `JJJJ-MM.nachtrag.jsonl` | Hüllen von Anfragen, die erst nach dem Packen ihres Monats fertig wurden; wird nie gepackt |
| `vorgeschichte.jsonl` | je Zeile eine Hülle mit einem Vorgeschichte-Tag; einmal übernommen |
| `konten.jsonl` | je Konto eine Zeile `{"account_id":"…","email":"…"}` mit voller Adresse; `email` fehlt, solange die Adresse unbekannt ist (sie steht nur für Konten auf dem Gerät: das der Desktop-App aus `CLAUDE_CODE_USER_EMAIL` und das aus `.claude.json`); nicht signiert, verlässt das Gerät nur nach der E-Mail-Stufe |
| `sitzungen.jsonl` | je Zeile `{"session_id":"…","account_id":"…","at":"…"}`: das Konto der Sitzung beim Sitzungsstart (Desktop-App, sonst `.claude.json`); höchstens ein Eintrag je Sitzung (eine fortgesetzte Sitzung schreibt keinen neuen); nur angehängt, nicht signiert, verlässt das Gerät nie; Einträge älter als 90 Tage fallen weg, höchstens 10 000 bleiben (die neuesten), aufgeräumt beim Schreiben und im Abgleich, also auch ohne Kopplung |
| `geliefert.json` | Merker „geliefert“, nicht signiert: `zeilen` je Archivdatei (Name ohne `.gz`, etwa `2026-10.jsonl`) die Zahl der erledigten Zeilen vom Anfang an, gezählt im ungepackten Inhalt; `abgewiesene_tage` (vom Server abgewiesene Vorgeschichte-Tage), `beschaedigt` (Zahl ausgesonderter Zeilen), `fremder_schluessel` (Zahl der Zeilen mit einem früheren Signaturschlüssel, den der Server einem anderen Gerät zuordnet), `konten_summe` (SHA-256 der zuletzt gelieferten Kontenliste) | <!-- doku-ok: Fachbegriff früherer Signaturschlüssel -->

Eine Zeile im Archiv wird nie geändert. Ein abgebrochener Schreibvorgang hinterlässt höchstens eine halbe letzte Zeile; das Plugin schneidet sie beim nächsten Lauf ab und schreibt die Zeile neu.

**Liefern aus dem Archiv:** Geliefert wird in dieser Reihenfolge: `vorgeschichte.jsonl`, dann je Monat aufsteigend die Monatsdatei (oder die `.gz`) und der Nachtrag; aus jeder Datei die Zeilen ab der Zahl in `geliefert.json`.
Vor dem Senden prüft das Plugin jede Hülle selbst wie der Server: Signatur gegen den aktuellen oder einen früheren öffentlichen Signaturschlüssel, Inhaltszeile mit genau den erlaubten Schlüsseln in ihren Grenzen. Eine Zeile, die das nicht besteht, geht nicht mit, gilt als erledigt und zählt in `beschaedigt`; `status` nennt die Zahl. <!-- doku-ok: Fachbegriff aktueller/früherer Signaturschlüssel -->
Ebenso geht eine Zeile nicht mit, deren Signatur nur zu einem früheren Schlüssel aus `sign_keys_refused` in `state.json` passt: Sie gilt als erledigt und zählt in `fremder_schluessel`, bleibt aber unverändert im Archiv; `sync`, `status` und `connect` nennen die Zahl mit dem Hinweis, dass diese älteren Zeilen nicht an den Server gehen, und dem Ausweg (Geräte zusammenlegen, danach neu koppeln). <!-- doku-ok: Fachbegriff aktueller/früherer Signaturschlüssel -->
Nach jedem angenommenen Paket rückt `geliefert.json` bis vor die erste ungelieferte Zeile jeder Datei vor; scheitert ein Paket, geht der Rest beim nächsten Abgleich. Neu koppeln löscht nur `geliefert.json`: Dann geht das ganze Archiv noch einmal, jede Zeile einmal, und der Server zählt schon Gespeichertes nicht doppelt (`known_requests`). Ist `geliefert.json` unlesbar, gilt dasselbe.
Die Kontenliste geht im ersten Paket jeder Lieferung mit; ändert sie sich (neues Konto, andere E-Mail-Stufe), auch ohne neue Zeilen.
