#!/usr/bin/env bash
# setup.sh — bootstrap a complete DPG development environment.
#
# Installs and verifies every tool required to build, test, and contribute
# to DPG. Reads the Go version directly from go.mod so it stays in sync
# with the module. Safe to re-run: each tool is checked before installing.
#
# Supported platforms:
#   Linux  — Ubuntu/Debian (apt), Fedora/RHEL (dnf), Arch (pacman)
#   macOS  — Homebrew (will be installed if absent)
#
# Usage:
#   bash scripts/setup.sh          # install everything
#   bash scripts/setup.sh --check  # check only, no installs
#   bash scripts/setup.sh --no-docs   # skip Hugo + Node (docs not needed)

set -euo pipefail
IFS=$'\n\t'

# ── Constants ────────────────────────────────────────────────────────────────

HUGO_VERSION="0.147.0"
NODE_MIN_VERSION="20"
STATICCHECK_VERSION="latest"

# Derived from go.mod so it never drifts.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
GO_VERSION="$(grep '^go ' "${REPO_ROOT}/core/go.mod" | awk '{print $2}')"

# ── Flags ────────────────────────────────────────────────────────────────────

CHECK_ONLY=false
SKIP_DOCS=false

for arg in "$@"; do
  case "$arg" in
    --check)   CHECK_ONLY=true ;;
    --no-docs) SKIP_DOCS=true ;;
    --help|-h)
      sed -n '/^# setup.sh/,/^$/p' "$0" | sed 's/^# \?//'
      exit 0
      ;;
  esac
done

# ── Terminal colours ─────────────────────────────────────────────────────────

if [[ -t 1 ]] && command -v tput &>/dev/null; then
  RED=$(tput setaf 1); GREEN=$(tput setaf 2); YELLOW=$(tput setaf 3)
  BLUE=$(tput setaf 4); BOLD=$(tput bold); RESET=$(tput sgr0)
else
  RED=''; GREEN=''; YELLOW=''; BLUE=''; BOLD=''; RESET=''
fi

info()    { echo "${BLUE}${BOLD}==>${RESET} $*"; }
ok()      { echo "${GREEN}  ✓${RESET} $*"; }
warn()    { echo "${YELLOW}  !${RESET} $*"; }
error()   { echo "${RED}  ✗${RESET} $*" >&2; }
die()     { error "$*"; exit 1; }
checking(){ echo -n "${BOLD}  ?${RESET} Checking $* ... "; }
found()   { echo "${GREEN}found${RESET} ($*)"; }
missing() { echo "${YELLOW}missing${RESET}"; }

# ── Platform detection ───────────────────────────────────────────────────────

OS="$(uname -s)"
ARCH_RAW="$(uname -m)"

case "$OS" in
  Linux)  PLATFORM="linux" ;;
  Darwin) PLATFORM="macos" ;;
  *)      die "Unsupported OS: $OS (Windows: see https://dullkingsman.github.io/dpg/docs/getting-started/installation/)" ;;
esac

case "$ARCH_RAW" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)             die "Unsupported architecture: $ARCH_RAW" ;;
esac

PKG_MGR=""
if [[ "$PLATFORM" == "linux" ]]; then
  if   command -v apt-get &>/dev/null; then PKG_MGR="apt"
  elif command -v dnf     &>/dev/null; then PKG_MGR="dnf"
  elif command -v pacman  &>/dev/null; then PKG_MGR="pacman"
  else die "Unsupported Linux distribution. Install dependencies manually — see https://dullkingsman.github.io/dpg/docs/getting-started/installation/"
  fi
fi

# ── Sudo / privilege detection ───────────────────────────────────────────────
# When sudo is unavailable without a password, binaries are installed to
# ~/.local/bin instead of /usr/local/bin. System packages (apt/dnf/pacman)
# still need sudo; those installers print a manual instruction instead of
# failing the script.

HAS_SUDO=false
if sudo -n true 2>/dev/null; then
  HAS_SUDO=true
fi

# Directory for user-local binaries (no sudo required).
USER_BIN="${HOME}/.local/bin"

