#!/usr/bin/env bash
# Install all required tools for vmware-vm-bootstrap
# Supports: Ubuntu/Debian, CentOS/RHEL/Fedora, macOS

set -euo pipefail

# --- Colors ---
BOLD="\033[1m"
CYAN="\033[36m"
GREEN="\033[32m"
YELLOW="\033[33m"
RED="\033[31m"
RESET="\033[0m"

# --- Versions ---
GO_VERSION="1.26.1"
GOLANGCI_LINT_VERSION="latest"
GOVC_VERSION="latest"
SOPS_VERSION="latest"

# --- Helpers ---
info()    { printf "  ${CYAN}${1}${RESET}\n"; }
success() { printf "  ${GREEN}✓ ${1}${RESET}\n"; }
warn()    { printf "  ${YELLOW}⚠ ${1}${RESET}\n"; }
error()   { printf "  ${RED}✗ ${1}${RESET}\n"; }
header()  { printf "\n${BOLD}${1}${RESET}\n"; }

need_sudo() {
    if [ "$EUID" -ne 0 ]; then
        echo sudo
    fi
}

# --- OS Detection ---
detect_os() {
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "macos"
    elif [ -f /etc/os-release ]; then
        . /etc/os-release
        case "$ID" in
            ubuntu|debian|linuxmint) echo "debian" ;;
            centos|rhel|rocky|almalinux) echo "rhel" ;;
            fedora) echo "fedora" ;;
            *) echo "unknown" ;;
        esac
    else
        echo "unknown"
    fi
}

# --- Architecture ---
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "amd64" ;;
    esac
}

OS=$(detect_os)
ARCH=$(detect_arch)
SUDO=$(need_sudo)

printf "\n${BOLD}vmware-vm-bootstrap — install-requirements${RESET}\n"
printf "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}\n"
printf "  OS:   $OS\n"
printf "  Arch: $ARCH\n"

# ─────────────────────────────────────────────────────────
# 1. Go
# ─────────────────────────────────────────────────────────
header "1. Go ${GO_VERSION}"

CURRENT_GO_LOCAL=$(GOTOOLCHAIN=local go version 2>/dev/null | grep -oP 'go\K[0-9]+\.[0-9]+(\.[0-9]+)?' || echo "none")

if [ "$CURRENT_GO_LOCAL" = "$GO_VERSION" ]; then
    success "Go ${GO_VERSION} already installed (local toolchain)"
else
    info "Installing Go ${GO_VERSION} (current local: ${CURRENT_GO_LOCAL})..."

    GO_TARBALL="go${GO_VERSION}.linux-${ARCH}.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TARBALL}"

    if [[ "$OS" == "macos" ]]; then
        GO_TARBALL="go${GO_VERSION}.darwin-${ARCH}.tar.gz"
        GO_URL="https://go.dev/dl/${GO_TARBALL}"
    fi

    TMPDIR=$(mktemp -d)
    trap "rm -rf $TMPDIR" EXIT

    curl -sfL "$GO_URL" -o "$TMPDIR/$GO_TARBALL"
    $SUDO rm -rf /usr/local/go
    $SUDO tar -C /usr/local -xzf "$TMPDIR/$GO_TARBALL"

    # Ensure PATH is set
    if ! echo "$PATH" | grep -q "/usr/local/go/bin"; then
        warn "Add to your shell profile: export PATH=\$PATH:/usr/local/go/bin"
    fi

    success "Go ${GO_VERSION} installed → /usr/local/go"
fi

export PATH=$PATH:/usr/local/go/bin:$(go env GOPATH)/bin

# ─────────────────────────────────────────────────────────
# 2. golangci-lint
# ─────────────────────────────────────────────────────────
header "2. golangci-lint"

GOLANGCI_LINT_BIN_DIR="$(go env GOPATH)/bin"
if echo "$PATH" | grep -q "/usr/local/bin"; then
    GOLANGCI_LINT_BIN_DIR="/usr/local/bin"
fi

if command -v golangci-lint >/dev/null 2>&1; then
    success "golangci-lint already installed ($(golangci-lint --version 2>&1 | head -1))"
else
    info "Installing golangci-lint..."
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
        | sh -s -- -b "$GOLANGCI_LINT_BIN_DIR" $GOLANGCI_LINT_VERSION
    success "golangci-lint installed → $GOLANGCI_LINT_BIN_DIR"
fi

# Ensure golangci-lint is discoverable on PATH for non-interactive shells
if ! command -v golangci-lint >/dev/null 2>&1; then
    if [ -x "$(go env GOPATH)/bin/golangci-lint" ] && [ -d /usr/local/bin ]; then
        $SUDO ln -sf "$(go env GOPATH)/bin/golangci-lint" /usr/local/bin/golangci-lint
        success "golangci-lint linked → /usr/local/bin/golangci-lint"
    else
        warn "golangci-lint not found in PATH. Add: export PATH=\$PATH:$(go env GOPATH)/bin"
    fi
fi

# ─────────────────────────────────────────────────────────
# 3. govc (VMware CLI) — binary download (go install doesn't work: replace directives)
# ─────────────────────────────────────────────────────────
header "3. govc (VMware CLI)"

if command -v govc >/dev/null 2>&1; then
    success "govc already installed ($(govc version 2>&1))"
