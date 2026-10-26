#!/bin/bash
# Baut in ein Temp-Verzeichnis und vergleicht mit einem fertigen Bau (Standard plugin/bin, sonst Ordner als Argument).
# Abweichung heißt: die Programme dort passen nicht zum Quelltext (oder der Bau ist nicht reproduzierbar).
# Die Veröffentlichung (.github/workflows/veroeffentlichen.yml) ruft das mit dem Ordner auf, der ins öffentliche Repo geht.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
# Ein relativer Ordner gilt wie bei build.sh ab der Repo-Wurzel.
dir=${1:-plugin/bin}
case "$dir" in /*|[A-Za-z]:*) ;; *) dir="$root/$dir" ;; esac
ls "$dir"/tokenlore-* >/dev/null 2>&1 || { echo "FEHLER: in $dir liegen keine Programme (bash scripts/build.sh ausführen)" >&2; exit 1; }
dir=$(cd "$dir" && pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
bash "$root/scripts/build.sh" "$tmp" >/dev/null
# Jede Datei einzeln vergleichen: die Summenliste allein ließe eine manipulierte Datei durch.
bad=0
for f in "$tmp"/tokenlore-*; do
  cmp -s "$f" "$dir/$(basename "$f")" || { echo "FEHLER: $(basename "$f") weicht vom Neubau ab" >&2; bad=1; }
done
# Keine zusätzlichen Programme neben den gebauten.
for f in "$dir"/tokenlore-*; do
  [ -e "$tmp/$(basename "$f")" ] || { echo "FEHLER: $(basename "$f") gehört nicht zum Bau" >&2; bad=1; }
done
(cd "$dir" && sha256sum -c --quiet SHA256SUMS) || bad=1
[ "$bad" = 0 ] || exit 1
if diff -u "$dir/SHA256SUMS" "$tmp/SHA256SUMS"; then
  echo "OK: Programme entsprechen dem Quelltext"
else
  echo "FEHLER: $dir weicht vom Neubau ab (bash scripts/build.sh ausführen)" >&2
  exit 1
fi
