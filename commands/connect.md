---
description: Koppelt dieses Gerät mit deinem tokenlore-Server
argument-hint: "<https://server> <code> [--ca <fingerabdruck>] [--name <gerätename>] [--resend-legacy]"
allowed-tools: Bash(sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" connect:*)
disable-model-invocation: true
---
!`sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" connect --args '$ARGUMENTS'`

Gib die Ausgabe oben unverändert wieder und füge nichts hinzu.
