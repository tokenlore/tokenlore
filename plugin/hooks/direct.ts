import type { Register } from 'claude-code'

// Direkte Ausgabe für /tokenlore:status, /tokenlore:sync, /tokenlore:disconnect und /tokenlore:email mit Argument:
// Das Modul ruft das Programm selbst und zeigt seine Ausgabe als Ausgabe des Befehls. Claude schreibt sie nicht ab,
// es gibt keinen eigenen Zug des Modells dafür. Ohne dieses Modul, bei einem Fehler darin oder ohne startbares sh
// läuft die Befehlsdatei aus plugin/commands/, die die Ausgabe an Claude gibt.

// Längste Laufzeit, die Claude Code erlaubt: Ein Abgleich darf dauern, eine eigene Grenze hat das Modul nicht.
const TIMEOUT_MS = 10 * 60 * 1000

export const NOT_FINISHED =
  'tokenlore lief nicht zu Ende (nach 10 Minuten abgebrochen). Den Stand zeigt /tokenlore:status.'

// Grenze für die Probe, ob sh startet: Sie läuft nur nach einem gescheiterten Aufruf und endet sofort.
const PROBE_TIMEOUT_MS = 10 * 1000

// shMissing meldet, ob sh sich nicht starten lässt. $.process.run startet ohne Shell; unter Windows liegt sh oft nicht
// im PATH (Git Bash, die Claude Code für Befehlsdateien und Hooks nimmt, bringt es mit, trägt es aber nicht ein). Dann
// lief tokenlore gar nicht, und die Befehlsdatei darf übernehmen, ohne dass etwas doppelt läuft. Startet sh, scheiterte
// der Aufruf selbst: in der Regel an der Zeitgrenze.
export async function shMissing($: any): Promise<boolean> {
  return $.process.run(['sh', '-c', 'exit 0'], { timeoutMs: PROBE_TIMEOUT_MS }).then(
    () => false,
    () => true,
  )
}

// handled meldet, ob das Modul den Aufruf übernimmt: nicht mit einem einfachen Anführungszeichen, das die Befehlsdatei
// mit ihrer eigenen Meldung ablehnt.
export function handled(args: string): boolean {
  return !args.includes("'")
}

// text ist die Ausgabe des Programms wie in der Befehlsdatei: stdout, dann stderr, ohne Leerzeilen am Rand.
export function text(r: { stdout: string; stderr: string }): string {
  return [r.stdout.trim(), r.stderr.trim()].filter(s => s !== '').join('\n')
}

// firstLine ist die erste Zeile von stderr, sonst von stdout: die Fehlermeldung eines Programms.
export function firstLine(r: { stdout: string; stderr: string }): string {
  return (r.stderr.trim() || r.stdout.trim()).split('\n')[0] ?? ''
}

// runDirect führt `tokenlore <command> --args <args>` aus und gibt die Ausgabe als Text zurück. undefined heißt: sh
// startet nicht, der Aufrufer nimmt die Befehlsdatei. Lief das Programm nicht zu Ende, sagt die Meldung das, statt den
// Befehl ein zweites Mal über die Befehlsdatei zu starten.
export async function runDirect($: any, command: string, args: string): Promise<{ text: string } | undefined> {
  const bin = `${$.plugin.root}/bin/tokenlore`
  try {
    const r = await $.process.run(['sh', bin, command, '--args', args], { timeoutMs: TIMEOUT_MS })
    return { text: text(r) }
  } catch {
    return (await shMissing($)) ? undefined : { text: NOT_FINISHED }
  }
}

// Je Befehl ein eigener Aufruf mit festem Namen, damit Claude Code (und `claude plugin validate`) den Befehl kennt.
// Fehler im Modul oder kein startbares sh: der Befehl läuft wie ohne dieses Modul.
export const register: Register = on => {
  on('command.run', { command: 'tokenlore:status' }, async ($, e, next) =>
    (handled(e.args) && (await runDirect($, 'status', e.args))) || next(e),
  ).catch(($, e, next) => next(e))
  on('command.run', { command: 'tokenlore:sync' }, async ($, e, next) =>
    (handled(e.args) && (await runDirect($, 'sync', e.args))) || next(e),
  ).catch(($, e, next) => next(e))
  on('command.run', { command: 'tokenlore:disconnect' }, async ($, e, next) =>
    (handled(e.args) && (await runDirect($, 'disconnect', e.args))) || next(e),
  ).catch(($, e, next) => next(e))
}
