#!/usr/bin/env bash
# install.sh — download and install dpg from GitHub Releases.
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/dullkingsman/dpg/master/scripts/install.sh | bash
#
# Options:
#   --version <tag>    Install a specific release tag (default: latest)
#   --install-dir <d>  Override install directory (default: /usr/local/bin or ~/.local/bin)
#   --with-lsp         Also install dpg-lsp (requires Go on PATH)
#   --check            Print what would be installed, then exit
#
# Supported platforms:
#   Linux  — amd64, arm64
#   macOS  — amd64 (Intel), arm64 (Apple Silicon)
#
# The binary is downloaded from:
#   https://github.com/dullkingsman/dpg/releases

set -euo pipefail
IFS=$'\n\t'

REPO="dullkingsman/dpg"
BINARY="dpg"
LSP_BINARY="dpg-lsp"
LSP_MODULE="github.com/dullkingsman/dpg-lsp/cmd/dpg-lsp@latest"

# ── Flags ────────────────────────────────────────────────────────────────────

VERSION=""
INSTALL_DIR=""
WITH_LSP=false
CHECK_ONLY=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)     VERSION="$2";      shift 2 ;;
    --install-dir) INSTALL_DIR="$2";  shift 2 ;;
    --with-lsp)    WITH_LSP=true;     shift ;;
    --check)       CHECK_ONLY=true;   shift ;;
    --help|-h)
      sed -n '/^# install.sh/,/^$/p' "$0" | sed 's/^# \?//'
      exit 0
      ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

# ── Terminal colours ─────────────────────────────────────────────────────────

if [[ -t 1 ]] && command -v tput &>/dev/null; then
  RED=$(tput setaf 1); GREEN=$(tput setaf 2); YELLOW=$(tput setaf 3)
  BLUE=$(tput setaf 4); BOLD=$(tput bold); RESET=$(tput sgr0)
else
  RED=''; GREEN=''; YELLOW=''; BLUE=''; BOLD=''; RESET=''
fi

info()  { echo "${BLUE}${BOLD}==>${RESET} $*"; }
ok()    { echo "${GREEN}  ✓${RESET} $*"; }
warn()  { echo "${YELLOW}  !${RESET} $*"; }
die()   { echo "${RED}  ✗${RESET} $*" >&2; exit 1; }

# ── Platform detection ───────────────────────────────────────────────────────

OS="$(uname -s)"
ARCH_RAW="$(uname -m)"

case "$OS" in
  Linux)  GOOS="linux" ;;
  Darwin) GOOS="darwin" ;;
  *)      die "Unsupported OS: $OS. For Windows, download the binary manually from https://github.com/${REPO}/releases" ;;
esac

case "$ARCH_RAW" in
  x86_64)        GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *)             die "Unsupported architecture: $ARCH_RAW" ;;
esac

ASSET_NAME="${BINARY}-${GOOS}-${GOARCH}"

# ── Resolve version ──────────────────────────────────────────────────────────

resolve_latest_version() {
  local url="https://api.github.com/repos/${REPO}/releases/latest"
  if command -v curl &>/dev/null; then
    curl -fsSL "$url" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
  elif command -v wget &>/dev/null; then
    wget -qO- "$url" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
  else
    die "curl or wget is required"
  fi
}

if [[ -z "$VERSION" ]]; then
  info "Fetching latest release version..."
  VERSION="$(resolve_latest_version)"
  [[ -z "$VERSION" ]] && die "Could not determine latest release. Specify --version <tag> explicitly."
fi

ARCHIVE_NAME="${ASSET_NAME}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE_NAME}"

# ── Resolve install directory ────────────────────────────────────────────────

HAS_SUDO=false
if sudo -n true 2>/dev/null; then
  HAS_SUDO=true
fi

if [[ -z "$INSTALL_DIR" ]]; then
  if $HAS_SUDO; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="${HOME}/.local/bin"
  fi
fi

# ── Check mode ───────────────────────────────────────────────────────────────

if $CHECK_ONLY; then
  echo
  echo "${BOLD}Would install:${RESET}"
  echo "  Binary    : ${BINARY} ${VERSION} (${GOOS}/${GOARCH})"
  echo "  From      : ${DOWNLOAD_URL}"
  echo "  To        : ${INSTALL_DIR}/${BINARY}"
  if $WITH_LSP; then
    echo "  LSP binary: ${LSP_BINARY} (via go install)"
  fi
  echo
  exit 0
