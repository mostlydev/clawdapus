#!/bin/sh
# Clawdapus installer — curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh
set -eu

REPO="mostlydev/clawdapus"
INSTALL_DIR="${CLAW_INSTALL_DIR:-$HOME/.local/bin}"

info() { printf '  %s\n' "$@"; }
err()  { printf 'Error: %s\n' "$@" >&2; exit 1; }

# --- Detect OS ---
OS="$(uname -s)"
case "$OS" in
  Linux*)  OS="linux"  ;;
  Darwin*) OS="darwin" ;;
  *)       err "Unsupported OS: $OS" ;;
esac

# --- Detect architecture ---
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       err "Unsupported architecture: $ARCH" ;;
esac

info "Detected platform: ${OS}/${ARCH}"

# --- Get latest release tag ---
info "Fetching latest release..."
TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//')"

if [ -z "$TAG" ]; then
  err "Could not determine latest release tag"
fi

info "Latest release: ${TAG}"

# --- Download tarball and checksums ---
VERSION="${TAG#v}"
TARBALL="claw_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

info "Downloading ${TARBALL}..."
curl -fsSL "${BASE_URL}/${TARBALL}" -o "${TMPDIR}/${TARBALL}"

info "Downloading checksums..."
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMPDIR}/checksums.txt"

# --- Verify SHA256 ---
info "Verifying checksum..."
EXPECTED="$(grep "${TARBALL}" "${TMPDIR}/checksums.txt" | awk '{print $1}')"
if [ -z "$EXPECTED" ]; then
  err "Tarball ${TARBALL} not found in checksums.txt"
fi

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMPDIR}/${TARBALL}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMPDIR}/${TARBALL}" | awk '{print $1}')"
else
  err "No sha256sum or shasum found — cannot verify integrity"
fi

if [ "$EXPECTED" != "$ACTUAL" ]; then
  err "Checksum mismatch!\n  expected: ${EXPECTED}\n  actual:   ${ACTUAL}"
fi

info "Checksum verified."

# --- Extract and install ---
mkdir -p "$INSTALL_DIR"
tar -xzf "${TMPDIR}/${TARBALL}" -C "$TMPDIR"
mv "${TMPDIR}/claw" "${INSTALL_DIR}/claw"
chmod +x "${INSTALL_DIR}/claw"

info "Installed claw to ${INSTALL_DIR}/claw"

# --- Check PATH ---
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    info "${INSTALL_DIR} is not in your PATH."
    info "Add it with:"
    info "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

echo ""
info "Run 'claw doctor' to verify your installation."
