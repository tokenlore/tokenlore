---
description: Gleicht die Nutzungszahlen jetzt mit dem Server ab
argument-hint: ""
allowed-tools: Bash(sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" sync:*)
disable-model-invocation: true
---
!`sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" sync --args '$ARGUMENTS'`

Gib die Ausgabe oben unverändert wieder und füge nichts hinzu.
