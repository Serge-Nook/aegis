#!/usr/bin/env bash
# Builds the Linux binary, egida_<version>_amd64.deb (Debian/Ubuntu/Astra Linux)
# and Egida-<version>-x86_64.AppImage into dist/.
#
# Run it on an old distribution (Debian 10 "buster", glibc 2.28 — the base of
# Astra Linux 1.7) so the binary also starts on Astra 1.7/1.8 and newer systems.
# packaging/release.sh does that inside a debian:buster Docker container.
#
# Requirements: go, gcc, pkg-config, libgl1-mesa-dev, xorg-dev, dpkg-deb, curl.
# GLFW is built X11-only (-tags x11): its Wayland backend needs libwayland >= 1.20,
# newer than Debian 10 ships; Astra (Fly) runs X11, Wayland sessions use XWayland.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-$(sed -n 's/.*appVersion *= *"\(.*\)".*/\1/p' "${ROOT}/main.go")}"
DIST="${ROOT}/dist"
TOOLS="${DIST}/tools"
mkdir -p "${DIST}" "${TOOLS}"

cd "${ROOT}"
CGO_ENABLED=1 go build -tags x11 -trimpath -ldflags '-s -w' -o "${DIST}/egida" .

install_tree() {
  local dest="$1"
  install -Dm755 "${DIST}/egida" "${dest}/usr/bin/egida"
  install -Dm644 packaging/egida.desktop "${dest}/usr/share/applications/egida.desktop"
  for s in 16 24 32 48 64 128 256 512; do
    install -Dm644 "build/icons/${s}x${s}.png" "${dest}/usr/share/icons/hicolor/${s}x${s}/apps/egida.png"
  done
}

# --- .deb ---
PKG="${DIST}/deb/egida_${VERSION}_amd64"
rm -rf "${PKG}"
mkdir -p "${PKG}/DEBIAN"
install_tree "${PKG}"
install -Dm644 packaging/linux/egida.service "${PKG}/lib/systemd/system/egida.service"
install -Dm644 packaging/linux/egida.default "${PKG}/etc/default/egida"
install -Dm644 README.md "${PKG}/usr/share/doc/egida/README.md"
install -Dm644 LICENSE "${PKG}/usr/share/doc/egida/copyright"
echo "/etc/default/egida" > "${PKG}/DEBIAN/conffiles"
SIZE="$(du -sk "${PKG}" | cut -f1)"
cat > "${PKG}/DEBIAN/control" <<CTRL
Package: egida
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: amd64
Installed-Size: ${SIZE}
Maintainer: Горшков Сергей Владимирович <i@nookbat.ru>
Homepage: https://nookbat.ru/
Depends: libc6 (>= 2.28), libgl1, libx11-6, libxcursor1, libxrandr2, libxinerama1, libxi6, libxxf86vm1, libxkbcommon0
Recommends: fonts-noto-cjk | fonts-wqy-microhei | fonts-droid-fallback
Description: Departmental task and workflow management
 «Эгида» manages tasks and work stages of departments: kanban boards with
 drag-and-drop, checklists, comments with @mentions, attachments, history,
 deadlines and notifications, roles, analytics with charts and CSV/Excel
 export. Works standalone (SQLite, this computer is the server), with a
 shared PostgreSQL database, or as a headless server (egida --serve,
 systemd unit egida.service).
CTRL
cat > "${PKG}/DEBIAN/postinst" <<'POST'
#!/bin/sh
set -e
if [ "$1" = "configure" ] && command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
POST
cat > "${PKG}/DEBIAN/prerm" <<'PRERM'
#!/bin/sh
set -e
if [ "$1" = "remove" ] && command -v systemctl >/dev/null 2>&1; then
  systemctl stop egida.service >/dev/null 2>&1 || true
  systemctl disable egida.service >/dev/null 2>&1 || true
fi
PRERM
chmod 755 "${PKG}/DEBIAN/postinst" "${PKG}/DEBIAN/prerm"
dpkg-deb --root-owner-group -Zxz --build "${PKG}" "${DIST}/egida_${VERSION}_amd64.deb" >/dev/null
echo "Built ${DIST}/egida_${VERSION}_amd64.deb"

# --- AppImage ---
if [ ! -x "${TOOLS}/appimagetool/AppRun" ]; then
  curl -fsSL -o "${TOOLS}/appimagetool.AppImage" \
    https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
  chmod +x "${TOOLS}/appimagetool.AppImage"
  (cd "${TOOLS}" && ./appimagetool.AppImage --appimage-extract >/dev/null && rm -rf appimagetool && mv squashfs-root appimagetool && rm appimagetool.AppImage)
fi
APPDIR="${DIST}/AppDir"
rm -rf "${APPDIR}"
install_tree "${APPDIR}"
cp packaging/egida.desktop "${APPDIR}/egida.desktop"
cp build/icon.png "${APPDIR}/egida.png"
cat > "${APPDIR}/AppRun" <<'RUN'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
exec "${HERE}/usr/bin/egida" "$@"
RUN
chmod +x "${APPDIR}/AppRun"
ARCH=x86_64 "${TOOLS}/appimagetool/AppRun" --no-appstream "${APPDIR}" "${DIST}/Egida-${VERSION}-x86_64.AppImage" >/dev/null
echo "Built ${DIST}/Egida-${VERSION}-x86_64.AppImage"