# Prepend USER_BIN unconditionally so checks later in this script find
# previously-installed user-local binaries before system-wide ones.
# This prevents false "outdated" reports when both locations contain
# the tool (e.g. system hugo 0.128 vs user-local hugo 0.147).
export PATH="${USER_BIN}:${PATH}"

# Resolve the directory to install a standalone binary into.
# Uses /usr/local/bin when sudo is available, ~/.local/bin otherwise.
install_bin_dir() {
  if $HAS_SUDO; then echo "/usr/local/bin"; else echo "$USER_BIN"; fi
}

# Ensure USER_BIN is on PATH for this session and persisted to shell profiles.
ensure_user_bin_on_path() {
  mkdir -p "$USER_BIN"
  if [[ ":$PATH:" != *":${USER_BIN}:"* ]]; then
    export PATH="${USER_BIN}:${PATH}"
  fi
  local line="export PATH=\"\${HOME}/.local/bin:\${PATH}\""
  for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
    if [[ -f "$rc" ]] && ! grep -qF '.local/bin' "$rc"; then
      echo "$line" >> "$rc"
      ok "Added ~/.local/bin to PATH in $rc"
    fi
  done
}

# Run a command with sudo when available, otherwise run it directly.
# For commands that genuinely need root (apt, systemctl) this will fail
# gracefully and print a manual instruction.
maybe_sudo() {
  if $HAS_SUDO; then sudo "$@"; else "$@"; fi
}

# ── Helpers ───────────────────────────────────────────────────────────────────

# Compare semver strings: returns 0 if $1 >= $2.
version_gte() {
  printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

pkg_install() {
  if $CHECK_ONLY; then return 0; fi
  if ! $HAS_SUDO; then
    warn "sudo not available — cannot run package manager. Install manually: $*"
    return 1
  fi
  case "$PKG_MGR" in
    apt)    sudo apt-get install -y -qq "$@" ;;
    dnf)    sudo dnf install -y -q "$@" ;;
    pacman) sudo pacman -S --noconfirm --needed "$@" ;;
  esac
}

# ── Version checks ────────────────────────────────────────────────────────────

MISSING=()

check_go() {
  checking "Go ${GO_VERSION}"
  if command -v go &>/dev/null; then
    local installed
    installed="$(go version | awk '{print $3}' | sed 's/go//')"
    if version_gte "$installed" "$GO_VERSION"; then
      found "go$installed"; return 0
    fi
    echo "${YELLOW}outdated${RESET} (have $installed, need $GO_VERSION)"
  else
    missing
  fi
  MISSING+=("go"); return 1
}

check_cgo() {
  checking "C compiler (CGo)"
  if command -v gcc &>/dev/null; then
    found "gcc $(gcc --version | head -1 | grep -oE '[0-9]+\.[0-9]+' | head -1)"; return 0
  elif command -v clang &>/dev/null; then
    found "clang $(clang --version | head -1 | grep -oE '[0-9]+\.[0-9]+' | head -1)"; return 0
  fi
  missing; MISSING+=("gcc"); return 1
}

check_git() {
  checking "Git"
  if command -v git &>/dev/null; then
    found "$(git --version | awk '{print $3}')"; return 0
  fi
  missing; MISSING+=("git"); return 1
}

check_docker() {
  checking "Docker (integration tests)"
  if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
    found "$(docker --version | awk '{print $3}' | tr -d ',')"; return 0
  elif command -v docker &>/dev/null; then
    warn "Docker installed but daemon not running. Start Docker before running integration tests."
    return 0
  fi
  missing; MISSING+=("docker"); return 1
}

check_hugo() {
  checking "Hugo extended ${HUGO_VERSION}"
  if command -v hugo &>/dev/null; then
    local ver ext
    ver="$(hugo version | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 | tr -d v)"
    ext="$(hugo version | grep -c 'extended' || true)"
    if version_gte "$ver" "$HUGO_VERSION" && [[ "$ext" -gt 0 ]]; then
      found "v$ver extended"; return 0
    elif version_gte "$ver" "$HUGO_VERSION"; then
      echo "${YELLOW}not extended${RESET} (Docsy requires Hugo extended for Sass)"
    else
      echo "${YELLOW}outdated${RESET} (have v$ver, need v${HUGO_VERSION} extended)"
    fi
  else
    missing
  fi
  MISSING+=("hugo-extended"); return 1
}

