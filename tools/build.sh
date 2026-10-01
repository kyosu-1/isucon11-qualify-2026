#!/usr/bin/env bash
# Go アプリを linux/amd64 向けにローカルでクロスビルドする（2 vCPU の上でビルドしない）。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/build"
cd "$ROOT/webapp/go"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$ROOT/build/isucondition" .
ls -l "$ROOT/build/isucondition" | awk '{print "==> build isucondition: " $5 " bytes"}'
