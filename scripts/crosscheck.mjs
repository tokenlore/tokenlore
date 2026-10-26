// Unabhängige Gegenprobe zu `tokenlore scan --summary`: zählt eindeutige
// (message.id, requestId)-Paare und summiert die Token-Werte je Modell. Je Anfrage gilt pro Feld
// das Maximum über alle ihre Zeilen (frühe Zeilen sind Zwischenstände beim Streamen).
// Aufruf: node scripts/crosscheck.mjs <Claude-Konfigurationsordner>
import fs from "node:fs";
import path from "node:path";

const root = process.argv[2];
if (!root) {
  console.error("Aufruf: node scripts/crosscheck.mjs <Konfigurationsordner>");
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
for (const f of sessionFiles(root)) {
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
      r = { model: j.message.model, ts: j.timestamp, input: 0, output: 0, cc: 0, cr: 0 };
      reqs.set(key, r);
    }
    r.input = Math.max(r.input, u.input_tokens ?? 0);
    r.output = Math.max(r.output, u.output_tokens ?? 0);
    r.cc = Math.max(r.cc, u.cache_creation_input_tokens ?? 0);
    r.cr = Math.max(r.cr, u.cache_read_input_tokens ?? 0);
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
const pad = (v, n) => String(v).padStart(n);
console.log(`${"Modell".padEnd(28)} ${pad("Anfragen", 8)} ${pad("input", 14)} ${pad("output", 14)} ${pad("cache_create", 14)} ${pad("cache_read", 16)}`);
for (const m of [...sums.keys()].sort()) {
  const s = sums.get(m);
  console.log(`${m.padEnd(28)} ${pad(s.n, 8)} ${pad(s.input, 14)} ${pad(s.output, 14)} ${pad(s.cc, 14)} ${pad(s.cr, 16)}`);
}
