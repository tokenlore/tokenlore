# Ingest-Protokoll, Version 1

Ein Lieferpaket ist ein JSON-Objekt (`Batch`). Es enthält nur Zahlen und Metadaten, nie Nachrichteninhalte, Werkzeug-Ein- oder -Ausgaben, Dateipfade oder Projektnamen im Klartext.

## Grenzen

| Grenze | Wert |
|---|---|
| Paketgröße (JSON) | ≤ 1 048 576 Byte |
| Anfragen pro Paket | ≤ 2000 |
| Sitzungen pro Paket | ≤ 10 000 |
| Felder in `extra` | ≤ 16 |
| Textwert in `extra` | ≤ 64 Zeichen |
| Modellname | `^[A-Za-z0-9._-]{1,64}$` |
| E-Mail | ≤ 254 Zeichen, enthält `@`, keine Steuerzeichen, Unicode-Formatzeichen (Cf, Zl, Zp, Co), Leerzeichen, `<>"` |
| Kennungen (`*_id`) | `^[A-Za-z0-9._:-]{1,128}$`; `request_id` darf leer sein (manche Backends liefern keine) |
| Werkzeugname | `^[A-Za-z0-9._:-]{1,128}$`, ≤ 64 Werkzeuge pro Anfrage, Anzahl 1–10 000; andere Namen sendet das Plugin als `_invalid` |
| `stop_reason` | `^[A-Za-z0-9_.-]{1,32}$` |
| `cc_version` | `^[0-9A-Za-z.+-]{1,32}$` |
| `project_id` | leer oder 64 Hex-Zeichen (klein) |
| Token-Feld | 0 – 100 000 000 |
| `web_search`, `web_fetch` | 0 – 10 000 |
| `utc_offset_min` | −840 – 840 |
| `ts` | nicht vor 2024-01-01 und nicht mehr als 5 min in der Zukunft; das Plugin prüft mit eigener Uhr, der Server mit seiner |
| `extra` | Schlüssel `^[A-Za-z0-9_]{1,64}$`, Werte endliche Zahl oder Text ohne Steuer- und Unicode-Formatzeichen (Cf, Zl, Zp, Co) |
| `device` | `os` ∈ windows, linux, darwin; `arch` ∈ amd64, arm64; `tz` ladbarer IANA-Name, ≤ 64 Zeichen, nicht `Local`; `plugin_version` wie `cc_version` |
| Altbestand | Datum `YYYY-MM-DD` von 2024-01-01 bis einschließlich morgen (UTC); Zahlen 0 – 10^12; ≤ 10 000 Einträge je Liste; Modellname wie oben |
| Gerätename | 1 – 64 Zeichen, gültiges UTF-8, keine Steuer- und Formatzeichen |
| Geräteschlüssel | `^tl_dk_[A-Za-z0-9_-]{43}$` |

Die Prüfungen stehen als `ValidateRequest`, `ValidateSession`, `ValidateAccount`, `ValidateDevice`, `ValidateLegacyModel`, `ValidateLegacyActivity` und `ValidDeviceName` in `validate.go`. Plugin und Server prüfen mit denselben Funktionen aus diesem Paket. Das Plugin lässt ungültige Anfragen vor dem Senden weg (mit Warnung), damit ein einzelner Wert nie den ganzen Abgleich blockiert. Bei einem Konto mit ungültiger Adresse sendet das Plugin das Konto ohne Adresse.

## Batch

| Feld | Typ | Bedeutung |
|---|---|---|
| `v` | int | Protokollversion, derzeit `1` |
| `device` | DeviceInfo | liefernde Maschine |
| `accounts` | Account[] | nur im ersten Paket einer Lieferung |
| `sessions` | Session[] | eine Sitzung steht im Paket ihrer ersten Anfrage |
| `requests` | Request[] | API-Anfragen |
| `legacy_model` | LegacyModelDay[] | Altbestand, nur im ersten Paket |
| `legacy_activity` | LegacyActivityDay[] | Altbestand, nur im ersten Paket |

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

`email_mode: off` ⇒ der Server löscht eine gespeicherte Adresse dieses Kontos.

## Session

| Feld | Typ | Bedeutung |
|---|---|---|
| `session_id` | string | Sitzungs-ID von Claude Code |
| `project_id` | string | Hex-HMAC-SHA256 des Projektordnernamens mit dem Salz des Nutzers; leer, solange das Gerät nicht gekoppelt ist |
| `cc_version` | string | Claude-Code-Version |
| `account_id` | string | Konto, falls bekannt |

## Request

Eindeutig über (`request_id`, `message_id`).

| Feld | Typ | Bedeutung |
|---|---|---|
| `request_id`, `message_id`, `session_id` | string | Kennungen |
| `ts` | string (RFC 3339, UTC, endet auf `Z`) | Zeitpunkt |
| `utc_offset_min` | int | Ortszeit-Versatz des Geräts zu diesem Zeitpunkt in Minuten |
| `model` | string | Modellname |
| `input`, `output`, `cache_create`, `cache_read`, `cache_1h`, `cache_5m`, `thinking` | int ≥ 0 | Token-Zahlen |
| `web_search`, `web_fetch` | int ≥ 0 | Anzahl Websuchen/-abrufe |
| `stop_reason` | string | z. B. `end_turn`, `tool_use` |
| `subagent` | bool | Anfrage eines Subagenten |
| `tools` | {Name: Anzahl} | benutzte Werkzeuge |
| `account_id` | string | Konto, falls bekannt |
| `extra` | {Name: Zahl oder Text} | unbekannte zusätzliche Felder aus `usage`, flach |

**Mehrfachsendung:** Eine Anfrage darf mehrfach gesendet werden; das Plugin schickt die zuletzt gelesene Anfrage einer Datei beim nächsten Lauf erneut, weil ihre Zeilen noch nicht vollständig gewesen sein können. Der Server zählt sie einmal und übernimmt **je Token-Feld und je Werkzeug den höchsten Wert** aller Sendungen. (Grund: In den Sitzungsdateien sind frühe Zeilen einer Anfrage Streaming-Zwischenstände mit zu kleinen Werten; derselbe Wert kann auch in Haupt- und Subagent-Datei stehen.)

## LegacyModelDay / LegacyActivityDay

Tageswerte aus `stats-cache.json`, Konto unbekannt. Werden einmal pro Gerät geliefert. Sie überschneiden sich mit Tagen, für die auch Einzelanfragen vorliegen; der Server darf sie nur für Tage (je Gerät und Modell) verwenden, an denen keine Einzelanfragen vorhanden sind, sonst wird doppelt gezählt.

| Feld | Typ |
|---|---|
| `date` | string `YYYY-MM-DD` |
| `model`, `tokens` | string, int (LegacyModelDay) |
| `messages`, `sessions`, `tool_calls` | int (LegacyActivityDay) |
