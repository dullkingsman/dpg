#!/usr/bin/env bash
# install-lsp.sh — download and install dpg-lsp from GitHub Releases.
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/dullkingsman/dpg/master/scripts/install-lsp.sh | bash
#
# Options:
#   --version <tag>    Install a specific release tag (default: latest)
#   --install-dir <d>  Override install directory (default: /usr/local/bin or ~/.local/bin)
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
BINARY="dpg-lsp"

# ── Flags ────────────────────────────────────────────────────────────────────

VERSION=""
INSTALL_DIR=""
CHECK_ONLY=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)     VERSION="$2";      shift 2 ;;
    --install-dir) INSTALL_DIR="$2";  shift 2 ;;
    --check)       CHECK_ONLY=true;   shift ;;
    --help|-h)
      sed -n '/^# install-lsp.sh/,/^$/p' "$0" | sed 's/^# \?//'
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

ARCHIVE_NAME="${BINARY}-${GOOS}-${GOARCH}.tar.gz"

# ── Resolve version ──────────────────────────────────────────────────────────

resolve_latest_version() {
  local url="https://api.github.com/repos/${REPO}/releases?per_page=50"
  local raw
  if command -v curl &>/dev/null; then
    raw="$(curl -fsSL "$url")"
  elif command -v wget &>/dev/null; then
    raw="$(wget -qO- "$url")"
  else
    die "curl or wget is required"
  fi
  # Find the first lsp-v* tag (releases are newest-first)
  echo "$raw" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' \
    | grep '^lsp-v' | head -1
}

if [[ -z "$VERSION" ]]; then
  info "Fetching latest release version..."
  VERSION="$(resolve_latest_version)"
  [[ -z "$VERSION" ]] && die "Could not determine latest release. Specify --version <tag> explicitly."
fi

# Normalise: accept bare v* tags and add the lsp- prefix
if [[ "$VERSION" != lsp-* ]]; then
  VERSION="lsp-${VERSION}"
fi

DISPLAY_VERSION="${VERSION#lsp-}"
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
  echo "  Binary : ${BINARY} ${DISPLAY_VERSION} (${GOOS}/${GOARCH})"
  echo "  From   : ${DOWNLOAD_URL}"
  echo "  To     : ${INSTALL_DIR}/${BINARY}"
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
echo "${BOLD}Installing dpg-lsp ${DISPLAY_VERSION} (${GOOS}/${GOARCH})${RESET}"
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

if [[ ! -f "${TMP}/${BINARY}" ]]; then
  die "Expected ${BINARY} inside ${ARCHIVE_NAME}. Archive contents: $(tar -tzf "${TMP}/${ARCHIVE_NAME}" 2>/dev/null || echo 'unknown')"
fi
chmod +x "${TMP}/${BINARY}"

info "Installing to ${INSTALL_DIR}/${BINARY}..."
mkdir -p "${INSTALL_DIR}"
if $HAS_SUDO && [[ "$INSTALL_DIR" == "/usr/local/bin" ]]; then
  sudo mv "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  mv "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

ok "dpg-lsp ${DISPLAY_VERSION} installed to ${INSTALL_DIR}/${BINARY}"

# ── PATH reminder (user-local installs) ──────────────────────────────────────

if [[ "$INSTALL_DIR" != "/usr/local/bin" ]]; then
  if [[ ":${PATH}:" != *":${INSTALL_DIR}:"* ]]; then
    warn "${INSTALL_DIR} is not on your PATH."
    warn "Add the following to your shell profile (e.g. ~/.bashrc or ~/.zshrc):"
    warn "  export PATH=\"${INSTALL_DIR}:\${PATH}\""
  fi
fi

# ── Verify ───────────────────────────────────────────────────────────────────

echo
if command -v "${BINARY}" &>/dev/null || [[ -x "${INSTALL_DIR}/${BINARY}" ]]; then
  INSTALLED_VERSION="$("${INSTALL_DIR}/${BINARY}" --version 2>/dev/null || echo 'unknown')"
  ok "Verified: ${INSTALLED_VERSION}"
fi

echo
echo "${GREEN}${BOLD}Done!${RESET} ${BINARY} is ready. See ${BOLD}editor-integration docs${RESET} to wire it into your editor."
echo
