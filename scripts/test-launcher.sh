#!/bin/bash
# Prüft den Starter plugin/bin/tokenlore mit einer Attrappe als Programm.
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fails=0
ok() { echo "OK   $1"; }
bad() { echo "FAIL $1"; fails=$((fails+1)); }

case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) os=windows; ext=.exe; win=1 ;; Linux) os=linux; ext=; win=0 ;; Darwin) os=darwin; ext=; win=0 ;; *) echo "Plattform unbekannt"; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "Architektur unbekannt"; exit 1 ;; esac

mkdir -p "$tmp/bin"
cp "$root/plugin/bin/tokenlore" "$tmp/bin/tokenlore" 2>/dev/null || { echo "FAIL Starter fehlt"; exit 1; }
dummy="$tmp/bin/tokenlore-$os-$arch$ext"
printf '#!/bin/sh\nfor a in "$@"; do echo "ARG[$a]"; done\n' > "$dummy"
chmod +x "$dummy"

# 1) Argumente mit Leerzeichen kommen unverändert an
out=$(sh "$tmp/bin/tokenlore" connect https://x.example 123 --name "Mein PC" 2>&1)
want=$(printf 'ARG[connect]\nARG[https://x.example]\nARG[123]\nARG[--name]\nARG[Mein PC]')
if [ "$out" = "$want" ]; then ok "Argumente mit Leerzeichen"; else bad "Argumente mit Leerzeichen: $out"; fi

# 2) Ohne Ausführungsbit trotzdem gestartet (nicht unter Windows)
if [ "$win" = 0 ]; then
  chmod -x "$dummy"
  out=$(sh "$tmp/bin/tokenlore" status 2>&1)
  if [ "$out" = "ARG[status]" ]; then ok "ohne Ausführungsbit"; else bad "ohne Ausführungsbit: $out"; fi
fi

# 3) Fehlendes Programm: session-start Exit 0, sonst Exit 1
rm -f "$dummy"
sh "$tmp/bin/tokenlore" session-start >/dev/null 2>&1; rc=$?
if [ $rc = 0 ]; then ok "fehlendes Programm bei session-start: Exit 0"; else bad "session-start Exit $rc"; fi
sh "$tmp/bin/tokenlore" status >/dev/null 2>&1; rc=$?
if [ $rc = 1 ]; then ok "fehlendes Programm sonst: Exit 1"; else bad "status Exit $rc"; fi

# 4) Pfad mit Leerzeichen
mkdir -p "$tmp/mit leer/bin"; cp "$tmp/bin/tokenlore" "$tmp/mit leer/bin/"
printf '#!/bin/sh\necho ok\n' > "$tmp/mit leer/bin/tokenlore-$os-$arch$ext"; chmod +x "$tmp/mit leer/bin/tokenlore-$os-$arch$ext"
out=$(sh "$tmp/mit leer/bin/tokenlore" status 2>&1)
if [ "$out" = "ok" ]; then ok "Pfad mit Leerzeichen"; else bad "Pfad mit Leerzeichen: $out"; fi

[ $fails = 0 ] && echo "alle OK" || { echo "$fails Fehler"; exit 1; }
