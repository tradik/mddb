#!/usr/bin/env bash
#
# test-export-brand.sh — tests for brand/ and scripts/export-brand.sh.
#
#   1. brand/mddb-logo.svg is byte-identical to docs/logo.svg (the master)
#   2. every file of the size matrix (LOGO-009) exists with its exact pixel
#      size, read by `magick identify`, and stays under its byte cap
#   3. favicon.ico holds exactly the frames 16, 32 and 48
#   4. banners and the maskable icon carry no alpha channel
#   5. the Chrome Web Store icon keeps 16 px of transparent padding
#   6. the maskable icon draws nothing outside its central 80% safe circle
#   7. brand/ stays under 1 MB
#   8. the export refuses to run, with a clear message, when a tool is missing
#   9. when resvg and oxipng are installed: a fresh export reproduces the
#      committed rasters byte for byte (skipped otherwise)
#
# Needs ImageMagick 7 (`magick`).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BRAND="${REPO_ROOT}/brand"
EXPORT="${REPO_ROOT}/scripts/export-brand.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; NC='\033[0m'
PASS=0; FAIL=0
ok() { echo -e "${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
bad() { echo -e "${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }
skip() { echo -e "${YELLOW}-${NC} $1"; }

command -v magick >/dev/null 2>&1 || { echo "test-export-brand: needs ImageMagick 7 (magick)" >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

KB=1024
# path:width:height:max-bytes
MATRIX=(
	"icon/icon-16.png:16:16:$((2 * KB))"
	"icon/icon-32.png:32:32:$((2 * KB))"
	"icon/icon-48.png:48:48:$((3 * KB))"
	"icon/icon-128.png:128:128:$((50 * KB))"
	"icon/icon-180.png:180:180:$((50 * KB))"
	"icon/icon-192.png:192:192:$((50 * KB))"
	"icon/icon-256.png:256:256:$((50 * KB))"
	"icon/icon-512.png:512:512:$((50 * KB))"
	"icon/icon-512-maskable.png:512:512:$((50 * KB))"
	"icon/chrome-store-128.png:128:128:$((50 * KB))"
	"icon/favicon.ico:48:48:$((50 * KB))"
	"banner/wp-banner-772x250.png:772:250:$((300 * KB))"
	"banner/wp-banner-1544x500.png:1544:500:$((300 * KB))"
	"banner/og-1200x630.png:1200:630:$((300 * KB))"
	"banner/github-social-1280x640.png:1280:640:$((300 * KB))"
	"banner/chrome-promo-small-440x280.png:440:280:$((300 * KB))"
	"banner/chrome-promo-marquee-1400x560.png:1400:560:$((300 * KB))"
)

# 1: the master.
if cmp -s "${BRAND}/mddb-logo.svg" "${REPO_ROOT}/docs/logo.svg"; then
	ok "brand/mddb-logo.svg is docs/logo.svg"
else bad "brand/mddb-logo.svg differs from docs/logo.svg"; fi
for f in mddb-mark.svg USAGE.md; do
	if [[ -f "${BRAND}/${f}" ]]; then ok "brand/${f} present"; else bad "brand/${f} missing"; fi
done

# 2: dimensions and byte caps. For the .ico, the largest frame is checked here
# and every frame in 3.
for entry in "${MATRIX[@]}"; do
	IFS=: read -r path w h cap <<<"${entry}"
	f="${BRAND}/${path}"
	if [[ ! -f "${f}" ]]; then bad "${path} missing"; continue; fi
	dims="$(magick identify -format '%wx%h\n' "${f}" | sort -t x -k1,1n | tail -1)"
	bytes="$(wc -c <"${f}")"
	if [[ "${dims}" != "${w}x${h}" ]]; then bad "${path} is ${dims}, want ${w}x${h}"
	elif (( bytes > cap )); then bad "${path} is ${bytes} B, cap ${cap} B"
	else ok "${path} ${dims}, ${bytes} B"; fi
done

# 3: favicon frames.
frames="$(magick identify -format '%wx%h ' "${BRAND}/icon/favicon.ico" 2>/dev/null || true)"
if [[ "${frames}" == "16x16 32x32 48x48 " ]]; then ok "favicon.ico frames 16, 32, 48"
else bad "favicon.ico frames: '${frames}'"; fi

# 4: opaque files.
for path in icon/icon-512-maskable.png banner/*.png; do
	for f in "${BRAND}"/${path}; do
		[[ -f "${f}" ]] || continue
		a="$(magick identify -format '%A' "${f}")"
		if [[ "${a}" == "Undefined" || "${a}" == "False" ]]; then ok "${f#"${BRAND}/"} opaque"
		else bad "${f#"${BRAND}/"} has an alpha channel"; fi
	done
done

# 5: Chrome Web Store padding — the artwork's bounding box is 96×96 at +16+16.
store="${BRAND}/icon/chrome-store-128.png"
if [[ -f "${store}" ]]; then
	box="$(magick "${store}" -alpha extract -threshold 0 -format '%@' info:)"
	if [[ "${box}" == "96x96+16+16" ]]; then ok "chrome-store-128 artwork 96x96 with 16 px padding"
	else bad "chrome-store-128 artwork box ${box}, want 96x96+16+16"; fi
fi

# 6: maskable safe zone — outside the circle of radius 204.8 px the icon is
# exactly the background gradient.
mask="${BRAND}/icon/icon-512-maskable.png"
if [[ -f "${mask}" ]]; then
	outside="$(magick "${mask}" \( -size 512x512 'gradient:#0f172a-#1e293b' \) \
		-compose difference -composite -colorspace gray \
		\( -size 512x512 xc:white -fill black -draw 'circle 256,256 256,51.2' \) \
		-compose multiply -composite -format '%[fx:maxima]' info:)"
	if awk -v m="${outside}" 'BEGIN { exit !(m < 0.02) }'; then ok "maskable artwork inside the 80% safe zone"
	else bad "maskable draws outside the safe zone (max diff ${outside})"; fi
fi

# 7: total size.
total="$(find "${BRAND}" -type f -exec cat {} + | wc -c)"
if (( total < 1024 * KB )); then ok "brand/ is ${total} B"; else bad "brand/ is ${total} B, over 1 MB"; fi

# 8: missing tools.
for tool in RESVG MAGICK OXIPNG; do
	if env "BRAND_${tool}=/nonexistent/${tool,,}" bash "${EXPORT}" "${TMP}/refuse" >"${TMP}/out" 2>&1; then
		bad "export ran without ${tool,,}"
	elif grep -q "not found" "${TMP}/out"; then ok "export refuses without ${tool,,}"
	else bad "export failed without ${tool,,} but said: $(head -1 "${TMP}/out")"; fi
done

# 9: reproducibility.
if command -v resvg >/dev/null 2>&1 && command -v oxipng >/dev/null 2>&1; then
	bash "${EXPORT}" "${TMP}/fresh" >/dev/null 2>&1
	diffs=0
	for entry in "${MATRIX[@]}"; do
		path="${entry%%:*}"
		cmp -s "${BRAND}/${path}" "${TMP}/fresh/${path}" || { bad "${path} differs from a fresh export"; diffs=1; }
	done
	(( diffs == 0 )) && ok "a fresh export reproduces every committed raster"
else
	skip "resvg/oxipng not installed: reproducibility not checked"
fi

echo
echo "passed: ${PASS}, failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
