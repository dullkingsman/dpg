#!/usr/bin/env bash
# Run tree-sitter generate then tree-sitter test.
# Must be called from the lang/grammar/ directory (or any directory that
# contains grammar.js), or with the grammar directory as the first argument.
#
# Usage:
#   ./scripts/test.sh            # from lang/grammar/
#   ./scripts/test.sh lang/grammar   # from repo root
set -euo pipefail

GRAMMAR_DIR="${1:-$(dirname "$0")/..}"
cd "$GRAMMAR_DIR"

if ! command -v npx &>/dev/null; then
  echo "error: npx is not on PATH — install Node.js first" >&2
  exit 1
fi

echo "==> Installing dependencies..."
npm install --prefer-offline --silent

echo "==> Generating parser from grammar.js..."
npx tree-sitter generate

echo "==> Running corpus tests..."
npx tree-sitter test

echo "==> All grammar tests passed."
