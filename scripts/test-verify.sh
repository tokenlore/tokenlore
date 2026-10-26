#!/bin/bash
# Prüft, dass verify-build.sh einen frischen Bau annimmt und eine manipulierte oder fremde Datei erkennt.
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
bash "$root/scripts/build.sh" "$tmp/bau" >/dev/null || { echo "FAIL: Bau gescheitert"; exit 1; }
bash "$root/scripts/verify-build.sh" "$tmp/bau" >/dev/null 2>&1 || { echo "FAIL: frischer Bau wird abgelehnt"; exit 1; }
f="$tmp/bau/tokenlore-linux-amd64"
cp "$f" "$tmp/original"
printf 'X' | dd of="$f" bs=1 seek=100 conv=notrunc 2>/dev/null
if bash "$root/scripts/verify-build.sh" "$tmp/bau" >/dev/null 2>&1; then echo "FAIL: manipulierte Datei nicht erkannt"; exit 1; fi
cp "$tmp/original" "$f"
cp "$f" "$tmp/bau/tokenlore-linux-riscv64"
if bash "$root/scripts/verify-build.sh" "$tmp/bau" >/dev/null 2>&1; then echo "FAIL: fremde Datei nicht erkannt"; exit 1; fi
rm "$tmp/bau/tokenlore-linux-riscv64"
if bash "$root/scripts/verify-build.sh" "$tmp/leer" >/dev/null 2>&1; then echo "FAIL: fehlender Bau nicht erkannt"; exit 1; fi
echo "OK   verify-build erkennt manipulierte, fremde und fehlende Programme"
