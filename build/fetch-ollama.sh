#!/usr/bin/env bash
# Downloads the Ollama binary used as a bundled fallback for Nurvis.
#
# It is intentionally NOT committed to git (see build/sidecar/.gitignore); CI
# and local desktop builds run this script before packaging so the binary lands
# in build/sidecar/<os>/ where the platform Taskfiles copy it into the bundle.
#
# Usage:
#   ./fetch-ollama.sh                 # auto-detect host OS/arch
#   OLLAMA_VERSION=0.24.0 ./fetch-ollama.sh
#   TARGET_OS=windows ./fetch-ollama.sh
#
# Pin a known-good version. Bump deliberately and re-test GPU/CPU paths.
set -euo pipefail

OLLAMA_VERSION="${OLLAMA_VERSION:-0.24.0}"
TARGET_OS="${TARGET_OS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SIDECAR_DIR="${SCRIPT_DIR}/sidecar"
BASE_URL="https://github.com/ollama/ollama/releases/download/${OLLAMA_VERSION}"

case "${TARGET_OS}" in
  darwin)
    # macOS release ships a universal binary inside ollama-darwin.tgz.
    OUT_DIR="${SIDECAR_DIR}/darwin"
    mkdir -p "${OUT_DIR}"
    echo "Fetching Ollama ${OLLAMA_VERSION} for macOS…"
    curl -fsSL "${BASE_URL}/ollama-darwin.tgz" -o /tmp/ollama-darwin.tgz
    tar -xzf /tmp/ollama-darwin.tgz -C "${OUT_DIR}"
    # The archive extracts an `ollama` binary (and possibly libs); keep them all.
    chmod +x "${OUT_DIR}/ollama"
    echo "Ollama (macOS) -> ${OUT_DIR}/ollama"
    ;;
  windows|mingw*|msys*)
    OUT_DIR="${SIDECAR_DIR}/windows"
    mkdir -p "${OUT_DIR}"
    echo "Fetching Ollama ${OLLAMA_VERSION} for Windows…"
    curl -fsSL "${BASE_URL}/ollama-windows-amd64.zip" -o /tmp/ollama-windows.zip
    unzip -o /tmp/ollama-windows.zip -d "${OUT_DIR}"
    # ZIP contains ollama.exe plus a lib/ directory (GPU runners). Keep both.
    echo "Ollama (Windows) -> ${OUT_DIR}/ollama.exe"
    ;;
  linux)
    OUT_DIR="${SIDECAR_DIR}/linux"
    mkdir -p "${OUT_DIR}"
    echo "Fetching Ollama ${OLLAMA_VERSION} for Linux…"
    curl -fsSL "${BASE_URL}/ollama-linux-amd64.tgz" -o /tmp/ollama-linux.tgz
    tar -xzf /tmp/ollama-linux.tgz -C "${OUT_DIR}"
    chmod +x "${OUT_DIR}/ollama" 2>/dev/null || chmod +x "${OUT_DIR}/bin/ollama" 2>/dev/null || true
    echo "Ollama (Linux) -> ${OUT_DIR}"
    ;;
  *)
    echo "Unsupported TARGET_OS: ${TARGET_OS}" >&2
    exit 1
    ;;
esac