check_node() {
  checking "Node.js ${NODE_MIN_VERSION}+"
  if command -v node &>/dev/null; then
    local ver
    ver="$(node --version | tr -d 'v' | cut -d. -f1)"
    if [[ "$ver" -ge "$NODE_MIN_VERSION" ]]; then
      found "$(node --version)"; return 0
    fi
    echo "${YELLOW}outdated${RESET} (have v$ver, need v${NODE_MIN_VERSION}+)"
  else
    missing
  fi
  MISSING+=("node"); return 1
}

check_staticcheck() {
  checking "staticcheck"
  if command -v staticcheck &>/dev/null; then
    found "$(staticcheck -version 2>&1 | awk '{print $2}')"; return 0
  fi
  missing; MISSING+=("staticcheck"); return 1
}


# ── Installers ────────────────────────────────────────────────────────────────

install_go() {
  if $CHECK_ONLY; then return; fi
  info "Installing Go ${GO_VERSION}"
  local tarball="go${GO_VERSION}.${PLATFORM}-${ARCH}.tar.gz"
  local url="https://go.dev/dl/${tarball}"
  local tmp; tmp="$(mktemp -d)"
  curl -fsSL --progress-bar "$url" -o "${tmp}/${tarball}"

  if $HAS_SUDO; then
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "${tmp}/${tarball}"
    local go_bin="/usr/local/go/bin"
  else
    mkdir -p "${HOME}/.local/go"
    rm -rf "${HOME}/.local/go"
    tar -C "${HOME}/.local" -xzf "${tmp}/${tarball}"
    mv "${HOME}/.local/go" "${HOME}/.local/go-${GO_VERSION}"
    # Symlink so `go` is accessible without version suffix.
    mkdir -p "$USER_BIN"
    ln -sf "${HOME}/.local/go-${GO_VERSION}/bin/go"   "${USER_BIN}/go"
    ln -sf "${HOME}/.local/go-${GO_VERSION}/bin/gofmt" "${USER_BIN}/gofmt"
    local go_bin="$USER_BIN"
    ensure_user_bin_on_path
  fi
  rm -rf "$tmp"

  export PATH="${go_bin}:${PATH}"
  local profile_line="export PATH=\"${go_bin}:\${PATH}\""
  for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
    if [[ -f "$rc" ]] && ! grep -qF "$go_bin" "$rc"; then
      echo "$profile_line" >> "$rc"
      ok "Added $go_bin to PATH in $rc"
    fi
  done
  ok "Go ${GO_VERSION} installed ($(command -v go))"
}

install_cgo_linux() {
  if $CHECK_ONLY; then return; fi
  info "Installing C compiler"
  case "$PKG_MGR" in
    apt)    sudo apt-get update -qq && pkg_install gcc build-essential ;;
    dnf)    pkg_install gcc gcc-c++ make ;;
    pacman) pkg_install gcc base-devel ;;
  esac
  ok "C compiler installed"
}

install_cgo_macos() {
  if $CHECK_ONLY; then return; fi
  info "Installing Xcode Command Line Tools (provides clang)"
  xcode-select --install 2>/dev/null || true
  ok "Xcode Command Line Tools installed"
}

install_brew() {
  if $CHECK_ONLY; then return; fi
  command -v brew &>/dev/null && return
  info "Installing Homebrew"
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  if [[ "$ARCH" == "arm64" ]]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
    echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> "$HOME/.zprofile"
  fi
  ok "Homebrew installed"
}