else
    info "Installing govc binary from GitHub releases..."

    GOVC_URL="https://github.com/vmware/govmomi/releases/latest/download/govc_Linux_x86_64.tar.gz"
    if [[ "$OS" == "macos" ]]; then
        if [[ "$ARCH" == "arm64" ]]; then
            GOVC_URL="https://github.com/vmware/govmomi/releases/latest/download/govc_Darwin_arm64.tar.gz"
        else
            GOVC_URL="https://github.com/vmware/govmomi/releases/latest/download/govc_Darwin_x86_64.tar.gz"
        fi
    elif [[ "$ARCH" == "arm64" ]]; then
        GOVC_URL="https://github.com/vmware/govmomi/releases/latest/download/govc_Linux_arm64.tar.gz"
    fi

    TMPDIR_GOVC=$(mktemp -d)
    curl -sfL "$GOVC_URL" | tar -xz -C "$TMPDIR_GOVC"
    $SUDO mv "$TMPDIR_GOVC/govc" /usr/local/bin/govc
    $SUDO chmod +x /usr/local/bin/govc
    rm -rf "$TMPDIR_GOVC"

    success "govc installed → /usr/local/bin/govc"
fi

# ─────────────────────────────────────────────────────────
# 4. govulncheck
# ─────────────────────────────────────────────────────────
header "4. govulncheck"

if command -v govulncheck >/dev/null 2>&1; then
    success "govulncheck already installed ($(govulncheck -version 2>&1 | head -1))"
else
    info "Installing govulncheck..."
    GOBIN="$(go env GOPATH)/bin"
    if echo "$PATH" | grep -q "/usr/local/bin"; then
        if [ -w "/usr/local/bin" ]; then
            GOBIN="/usr/local/bin"
        fi
    fi
    GOBIN="$GOBIN" GOTOOLCHAIN=local go install golang.org/x/vuln/cmd/govulncheck@latest
    success "govulncheck installed → $GOBIN"
fi

# Ensure govulncheck is discoverable on PATH for non-interactive shells
if ! command -v govulncheck >/dev/null 2>&1; then
    if [ -x "$(go env GOPATH)/bin/govulncheck" ] && [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
        $SUDO ln -sf "$(go env GOPATH)/bin/govulncheck" /usr/local/bin/govulncheck
        success "govulncheck linked → /usr/local/bin/govulncheck"
    else
        warn "govulncheck not found in PATH. Add: export PATH=\$PATH:$(go env GOPATH)/bin"
    fi
fi

# ─────────────────────────────────────────────────────────
# 5. sops
# ─────────────────────────────────────────────────────────
header "5. sops"

if command -v sops >/dev/null 2>&1; then
    success "sops already installed ($(sops --version 2>&1 | head -1))"
else
    info "Installing sops binary from GitHub releases..."

    SOPS_BINARY="sops-v3.9.4.linux.amd64"
    SOPS_URL="https://github.com/getsops/sops/releases/latest/download/${SOPS_BINARY}"

    if [[ "$OS" == "macos" ]]; then
        SOPS_BINARY="sops-v3.9.4.darwin.amd64"
        if [[ "$ARCH" == "arm64" ]]; then
            SOPS_BINARY="sops-v3.9.4.darwin.arm64"
        fi
        SOPS_URL="https://github.com/getsops/sops/releases/latest/download/${SOPS_BINARY}"
    elif [[ "$ARCH" == "arm64" ]]; then
        SOPS_BINARY="sops-v3.9.4.linux.arm64"
        SOPS_URL="https://github.com/getsops/sops/releases/latest/download/${SOPS_BINARY}"
    fi

    TMPDIR_SOPS=$(mktemp -d)
    curl -sfL "$SOPS_URL" -o "$TMPDIR_SOPS/sops"
    $SUDO mv "$TMPDIR_SOPS/sops" /usr/local/bin/sops
    $SUDO chmod +x /usr/local/bin/sops
    rm -rf "$TMPDIR_SOPS"

    success "sops installed → /usr/local/bin/sops"
fi

# ─────────────────────────────────────────────────────────
# 6. xorriso + genisoimage (system packages)
# ─────────────────────────────────────────────────────────
header "6. xorriso + genisoimage"

install_pkg() {
    local pkg=$1
    if command -v "$pkg" >/dev/null 2>&1; then
        success "$pkg already installed"
        return
    fi

    info "Installing $pkg..."
    case "$OS" in
        debian)
            $SUDO apt-get install -y -q "$pkg" >/dev/null 2>&1 && success "$pkg installed" || error "$pkg install failed"
            ;;
        rhel)
            $SUDO yum install -y "$pkg" >/dev/null 2>&1 && success "$pkg installed" || error "$pkg install failed"
            ;;
        fedora)
            $SUDO dnf install -y "$pkg" >/dev/null 2>&1 && success "$pkg installed" || error "$pkg install failed"
            ;;
        macos)
            brew install "$pkg" >/dev/null 2>&1 && success "$pkg installed" || error "$pkg install failed (try: brew install $pkg)"
            ;;
        *)
            warn "$pkg: unknown OS — install manually"
            ;;
    esac
}

install_pkg xorriso
install_pkg genisoimage

# ─────────────────────────────────────────────────────────
# Done
# ─────────────────────────────────────────────────────────
printf "\n${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}\n"
printf "${GREEN}${BOLD}✓ All requirements installed${RESET}\n\n"
printf "  Next: ${CYAN}make build${RESET} to verify everything compiles\n\n"
