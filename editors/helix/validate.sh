#!/usr/bin/env bash
# Validates editors/helix/languages.toml for structural correctness.
# Requires either `taplo` (preferred) or Python 3 with tomllib (stdlib ≥ 3.11).
#
# Usage:
#   ./editors/helix/validate.sh
set -euo pipefail

TOML="${1:-$(dirname "$0")/languages.toml}"

if ! [ -f "$TOML" ]; then
  echo "error: $TOML not found" >&2
  exit 1
fi

# ── prefer taplo ──────────────────────────────────────────────────────────────
if command -v taplo &>/dev/null; then
  echo "==> Validating with taplo..."
  taplo check "$TOML"
  echo "==> TOML is valid."
  exit 0
fi

# ── fallback: Python tomllib (stdlib in 3.11+) ────────────────────────────────
if command -v python3 &>/dev/null; then
  python3 - "$TOML" <<'EOF'
import sys, pathlib
try:
    import tomllib
except ImportError:
    try:
        import tomli as tomllib
    except ImportError:
        print("warning: neither tomllib nor tomli available; skipping parse check", file=sys.stderr)
        sys.exit(0)

path = pathlib.Path(sys.argv[1])
try:
    data = tomllib.loads(path.read_text())
except Exception as e:
    print(f"error: invalid TOML — {e}", file=sys.stderr)
    sys.exit(1)

# Structural assertions
langs = data.get("language", [])
assert isinstance(langs, list) and len(langs) > 0, "expected at least one [[language]] entry"
dpg = next((l for l in langs if l.get("name") == "dpg"), None)
assert dpg is not None, "expected a [[language]] entry with name = 'dpg'"
assert dpg.get("scope") == "source.dpg", "expected scope = 'source.dpg'"
assert "dpg" in dpg.get("file-types", []), "expected 'dpg' in file-types"
assert dpg.get("comment-token") == "--", "expected comment-token = '--'"
fmt = dpg.get("formatter", {})
assert fmt.get("command") == "dpg", "expected formatter command = 'dpg'"
assert fmt.get("args") == ["fmt", "--stdin"], "expected formatter args = ['fmt', '--stdin']"

servers = data.get("language-server", {})
assert "dpg-lsp" in servers, "expected [language-server.dpg-lsp] section"
assert servers["dpg-lsp"].get("command") == "dpg-lsp", "expected command = 'dpg-lsp'"

print("==> TOML is structurally valid.")
EOF
  exit 0
fi

echo "error: neither taplo nor python3 is available; cannot validate TOML" >&2
exit 1
