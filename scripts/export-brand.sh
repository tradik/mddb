#!/usr/bin/env bash
#
# export-brand.sh — render the MDDB brand masters into the rasters under brand/.
#
# Sources (committed, edited by hand):
#   brand/mddb-logo.svg  the master — byte-identical to docs/logo.svg (512×512
#                        tile: dark gradient square with the cyan hex mark)
#   brand/mddb-mark.svg  the master without its tile (hex mark only)
#
# Outputs (the size matrix of LOGO-009; brand/USAGE.md lists the consumers):
#   icon/icon-{16,32,48,128,180,192,256,512}.png   the tile, square
#   icon/icon-512-maskable.png    opaque, mark inside the central 80% safe zone
#   icon/chrome-store-128.png     96×96 tile + 16 px transparent padding
#   icon/favicon.ico              frames 16, 32, 48
#   banner/wp-banner-772x250.png, wp-banner-1544x500.png, og-1200x630.png,
#   banner/github-social-1280x640.png, chrome-promo-small-440x280.png,
#   banner/chrome-promo-marquee-1400x560.png       opaque, mark centred
#
# Usage: scripts/export-brand.sh [OUT_DIR]     (default: brand/ in the repo)
#        make brand-icons
#
# Tools:
#   resvg    renders the SVGs. ImageMagick's own SVG renderer (MSVG — the one
#            `magick -list format` shows when librsvg is absent) paints the
#            master's gradients black and drops its glow filter, so it is never
#            used. Install: cargo install resvg --version 0.49.0 --locked
#   magick   ImageMagick 7: composes padding, banners and the .ico.
#   oxipng   lossless PNG optimiser; without it icon-512 exceeds its 50 KB cap.
#            Install: cargo install oxipng --version 10.2.1 --locked
# Override the binaries with BRAND_RESVG / BRAND_MAGICK / BRAND_OXIPNG.
#
# Every raster is rendered straight from the vector at its final size — no
# raster is ever upscaled. Output is deterministic for a given resvg/ImageMagick
# pair: no timestamps or other ancillary chunks are written.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BRAND="${REPO_ROOT}/brand"
OUT="${1:-${BRAND}}"
RESVG="${BRAND_RESVG:-resvg}"
MAGICK="${BRAND_MAGICK:-magick}"
OXIPNG="${BRAND_OXIPNG:-oxipng}"
RESVG_PIN="0.49.0"

ICON_SIZES=(16 32 48 128 180 192 256 512)
ICO_SIZES=(16 32 48)
# name:width:height
BANNERS=(
	"wp-banner-772x250:772:250"
	"wp-banner-1544x500:1544:500"
	"og-1200x630:1200:630"
	"github-social-1280x640:1280:640"
	"chrome-promo-small-440x280:440:280"
	"chrome-promo-marquee-1400x560:1400:560"
)
# The master's own tile gradient (bgGrad in mddb-logo.svg).
BG="gradient:#0f172a-#1e293b"

die() { echo "export-brand: $*" >&2; exit 1; }

command -v "${RESVG}" >/dev/null 2>&1 ||
	die "'${RESVG}' not found. Install resvg ${RESVG_PIN}: cargo install resvg --version ${RESVG_PIN} --locked"
command -v "${MAGICK}" >/dev/null 2>&1 ||
	die "'${MAGICK}' not found. Install ImageMagick 7 (the 'magick' command)."
command -v "${OXIPNG}" >/dev/null 2>&1 ||
	die "'${OXIPNG}' not found. Install oxipng: cargo install oxipng --version 10.2.1 --locked"

have="$("${RESVG}" --version 2>/dev/null || true)"
[[ "${have}" == "${RESVG_PIN}" ]] ||
	echo "export-brand: warning: resvg ${have:-unknown}; the committed rasters come from ${RESVG_PIN}" >&2

for f in mddb-logo.svg mddb-mark.svg; do
	[[ -f "${BRAND}/${f}" ]] || die "missing master ${BRAND}/${f}"
done

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# render <svg> <px> <out>: one vector render at the final size.
render() {
	local svg="$1" px="$2" out="$3"
	"${RESVG}" -w "${px}" -h "${px}" "${BRAND}/${svg}" "${out}"
}

# save <in-args...> <out> <PNG24|PNG32>: strip metadata, maximum compression.
save() {
	local fmt="${*: -1}" out="${*: -2:1}"
	"${MAGICK}" "${@:1:$#-2}" -strip -define png:exclude-chunks=date,time \
		-define png:compression-level=9 "${fmt}:${out}"
}

# opaque <w> <h> <mark px> <out>: the mark centred on the tile gradient.
opaque() {
	local width="$1" height="$2" mark_px="$3" out="$4"
	render mddb-mark.svg "${mark_px}" "${TMP}/mark.png"
	save -size "${width}x${height}" "${BG}" "${TMP}/mark.png" -gravity center -composite \
		-alpha off "${out}" PNG24
}

mkdir -p "${OUT}/icon" "${OUT}/banner"

for s in "${ICON_SIZES[@]}"; do
	render mddb-logo.svg "${s}" "${TMP}/icon-${s}.png"
	save "${TMP}/icon-${s}.png" "${OUT}/icon/icon-${s}.png" PNG32
done

# Maskable: the hexagon's circumradius plus glow is 170 of the mark's 360 units;
# at 400 px that is 189 px, inside the 204.8 px safe-zone radius of a 512 icon.
opaque 512 512 400 "${OUT}/icon/icon-512-maskable.png"

# Chrome Web Store: 96×96 artwork, 16 px transparent padding on every side.
# The master's tile sits at 32..480 of its 512 canvas; cropping the viewBox to
# the tile makes the tile itself the 96×96 artwork.
sed 's/viewBox="0 0 512 512"/viewBox="32 32 448 448"/' "${BRAND}/mddb-logo.svg" >"${TMP}/tile.svg"
grep -q 'viewBox="32 32 448 448"' "${TMP}/tile.svg" || die "mddb-logo.svg no longer has viewBox 0 0 512 512"
"${RESVG}" -w 96 -h 96 "${TMP}/tile.svg" "${TMP}/store-96.png"
save "${TMP}/store-96.png" -background none -gravity center -extent 128x128 \
	"${OUT}/icon/chrome-store-128.png" PNG32

ico=()
for s in "${ICO_SIZES[@]}"; do ico+=("${TMP}/icon-${s}.png"); done
"${MAGICK}" "${ico[@]}" -strip "${OUT}/icon/favicon.ico"

for b in "${BANNERS[@]}"; do
	IFS=: read -r name w h <<<"${b}"
	opaque "${w}" "${h}" $((h * 7 / 10)) "${OUT}/banner/${name}.png"
done

# Lossless recompression of every PNG; the .ico keeps ImageMagick's encoding.
"${OXIPNG}" -q -o max --strip all "${OUT}"/icon/*.png "${OUT}"/banner/*.png

echo "export-brand: wrote $((${#ICON_SIZES[@]} + 3)) icons and ${#BANNERS[@]} banners to ${OUT}"