install_docker_linux() {
  if $CHECK_ONLY; then return; fi
  if ! $HAS_SUDO; then
    warn "sudo not available — cannot install Docker automatically."
    warn "Install Docker manually: https://docs.docker.com/engine/install/"
    return
  fi
  info "Installing Docker"
  case "$PKG_MGR" in
    apt)
      sudo apt-get update -qq
      pkg_install ca-certificates curl
      sudo install -m 0755 -d /etc/apt/keyrings
      curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
        | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
      sudo chmod a+r /etc/apt/keyrings/docker.gpg
      echo \
        "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
        https://download.docker.com/linux/ubuntu \
        $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
        | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
      sudo apt-get update -qq
      pkg_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
      sudo systemctl enable --now docker
      sudo usermod -aG docker "$USER"
      warn "Log out and back in (or run 'newgrp docker') for the docker group to take effect."
      ;;
    dnf)
      sudo dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo
      pkg_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin
      sudo systemctl enable --now docker
      sudo usermod -aG docker "$USER"
      warn "Log out and back in for the docker group to take effect."
      ;;
    pacman)
      pkg_install docker docker-compose
      sudo systemctl enable --now docker
      sudo usermod -aG docker "$USER"
      warn "Log out and back in for the docker group to take effect."
      ;;
  esac
  ok "Docker installed"
}

install_docker_macos() {
  if $CHECK_ONLY; then return; fi
  info "Installing Docker Desktop for macOS"
  install_brew
  brew install --cask docker
  warn "Open Docker.app from Applications to start the daemon before running integration tests."
  ok "Docker Desktop installed"
}

install_hugo() {
  if $CHECK_ONLY; then return; fi
  info "Installing Hugo extended ${HUGO_VERSION}"
  local os_name; [[ "$PLATFORM" == "macos" ]] && os_name="darwin" || os_name="linux"
  local tarball="hugo_extended_${HUGO_VERSION}_${os_name}-${ARCH}.tar.gz"
  local url="https://github.com/gohugoio/hugo/releases/download/v${HUGO_VERSION}/${tarball}"
  local dest; dest="$(install_bin_dir)"
  local tmp; tmp="$(mktemp -d)"

  curl -fsSL --progress-bar "$url" -o "${tmp}/${tarball}"
  tar -xzf "${tmp}/${tarball}" -C "${tmp}" hugo
  rm -rf "$tmp/${tarball}"

  if $HAS_SUDO; then
    sudo mv "${tmp}/hugo" "${dest}/hugo"
    sudo chmod +x "${dest}/hugo"
  else
    ensure_user_bin_on_path
    mv "${tmp}/hugo" "${dest}/hugo"
    chmod +x "${dest}/hugo"
  fi
  rm -rf "$tmp"
  ok "Hugo ${HUGO_VERSION} extended installed to ${dest}/hugo"
}

install_node_linux() {
  if $CHECK_ONLY; then return; fi
  info "Installing Node.js ${NODE_MIN_VERSION}"
  if ! $HAS_SUDO; then
    warn "sudo not available — install Node.js via nvm instead:"
    warn "  curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.0/install.sh | bash"
    warn "  nvm install ${NODE_MIN_VERSION}"
    return
  fi
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_MIN_VERSION}.x" | sudo -E bash -
  case "$PKG_MGR" in
    apt)    pkg_install nodejs ;;
    dnf)    sudo dnf module install -y "nodejs:${NODE_MIN_VERSION}/common" ;;
    pacman) pkg_install nodejs npm ;;
  esac
  ok "Node.js installed"
}

install_node_macos() {
  if $CHECK_ONLY; then return; fi
  info "Installing Node.js ${NODE_MIN_VERSION} via Homebrew"
  install_brew
  brew install "node@${NODE_MIN_VERSION}"
  brew link --overwrite "node@${NODE_MIN_VERSION}"
  ok "Node.js ${NODE_MIN_VERSION} installed"
}

install_staticcheck() {
  if $CHECK_ONLY; then return; fi
  info "Installing staticcheck"
  go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}"
  local gobin; gobin="$(go env GOPATH)/bin"
  ok "staticcheck installed to ${gobin}/staticcheck"
  if [[ ":$PATH:" != *":${gobin}:"* ]]; then
    warn "Add \$(go env GOPATH)/bin to your PATH to use staticcheck."
    warn "  echo 'export PATH=\"\$(go env GOPATH)/bin:\$PATH\"' >> ~/.bashrc"
  fi
}


