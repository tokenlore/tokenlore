// Unabhängige Gegenprobe zu `tokenlore scan --summary` und zum Archiv: zählt eindeutige
// (message.id, requestId)-Paare und summiert die Token-Werte je Modell. Je Anfrage gilt der usage-Block der
// Zeile mit dem größten output_tokens, bei Gleichstand der letzten (frühe Zeilen sind Zwischenstände beim
// Streamen), wie in protocol/doc.md.
// Aufruf: node scripts/crosscheck.mjs <Claude-Konfigurationsordner>
//         node scripts/crosscheck.mjs --archiv <Archivordner rechner-<kennung>>
// Mit --archiv liest es die Hüllen aller Monatsdateien, gepackten Monate und Nachträge und meldet Anfragen,
// die mehr als einmal im Archiv stehen. Signaturen prüft es nicht.
import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";

const archiveMode = process.argv[2] === "--archiv";
const root = archiveMode ? process.argv[3] : process.argv[2];
if (!root) {
  console.error("Aufruf: node scripts/crosscheck.mjs <Konfigurationsordner> | --archiv <Archivordner>");
  process.exit(2);
}

function* sessionFiles(dir) {
  const projects = path.join(dir, "projects");
  if (!fs.existsSync(projects)) return;
  for (const p of fs.readdirSync(projects, { withFileTypes: true })) {
    if (!p.isDirectory()) continue;
    const pdir = path.join(projects, p.name);
    for (const e of fs.readdirSync(pdir, { withFileTypes: true })) {
      if (e.isFile() && e.name.endsWith(".jsonl")) yield path.join(pdir, e.name);
      if (e.isDirectory()) {
        const sub = path.join(pdir, e.name, "subagents");
        if (!fs.existsSync(sub)) continue;
        for (const s of fs.readdirSync(sub, { withFileTypes: true })) {
          if (s.isFile() && s.name.endsWith(".jsonl")) yield path.join(sub, s.name);
        }
      }
    }
  }
}

const reqs = new Map();
const MODEL = /^[A-Za-z0-9._-]{1,64}$/;
const num = (v) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
let duplicates = 0;
if (archiveMode) readArchive(root);
for (const f of archiveMode ? [] : sessionFiles(root)) {
  const text = fs.readFileSync(f, "utf8");
  // nur vollständige Zeilen, wie tokenlore
  const complete = text.slice(0, text.lastIndexOf("\n") + 1);
  for (const line of complete.split("\n")) {
    if (!line) continue;
    let j;
    try { j = JSON.parse(line); } catch { continue; }
    if (j.type !== "assistant" || !j.message?.usage || !j.message.id) continue;
    const key = j.message.id + "|" + (j.requestId ?? "");
    const u = j.message.usage;
    let r = reqs.get(key);
    if (!r) {
      r = { model: j.message.model, ts: j.timestamp, input: 0, output: -1, cc: 0, cr: 0 };
      reqs.set(key, r);
    }
    if (num(u.output_tokens) >= r.output) setUsage(r, u);
  }
}

function setUsage(r, u) {
  r.input = num(u.input_tokens);
  r.output = num(u.output_tokens);
  r.cc = num(u.cache_creation_input_tokens);
  r.cr = num(u.cache_read_input_tokens);
}

function readArchive(dir) {
  for (const name of fs.readdirSync(dir).sort()) {
    if (!/^[0-9]{4}-[0-9]{2}(\.jsonl|\.jsonl\.gz|\.nachtrag\.jsonl)$/.test(name)) continue;
    let text = fs.readFileSync(path.join(dir, name));
    if (name.endsWith(".gz")) text = zlib.gunzipSync(text);
    text = text.toString("utf8");
    for (const line of text.slice(0, text.lastIndexOf("\n") + 1).split("\n")) {
      if (!line) continue;
      let z;
      try { z = JSON.parse(JSON.parse(line).z); } catch { continue; }
      if (z.kind !== "request") continue;
      const key = z.message_id + "|" + z.request_id;
      if (reqs.has(key)) { duplicates++; continue; }
      const r = { model: z.model, ts: z.ts };
      setUsage(r, z.usage ?? {});
      reqs.set(key, r);
    }
  }
}
const sums = new Map();
let total = 0;
for (const r of reqs.values()) {
  if (!r.model || r.model === "<synthetic>" || !MODEL.test(r.model) || Number.isNaN(Date.parse(r.ts))) continue;
  total++;
  const s = sums.get(r.model) ?? { n: 0, input: 0, output: 0, cc: 0, cr: 0 };
  s.n++; s.input += r.input; s.output += r.output; s.cc += r.cc; s.cr += r.cr;
  sums.set(r.model, s);
}

console.log(`Anfragen gesamt: ${total}`);
if (archiveMode) console.log(`Doppelt im Archiv: ${duplicates}`);
const pad = (v, n) => String(v).padStart(n);
console.log(`${"Modell".padEnd(28)} ${pad("Anfragen", 8)} ${pad("input", 14)} ${pad("output", 14)} ${pad("cache_create", 14)} ${pad("cache_read", 16)}`);
for (const m of [...sums.keys()].sort()) {
  const s = sums.get(m);
  console.log(`${m.padEnd(28)} ${pad(s.n, 8)} ${pad(s.input, 14)} ${pad(s.output, 14)} ${pad(s.cc, 14)} ${pad(s.cr, 16)}`);
}
