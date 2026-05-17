#!/usr/bin/env bash
set -euo pipefail

TAG="${1:?Usage: scripts/tag.sh <tag>  (e.g. v1.2.3, lsp-v1.2.3, docs-v1.2.3, vscode-v1.2.3, idea-v1.2.3)}"
TODAY=$(date +%Y-%m-%d)
CHANGELOG="CHANGELOG.md"
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"

case "$TAG" in
  vscode-v[0-9]*) PREFIX="vscode-v" ;;
  idea-v[0-9]*)   PREFIX="idea-v" ;;
  lsp-v[0-9]*)    PREFIX="lsp-v" ;;
  docs-v[0-9]*)   PREFIX="docs-v" ;;
  v[0-9]*)        PREFIX="v" ;;
  *)
    echo "error: tag must start with v<semver>, lsp-v<semver>, docs-v<semver>, vscode-v<semver>, or idea-v<semver>" >&2
    exit 1 ;;
esac

VERSION="${TAG#"$PREFIX"}"

if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "error: tag '$TAG' already exists" >&2
  exit 1
fi

# Populate [Unreleased] body from git log
bash "$SCRIPTS/changelog.sh" "$PREFIX"

# Replace the [Unreleased] header with the versioned header,
# and insert a fresh empty [Unreleased] above it.
LABEL=$( [ "$PREFIX" = "v" ] && echo "$VERSION" || echo "$TAG" )

python3 - "$CHANGELOG" "$LABEL" "$TODAY" <<'PYEOF'
import sys, re

changelog_path, label, today = sys.argv[1:4]

with open(changelog_path) as f:
    content = f.read()

pattern = r'## \[Unreleased\][^\n]*'
replacement = f"## [Unreleased]\n\n## [{label}] — {today}"

new_content, count = re.subn(pattern, replacement, content, count=1)
if count == 0:
    sys.exit("error: '## [Unreleased]' not found in CHANGELOG.md")

with open(changelog_path, 'w') as f:
    f.write(new_content)

print(f"  [Unreleased] → [{label}] — {today}")
PYEOF

git add "$CHANGELOG"
git commit -m "chore: release ${TAG}"
git tag "${TAG}"

echo ""
echo "Done. Push with:"
echo "  git push && git push origin ${TAG}"
