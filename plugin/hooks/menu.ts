import type { Register } from 'claude-code'
import { chooseLevel } from './email.ts'
import { firstLine, runDirect } from './direct.ts'

// Menü /tokenlore:menu: ein Auswahlfenster mit dem Stand in der Frage (Gerät, letzter Abgleich) und den Optionen
// Status im Detail, E-Mail-Stufe ändern und Plugin aktualisieren. Den Stand liest es aus der Ausgabe von `status`,
// die es bei „Status im Detail“ unverändert zeigt. connect, sync und disconnect bleiben eigene Befehle.
// Ohne dieses Modul läuft die Befehlsdatei plugin/commands/menu.md: Sie zeigt den Status und nennt die Befehle.

const STATUS = 'Status im Detail'
const EMAIL = 'E-Mail-Stufe ändern'
const UPDATE = 'Plugin aktualisieren'

// Grenze für die beiden Aufrufe von `claude plugin …`: Der Marktplatz kommt per Git, das darf dauern.
const UPDATE_TIMEOUT_MS = 5 * 60 * 1000

const COMMANDS = 'claude plugin marketplace update tokenlore · claude plugin update tokenlore@tokenlore'

// summary ist der Stand für die Frage, aus den Zeilen „Gerät: …“ und „Letzter Erfolg: …“ der Ausgabe von `status`.
export function summary(status: string): string {
  const line = (key: string) =>
    status
      .split('\n')
      .find(l => l.startsWith(`${key}: `))
      ?.slice(key.length + 2)
      .trim()
  const device = line('Gerät')?.replace(/ \(Nummer \d+\)$/, '')
  const last = line('Letzter Erfolg')
  if (!device) {
    return status.includes('Nicht verbunden') ? 'Nicht verbunden.' : 'Stand unbekannt.'
  }
  return `Gerät ${device}, letzter Abgleich: ${last ?? 'unbekannt'}.`
}

// update holt die Marktplatz-Liste neu und aktualisiert das Plugin. `claude plugin update` frischt die Liste nur auf,
// wenn sie nicht gerade erst geholt wurde; der eigene Aufruf vorher holt sie sicher. Nach einer neuen Version steht
// /reload-plugins im Eingabefeld: Neu laden kann das Plugin sich nicht verlässlich selbst. Die erste Zeile beginnt mit
// ✓ (aktuell oder aktualisiert), · (übersprungen) oder ✗ (Fehler); darunter stehen eingerückt Hinweis und Befehle.
export async function update($: any): Promise<{ text: string }> {
  const run = (argv: string[]) => $.process.run(argv, { timeoutMs: UPDATE_TIMEOUT_MS })
  const done = (first: string, ...more: string[]) => ({ text: [first, ...more].join('\n  ') })
  const failed = (why: string) => done(`✗ Aktualisieren ging nicht: ${why}`, `von Hand im Terminal: ${COMMANDS}`)
  $.ui.toast('tokenlore: Plugin wird aktualisiert …')
  let market, r
  try {
    market = await run(['claude', 'plugin', 'marketplace', 'update', 'tokenlore'])
    r = market.exitCode === 0 ? await run(['claude', 'plugin', 'update', 'tokenlore@tokenlore', '--json']) : undefined
  } catch {
    return failed('claude ließ sich nicht starten.')
  }
  if (!r) {
    return failed(firstLine(market))
  }
  let u: { outcome?: string; updateOutcome?: string; oldVersion?: string; newVersion?: string; message?: string }
  try {
    u = JSON.parse(r.stdout.trim().split('\n').pop() ?? '')
  } catch {
    return failed(firstLine(r)) // etwa ein Claude Code ohne --json
  }
  if (r.exitCode !== 0 || u.outcome !== 'ok') {
    return failed(u.message ?? firstLine(r))
  }
  if (u.updateOutcome === 'up_to_date') {
    return done(`✓ Plugin ist aktuell (${u.oldVersion})`, `geprüft mit: ${COMMANDS}`)
  }
  if (u.updateOutcome !== 'updated') {
    return done(`· ${u.message ?? 'Nicht aktualisiert.'}`, `ausgeführt: ${COMMANDS}`) // etwa skipped
  }
  const filled = await $.prompt
    .fill({ text: '/reload-plugins ' })
    .then((f: any) => f?.isFilled !== false)
    .catch(() => false)
  const reload = filled
    ? 'Enter lädt die neue Version (/reload-plugins steht im Eingabefeld)'
    : '/reload-plugins eingeben lädt die neue Version (oder Claude Code neu starten)'
  return done(`✓ Plugin aktualisiert: ${u.oldVersion} → ${u.newVersion}`, reload, `ausgeführt: ${COMMANDS}`)
}

export const register: Register = on => {
  on('command.run', { command: 'tokenlore:menu' }, async ($, e, next) => {
    const status = await runDirect($, 'status', '')
    if (!status) {
      return next(e) // sh startet nicht: menu.md zeigt den Status über die Shell von Claude Code
    }
    let answer: string
    try {
      answer = await $.ui.ask(`tokenlore: ${summary(status.text)} Was möchtest du tun?`, {
        header: 'tokenlore',
        options: [STATUS, EMAIL, UPDATE],
      })
    } catch {
      return { text: 'Abgebrochen.' }
    }
    // Aktualisieren und E-Mail-Stufe nennen das Ergebnis zuerst (✓, · oder ✗), der Status den Stand nach einem Pfeil:
    // Im Chat steht sonst nur /tokenlore:menu.
    switch (answer.trim()) {
      case STATUS:
        return { text: `${STATUS}\n→ ${summary(status.text)}\n${status.text}` }
      case EMAIL:
        return (await chooseLevel($)) ?? { text: '✗ Das E-Mail-Fenster ging nicht auf. Setzen mit /tokenlore:email off|short|full.' }
      case UPDATE:
        return update($)
      default:
        return { text: 'Nichts gewählt.' }
    }
  }).catch(($, e, next) => next(e)) // Fehler im Modul: die Befehlsdatei läuft

  // Status und E-Mail-Stufe stehen im Menü; in der Liste der Befehle fehlen sie, getippt laufen sie weiter.
  for (const command of ['tokenlore:status', 'tokenlore:email']) {
    on('command.describe', { command }, ($, e, next) => next({ ...e, isHidden: true }))
  }
}
