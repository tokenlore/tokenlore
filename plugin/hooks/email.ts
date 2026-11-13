import type { Register } from 'claude-code'
import { firstLine, handled, runDirect } from './direct.ts'

// Auswahlfenster für /tokenlore:email ohne Argument. Das Fenster öffnet Claude Code selbst, ohne dass Claude es
// liest; deshalb darf es die Adresse zeigen. Die Textausgabe des Befehls liest Claude mit: Sie nennt nur die Stufe.
// Mit Argument (off|short|full) ruft das Modul `email <stufe>` selbst und zeigt die Ausgabe (direct.ts).

type Choice = {
  mode: string
  account?: { from: string; short: string; full: string }
}

const LEVELS = [
  { mode: 'off', name: 'Aus' },
  { mode: 'short', name: 'Kurz' },
  { mode: 'full', name: 'Voll' },
] as const

// options baut die Optionen des Fensters. Ohne bekannte Adresse nennen sie nur die Stufen.
export function options(c: Choice): string[] {
  const a = c.account
  return [
    'Aus – nichts senden',
    a ? `Kurz – ${a.short}` : 'Kurz – gekürzte Adresse',
    a ? `Voll – ${a.full}` : 'Voll – ganze Adresse',
  ]
}

// levelOf ist die Stufe zu einer Antwort des Fensters, undefined für Überspringen oder eigenen Text.
export function levelOf(c: Choice, answer: string): (typeof LEVELS)[number] | undefined {
  const i = options(c).indexOf(answer.trim())
  return i < 0 ? undefined : LEVELS[i]
}

// chooseLevel zeigt das Auswahlfenster und setzt die gewählte Stufe. Die Meldung beginnt mit ✓ (gesetzt), · (nichts
// geändert) oder ✗ (Fehler). undefined heißt: Stufe und Adresse nicht lesbar,
// der Aufrufer nimmt seinen Rückfall. /tokenlore:menu ruft es auch.
export async function chooseLevel($: any): Promise<{ text: string } | undefined> {
  const bin = `${$.plugin.root}/bin/tokenlore`
  let c: Choice
  try {
    const r = await $.process.run(['sh', bin, 'email', '--json'])
    if (r.exitCode !== 0) {
      return undefined
    }
    c = JSON.parse(r.stdout) as Choice
  } catch {
    return undefined
  }

  const current = LEVELS.find(l => l.mode === c.mode)?.name ?? 'Aus'
  let answer: string
  try {
    answer = await $.ui.ask(
      `Welche E-Mail-Adresse soll tokenlore senden? Die Stufe gilt für alle Konten; eingestellt ist ${current}.`,
      { header: 'E-Mail', options: options(c) },
    )
  } catch {
    return { text: `· Abgebrochen, die Stufe bleibt ${current}.` }
  }

  const level = levelOf(c, answer)
  if (!level) {
    return { text: `· Nichts gewählt, die Stufe bleibt ${current}.` }
  }
  // Die Meldungen von `email <stufe>` nennen keine Adresse; sie dürfen in den Chat.
  const failure = await $.process
    .run(['sh', bin, 'email', level.mode])
    .then((r: any) => (r.exitCode === 0 ? '' : firstLine(r)))
    .catch(() => 'tokenlore ließ sich nicht starten.')
  if (failure !== '') {
    return { text: `✗ Nicht gespeichert, die Stufe bleibt ${current}. ${failure}`.trim() }
  }

  return { text: `✓ E-Mail-Stufe: ${current} → ${level.name}` }
}

export const register: Register = on => {
  on('command.run', { command: 'tokenlore:email' }, async ($, e, next) => {
    if (e.args.trim() !== '') {
      return handled(e.args) ? runDirect($, 'email', e.args) : next(e)
    }
    return (await chooseLevel($)) ?? next(e)
  }).catch(($, e, next) => next(e)) // Fehler im Modul: der Befehl läuft wie ohne Auswahlfenster
}
