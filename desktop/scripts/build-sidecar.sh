#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
TARGET=${TAURI_ENV_TARGET_TRIPLE:-$(rustc -vV | sed -n 's/^host: //p')}
SUFFIX=""
case "$TARGET" in *windows*) SUFFIX=".exe" ;; esac
mkdir -p "$ROOT/desktop/src-tauri/binaries"
cd "$ROOT"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o "desktop/src-tauri/binaries/asayn-bridge-$TARGET$SUFFIX" ./cmd/asayn-bridge
