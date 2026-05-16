#!/usr/bin/env bash
set -euo pipefail

# Usage: changelog.sh [prefix]
# prefix: v (default), lsp-v, docs-v
# Populates the body of ## [Unreleased] in CHANGELOG.md from git log
# since the last tag of the same type. The header line is preserved.

PREFIX="${1:-v}"
CHANGELOG="CHANGELOG.md"

case "$PREFIX" in
  lsp-v)  MATCH="lsp-v*"  ;;
  docs-v) MATCH="docs-v*" ;;
  v)      MATCH="v[0-9]*" ;;
  *)
    echo "error: prefix must be v, lsp-v, or docs-v" >&2
    exit 1 ;;
esac

LAST_TAG=$(git describe --tags --match "$MATCH" --abbrev=0 2>/dev/null || true)
RANGE=$( [ -n "$LAST_TAG" ] && echo "${LAST_TAG}..HEAD" || echo "HEAD" )

COMMITS=$(git log "$RANGE" --no-merges --format="- %s" \
  | grep -v '^- chore: release' || true)

TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT
printf '%s\n' "$COMMITS" > "$TMPFILE"

python3 - "$CHANGELOG" "$TMPFILE" <<'PYEOF'
import sys, re

changelog_path, commits_file = sys.argv[1:3]

with open(changelog_path) as f:
    content = f.read()
with open(commits_file) as f:
    raw = [l for l in f.read().strip().splitlines() if l]

SECTION_MAP = {"feat": "Added", "fix": "Fixed"}
SKIP = {"chore", "test", "ci", "style", "build"}

def categorize(line):
    msg = line.removeprefix("- ")
    colon = msg.find(":")
    if colon == -1:
        return "Changed", msg
    type_part = msg[:colon].split("(")[0].lower()
    if type_part in SKIP:
        return None, None
    body = msg[colon + 1:].strip()
    body = body[:1].upper() + body[1:]
    return SECTION_MAP.get(type_part, "Changed"), body

buckets = {"Added": [], "Fixed": [], "Changed": []}
for line in raw:
    section, body = categorize(line)
    if section:
        buckets[section].append(f"- {body}")

parts = [
    f"### {name}\n\n" + "\n".join(buckets[name])
    for name in ("Added", "Fixed", "Changed")
    if buckets[name]
]
body = "\n\n".join(parts) if parts else "- No changes."

# Replace only the body of [Unreleased]; keep the header line intact.
pattern = r'(## \[Unreleased\][^\n]*\n).*?(?=\n## \[|\Z)'
replacement = f"\\1\n{body}\n"

new_content, count = re.subn(pattern, replacement, content, count=1, flags=re.DOTALL)
if count == 0:
    sys.exit("error: '## [Unreleased]' not found in CHANGELOG.md")

with open(changelog_path, 'w') as f:
    f.write(new_content)

print("  Updated [Unreleased] body in CHANGELOG.md")
PYEOF