# ── Final verification ────────────────────────────────────────────────────────

verify_build() {
  if $CHECK_ONLY; then return; fi
  info "Verifying build"
  cd "$REPO_ROOT/core"
  go mod download -x 2>/dev/null | grep -E '^(go: downloading|done)' || true
  go build ./... && ok "go build ./... — all packages compile"
  go vet   ./... && ok "go vet ./...   — no issues"
  go test  ./... && ok "go test ./...  — unit tests pass"
}

# ── Main ─────────────────────────────────────────────────────────────────────

main() {
  echo
  echo "${BOLD}DPG Development Environment Setup${RESET}"
  echo "Platform : ${PLATFORM} / ${ARCH}"
  echo "Repo     : ${REPO_ROOT}"
  echo "Go needed: ${GO_VERSION}"
  echo "Sudo     : $($HAS_SUDO && echo "available (system-wide install)" || echo "not available (user-local install: ${USER_BIN})")"
  echo

  if $CHECK_ONLY; then
    echo "${YELLOW}${BOLD}Check-only mode — nothing will be installed.${RESET}"
    echo
  fi

  # ── Mandatory tools ─────────────────────────────────────────────────────────

  info "Checking mandatory dependencies"
  check_git || {
    [[ "$PLATFORM" == "linux" ]] && pkg_install git
    [[ "$PLATFORM" == "macos" ]] && { install_brew; brew install git; }
    ok "git installed"
  }
  check_go  || install_go
  check_cgo || {
    [[ "$PLATFORM" == "linux" ]] && install_cgo_linux
    [[ "$PLATFORM" == "macos" ]] && install_cgo_macos
  }
  check_docker || {
    [[ "$PLATFORM" == "linux" ]] && install_docker_linux
    [[ "$PLATFORM" == "macos" ]] && install_docker_macos
  }
  check_staticcheck || install_staticcheck

  # ── Docs tools ───────────────────────────────────────────────────────────────

  if ! $SKIP_DOCS; then
    echo
    info "Checking documentation dependencies"
    check_hugo || install_hugo
    check_node || {
      [[ "$PLATFORM" == "linux" ]] && install_node_linux
      [[ "$PLATFORM" == "macos" ]] && install_node_macos
    }
  fi

  # ── Repo configuration ───────────────────────────────────────────────────────

  echo
  info "Configuring repository"
  git -C "$REPO_ROOT" config core.hooksPath .githooks
  chmod +x "$REPO_ROOT/.githooks/pre-commit"
  ok "Git hooks configured (.githooks/pre-commit)"

  # ── Build verification ────────────────────────────────────────────────────────

  echo
  if $CHECK_ONLY; then
    if [[ ${#MISSING[@]} -gt 0 ]]; then
      echo "${YELLOW}${BOLD}Missing or outdated:${RESET} ${MISSING[*]}"
      echo "Run ${BOLD}bash scripts/setup.sh${RESET} to install them."
      exit 1
    else
      echo "${GREEN}${BOLD}All tools present.${RESET}"
      exit 0
    fi
  fi

  verify_build

  # ── Summary ───────────────────────────────────────────────────────────────────

  echo
  echo "${GREEN}${BOLD}Setup complete.${RESET}"
  echo
  echo "Next steps:"
  echo "  ${BOLD}make build${RESET}              — fast dev build (docs not embedded)"
  echo "  ${BOLD}make build-full${RESET}         — full build with embedded docs"
  echo "  ${BOLD}make test${RESET}               — unit tests"
  echo "  ${BOLD}make test-integration${RESET}   — integration tests (requires Docker)"
  echo "  ${BOLD}make docs-serve${RESET}         — live-reload docs site on localhost:1313"
  echo "  ${BOLD}make lint${RESET}               — staticcheck"
  echo
}

main
