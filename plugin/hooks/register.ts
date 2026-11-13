import type { Register } from 'claude-code'
import { register as email } from './email.ts'
import { register as connect } from './connect.ts'
import { register as direct } from './direct.ts'

// Einziges Hooks-Modul des Plugins (hooks.json nennt je Plugin genau eines): Es meldet die Hooks der einzelnen
// Befehle an, je Befehl aus einer eigenen Datei.
export const register: Register = (on, options) => {
  email(on, options)
  connect(on, options)
  direct(on, options)
}
