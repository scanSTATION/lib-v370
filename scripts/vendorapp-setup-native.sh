#!/bin/bash
#
# setup-native.sh — Extract the vendor app backend from the installed
# Flatpak and set it up for native (non-Flatpak) execution.
#
# This script does NOT require root if /opt/epsonscan2 is writable by the
# current user. Otherwise it uses sudo for the copy and symlink steps.
#
# After running this script, you can use:
#   scanstation --driver epsonscan2 --epsonscan2-native
# or invoke the binary directly:
#   /opt/epsonscan2/bin/epsonscan2 --list
#
set -euo pipefail

FLATPAK_ID="net.epson.epsonscan2"
INSTALL_DIR="/opt/epsonscan2"
APP_DIR="/var/lib/flatpak/app/${FLATPAK_ID}/current/active/files"

# --- checks --------------------------------------------------------------

if ! command -v flatpak &>/dev/null; then
    echo "Error: flatpak is not installed." >&2
    exit 1
fi

if ! flatpak list 2>/dev/null | grep -q "${FLATPAK_ID}"; then
    echo "Error: ${FLATPAK_ID} is not installed via Flatpak." >&2
    echo "Install it with: flatpak install flathub ${FLATPAK_ID}" >&2
    exit 1
fi

if [ ! -d "${APP_DIR}" ]; then
    echo "Error: Flatpak app directory not found at ${APP_DIR}" >&2
    exit 1
fi

# --- determine if we need sudo -------------------------------------------

SUDO=""
if [ ! -w "/opt" ] && [ "$(id -u)" -ne 0 ]; then
    SUDO="sudo"
fi

echo "==> Extracting the vendor app from Flatpak to ${INSTALL_DIR}"

${SUDO} mkdir -p "${INSTALL_DIR}"

# Copy the entire app directory (bin, lib, share)
${SUDO} cp -a "${APP_DIR}/bin" "${INSTALL_DIR}/"
${SUDO} cp -a "${APP_DIR}/lib" "${INSTALL_DIR}/"
${SUDO} cp -a "${APP_DIR}/share" "${INSTALL_DIR}/"

# Create the /app symlink that the libraries expect
# The vendor app libraries have a hardcoded path: /app/lib/x86_64-linux-gnu/epsonscan2/
if [ -L "/app" ] || [ -d "/app" ]; then
    echo "   /app already exists — checking if it points to the right place"
    if [ "$(readlink -f /app 2>/dev/null)" != "$(readlink -f ${INSTALL_DIR})" ]; then
        echo "   WARNING: /app exists but does not point to ${INSTALL_DIR}" >&2
        echo "   The vendor app backend requires /app to point to ${INSTALL_DIR}" >&2
        echo "   Please fix this manually or ensure the symlink is correct." >&2
    fi
else
    echo "==> Creating /app symlink -> ${INSTALL_DIR}"
    ${SUDO} ln -s "${INSTALL_DIR}" "/app"
fi

# Fix library permissions
${SUDO} chmod -R a+rX "${INSTALL_DIR}"

# Set up the SANE backend so scanimage can find it
SANE_BACKEND_DIR="/usr/lib/sane"
SANE_CONFIG_DIR="/etc/sane.d"

echo "==> Registering SANE backend"

if [ -d "${SANE_BACKEND_DIR}" ]; then
    ${SUDO} ln -sf "${INSTALL_DIR}/lib/x86_64-linux-gnu/epsonscan2/libsane-epsonscan2.so" \
        "${SANE_BACKEND_DIR}/libsane-epsonscan2.so"

    # Add the vendor app to dll.conf if not already present
    if [ -f "${SANE_CONFIG_DIR}/dll.conf" ]; then
        if ! grep -q "^epsonscan2" "${SANE_CONFIG_DIR}/dll.conf"; then
            echo "epsonscan2" | ${SUDO} tee -a "${SANE_CONFIG_DIR}/dll.conf" >/dev/null
        fi
    fi

    # Create a minimal epsonscan2.conf for SANE
    ${SUDO} tee "${SANE_CONFIG_DIR}/epsonscan2.conf" >/dev/null <<EOF
# The vendor app backend configuration
# USB scanner support
usb 0x04b8 0x014a
EOF

    echo "   SANE backend registered. You can now use 'scanimage -L' to find the scanner."
else
    echo "   SANE not found — skipping SANE backend registration." >&2
    echo "   You can still use the vendor app directly." >&2
fi

# Set up udev rules for USB access
UDEV_RULES="/etc/udev/rules.d/60-epsonscan2.rules"

echo "==> Installing udev rules for USB access"
${SUDO} tee "${UDEV_RULES}" >/dev/null <<'EOF'
# The vendor app — USB scanner access
ATTRS{idVendor}=="04b8", ATTRS{idProduct}=="014a", MODE="0664", GROUP="scanner"
EOF

if command -v udevadm &>/dev/null; then
    ${SUDO} udevadm control --reload-rules 2>/dev/null || true
    ${SUDO} udevadm trigger --action=add --subsystem-match=usb 2>/dev/null || true
fi

echo ""
echo "==> Setup complete!"
echo ""
echo "    Binary:     ${INSTALL_DIR}/bin/epsonscan2"
echo "    SANE:       scanimage -L (should show the V370)"
echo ""
echo "    Test with:"
echo "      ${INSTALL_DIR}/bin/epsonscan2 --list"
echo "      scanimage -L"
echo "      scanstation --driver epsonscan2 --epsonscan2-native --list"
