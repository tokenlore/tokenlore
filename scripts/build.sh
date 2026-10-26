#!/bin/bash
# Baut die Programme für alle vier Plattformen reproduzierbar nach plugin/bin
# und schreibt SHA256SUMS. Aufruf aus beliebigem Ordner.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
# go aus dem PATH; nur wenn es fehlt, der übliche Windows-Ort.
if ! command -v go >/dev/null 2>&1; then
  export PATH="/c/Program Files/Go/bin:$PATH"
fi
command -v go >/dev/null 2>&1 || { echo "Fehler: go nicht gefunden" >&2; exit 1; }

# Toolchain festnageln (go.mod kennt nur ein Minimum): andere Go-Version = andere Prüfsummen.
want=$(tr -d '[:space:]' < GO_TOOLCHAIN)
have=$(go env GOVERSION)
if [ "$have" != "$want" ]; then
  echo "Fehler: Go-Toolchain \"$have\" statt \"$want\" (siehe GO_TOOLCHAIN); bitte $want verwenden" >&2
  exit 1
fi

out=${1:-plugin/bin}
version=$(tr -d '[:space:]' < VERSION)

# Versionsnummern in den Manifesten müssen zu VERSION passen.
for f in plugin/.claude-plugin/plugin.json .claude-plugin/marketplace.json; do
  if ! grep -q "\"version\": *\"$version\"" "$f"; then
    echo "Fehler: Version in $f weicht von VERSION ($version) ab" >&2
    exit 1
  fi
done

mkdir -p "$out"
targets="windows/amd64 linux/amd64 linux/arm64 darwin/arm64"
names=""
for t in $targets; do
  os=${t%/*}; arch=${t#*/}
  ext=; [ "$os" = windows ] && ext=.exe
  name="tokenlore-$os-$arch$ext"
  names="$names $name"
  echo "baue $name"
  # Feste Werte statt Umgebung oder `go env -w` des Bauenden: sonst ergäben gleiche Quellen andere Programme.
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOAMD64=v1 GOARM64=v8.0 GOEXPERIMENT= GOFLAGS=-mod=readonly go build -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X main.version=$version" \
    -o "$out/$name" ./cmd/tokenlore
done

# Format wie sha256sum, binär (Stern), damit sha256sum -c überall gleich liest.
( cd "$out" && sha256sum $names | sed 's/  / */' > SHA256SUMS )
echo "fertig: $out/SHA256SUMS"
