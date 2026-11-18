#!/usr/bin/env bash
# Cross-builds Windows 10/11 x64 binaries from Linux into dist/:
#   Egida-<version>-portable.zip  — egida.exe + mesa\, no installation
#   Egida-<version>-Setup.exe     — NSIS installer (Start menu + desktop shortcut)
#
# Requirements: go, gcc-mingw-w64-x86-64, nsis (makensis), 7z, zip, curl.
# The binary is linked statically so it needs only system DLLs. mesa\ holds the
# Mesa llvmpipe software OpenGL renderer (pal1000/mesa-dist-win) plus a copy of
# egida.exe; the program restarts itself from there only when the system has
# no OpenGL 2.1 (no GPU driver, RDP, VM).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-$(sed -n 's/.*appVersion *= *"\(.*\)".*/\1/p' "${ROOT}/main.go")}"
DIST="${ROOT}/dist"
MESA_VERSION="${MESA_VERSION:-26.2.5}"
MESA_CACHE="${MESA_CACHE:-${HOME}/.cache/egida/mesa-${MESA_VERSION}}"
rm -rf "${DIST}/windows"
mkdir -p "${DIST}/windows/mesa"
cd "${ROOT}"

if [ ! -f "${MESA_CACHE}/x64/libgallium_wgl.dll" ]; then
  mkdir -p "${MESA_CACHE}"
  curl -fL -o "${MESA_CACHE}/mesa.7z" \
    "https://github.com/pal1000/mesa-dist-win/releases/download/${MESA_VERSION}/mesa3d-${MESA_VERSION}-release-mingw.7z"
  7z x -y -o"${MESA_CACHE}" "${MESA_CACHE}/mesa.7z" x64/opengl32.dll x64/libgallium_wgl.dll >/dev/null
  rm -f "${MESA_CACHE}/mesa.7z"
fi
cp "${MESA_CACHE}/x64/opengl32.dll" "${MESA_CACHE}/x64/libgallium_wgl.dll" "${DIST}/windows/mesa/"

GOWINRES="$(go env GOPATH)/bin/go-winres"
[ -x "${GOWINRES}" ] || go install github.com/tc-hib/go-winres@v0.3.3
"${GOWINRES}" simply \
  --arch amd64 \
  --icon build/icon.png \
  --manifest gui \
  --product-name "Эгида" \
  --file-description "Эгида — управление задачами и этапами работы отделов" \
  --product-version "${VERSION}.0" \
  --file-version "${VERSION}.0" \
  --copyright "Горшков Сергей Владимирович, https://nookbat.ru/" \
  --original-filename egida.exe \
  --out rsrc
trap 'rm -f "${ROOT}"/rsrc_windows_*.syso' EXIT

CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
  go build -trimpath -ldflags '-s -w -H windowsgui -extldflags=-static' -o "${DIST}/windows/egida.exe" .
cp "${DIST}/windows/egida.exe" "${DIST}/windows/mesa/egida.exe"
PORTABLE="${DIST}/Egida-${VERSION}-portable.zip"
rm -rf "${DIST}/portable" "${PORTABLE}"
mkdir -p "${DIST}/portable/Egida"
cp -r "${DIST}/windows/egida.exe" "${DIST}/windows/mesa" "${DIST}/portable/Egida/"
(cd "${DIST}/portable" && zip -qr9 "${PORTABLE}" Egida)
rm -rf "${DIST}/portable"
echo "Built ${PORTABLE}"

cp build/icon.ico "${DIST}/windows/egida.ico"
makensis -V2 -DVERSION="${VERSION}" -DSRCDIR="${DIST}/windows" -DOUTFILE="${DIST}/Egida-${VERSION}-Setup.exe" \
  packaging/windows/installer.nsi
echo "Built ${DIST}/Egida-${VERSION}-Setup.exe"