fi

# ── Check for required tools ─────────────────────────────────────────────────

if ! command -v curl &>/dev/null && ! command -v wget &>/dev/null; then
  die "curl or wget is required to download the binary"
fi

if ! command -v tar &>/dev/null; then
  die "tar is required to extract the archive"
fi

# ── Download and install ─────────────────────────────────────────────────────

echo
echo "${BOLD}Installing dpg ${VERSION} (${GOOS}/${GOARCH})${RESET}"
echo

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "Downloading ${ARCHIVE_NAME}..."
if command -v curl &>/dev/null; then
  curl -fsSL --progress-bar "${DOWNLOAD_URL}" -o "${TMP}/${ARCHIVE_NAME}"
else
  wget -q --show-progress "${DOWNLOAD_URL}" -O "${TMP}/${ARCHIVE_NAME}"
fi

info "Extracting..."
tar -xzf "${TMP}/${ARCHIVE_NAME}" -C "${TMP}"

# The archive contains a binary named dpg-<os>-<arch>; rename it to dpg.
if [[ ! -f "${TMP}/${ASSET_NAME}" ]]; then
  die "Expected ${ASSET_NAME} inside ${ARCHIVE_NAME}. Archive contents: $(tar -tzf "${TMP}/${ARCHIVE_NAME}" 2>/dev/null || echo 'unknown')"
fi
mv "${TMP}/${ASSET_NAME}" "${TMP}/${BINARY}"
chmod +x "${TMP}/${BINARY}"

info "Installing to ${INSTALL_DIR}/${BINARY}..."
mkdir -p "${INSTALL_DIR}"
if $HAS_SUDO && [[ "$INSTALL_DIR" == "/usr/local/bin" ]]; then
  sudo mv "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  mv "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

ok "dpg ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"

# ── PATH reminder (user-local installs) ──────────────────────────────────────

if [[ "$INSTALL_DIR" != "/usr/local/bin" ]]; then
  if [[ ":${PATH}:" != *":${INSTALL_DIR}:"* ]]; then
    warn "${INSTALL_DIR} is not on your PATH."
    warn "Add the following to your shell profile (e.g. ~/.bashrc or ~/.zshrc):"
    warn "  export PATH=\"${INSTALL_DIR}:\${PATH}\""
  fi
fi

# ── dpg-lsp (optional) ───────────────────────────────────────────────────────

if $WITH_LSP; then
  echo
  info "Installing dpg-lsp..."
  if ! command -v go &>/dev/null; then
    warn "Go is not on PATH — cannot install dpg-lsp automatically."
    warn "Install Go first: https://go.dev/dl/"
    warn "Then run: go install ${LSP_MODULE}"
  else
    go install "${LSP_MODULE}"
    GOBIN="$(go env GOPATH)/bin"
    ok "dpg-lsp installed to ${GOBIN}/dpg-lsp"
    if [[ ":${PATH}:" != *":${GOBIN}:"* ]]; then
      warn "\$(go env GOPATH)/bin is not on your PATH."
      warn "Add the following to your shell profile:"
      warn "  export PATH=\"\$(go env GOPATH)/bin:\${PATH}\""
    fi
  fi
fi

# ── Verify ───────────────────────────────────────────────────────────────────

echo
if command -v "${BINARY}" &>/dev/null || [[ -x "${INSTALL_DIR}/${BINARY}" ]]; then
  INSTALLED_VERSION="$("${INSTALL_DIR}/${BINARY}" --version 2>/dev/null || echo 'unknown')"
  ok "Verified: ${INSTALLED_VERSION}"
fi

echo
echo "${GREEN}${BOLD}Done!${RESET} Run ${BOLD}dpg --help${RESET} to get started."
echo
echo "Next steps:"
echo "  dpg init          — initialise a new DPG project"
echo "  dpg plan          — preview the SQL migration"
echo "  dpg apply         — apply the migration"
if ! $WITH_LSP; then
  echo
  echo "Install the language server for editor support:"
  echo "  go install ${LSP_MODULE}"
fi
echo
