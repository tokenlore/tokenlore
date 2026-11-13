import type { Register } from 'claude-code'

// Auswahlfenster für die Frage nach dem Vorgänger bei /tokenlore:connect. Stammt dieser Konfigurationsordner von
// einem Gerät desselben Benutzers, fragt der Server, ob dieser Rechner das alte Gerät ist. Das Modul ruft
// `connect --frage-json`; Exit 3 heißt Frage, stdout ist dann JSON mit fertigem Fragetext, den Optionen und der
// Meldung ohne Antwort, alles vom Go-Programm gebaut (dort getestet). Das Modul zeigt nur das Fenster und koppelt
// danach mit `--vorgaenger <wert>`. Ohne Frage gibt es die Ausgabe von connect unverändert zurück. Mit
// `--vorgaenger` in den Argumenten, ohne Argumente oder ohne dieses Modul läuft der Befehl aus plugin/commands/; er
// druckt die Frage dann als Text mit den drei fertigen Befehlen.

type Question = {
  before: string
  question: string
  header: string
  options: { label: string; value: string }[]
  not_paired: string
}

// Exit-Code von connect bei einer Frage (exitPredecessorQuestion in cmd/tokenlore/predecessor.go).
const EXIT_QUESTION = 3

// Längste Laufzeit, die Claude Code erlaubt: Der erste Abgleich nach dem Koppeln darf dauern, eine eigene Grenze hat
// das Modul nicht.
const TIMEOUT_MS = 10 * 60 * 1000

const NOT_FINISHED =
  'tokenlore lief nicht zu Ende (nicht gestartet oder nach 10 Minuten abgebrochen). Ob dieser Rechner gekoppelt ist, zeigt /tokenlore:status.'

// handled meldet, ob das Modul den Aufruf übernimmt: nicht ohne Argumente, nicht mit --vorgaenger oder --frage-json
// (die Antwort steht schon fest oder jemand will das JSON selbst sehen) und nicht mit einem einfachen
// Anführungszeichen, das die Befehlsdatei mit ihrer eigenen Meldung ablehnt.
export function handled(args: string): boolean {
  return args.trim() !== '' && !/(^|\s)--?(vorgaenger|frage-json)(=|\s|$)/.test(args) && !args.includes("'")
}

// valueOf ist der Wert für --vorgaenger zu einer Antwort des Fensters, undefined für eigenen Text.
export function valueOf(q: Question, answer: string): string | undefined {
  return q.options.find(o => o.label === answer.trim())?.value
}

function output(r: { stdout: string; stderr: string }): string {
  return (r.stdout.trim() || r.stderr.trim())
}

export const register: Register = on => {
  on('command.run', { command: 'tokenlore:connect' }, async ($, e, next) => {
    if (!handled(e.args)) {
      return next(e)
    }
    const bin = `${$.plugin.root}/bin/tokenlore`
    const connect = (extra: string) =>
      $.process.run(['sh', bin, 'connect', '--args', `${e.args} ${extra}`], { timeoutMs: TIMEOUT_MS })

    let first
    try {
      first = await connect('--frage-json')
    } catch {
      // Nicht next(e): Hat connect schon gekoppelt, wäre der Code verbraucht und ein zweiter Lauf scheiterte.
      return { text: NOT_FINISHED }
    }
    if (first.exitCode !== EXIT_QUESTION) {
      return { text: output(first) }
    }
    // Fehler beim Lesen fängt .catch unten: Der Befehl aus plugin/commands/ druckt die Frage dann als Text.
    const q = JSON.parse(first.stdout) as Question
    const before = q.before.trim() === '' ? '' : `${q.before.trim()}\n`

    let answer: string
    try {
      answer = await $.ui.ask(q.question, { header: q.header, options: q.options.map(o => o.label) })
    } catch {
      return { text: before + q.not_paired }
    }
    const value = valueOf(q, answer)
    if (!value) {
      return { text: before + q.not_paired }
    }

    // Die gewählte Antwort steht in der Meldung, damit klar bleibt, was gekoppelt wurde (auch nach einem schnellen Enter).
    const chosen = `${before}Deine Antwort: ${answer.trim()}\n`
    try {
      return { text: chosen + output(await connect(`--vorgaenger ${value}`)) }
    } catch {
      return { text: chosen + NOT_FINISHED }
    }
  }).catch(($, e, next) => next(e)) // Fehler im Modul: der Befehl läuft wie ohne Auswahlfenster
}
