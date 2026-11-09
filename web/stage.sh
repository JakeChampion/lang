#!/usr/bin/env bash
# Copy the playground's runtime files into DEST, the directory the docs site
# serves at /playground/.
#
#   ./web/stage.sh site/public/playground
#
# index.html statically imports ./wasi-shim.js and ./wasi-http-shim.js, so a
# missing sibling stops the whole module and the playground stays on
# "loading runtime…". The script copies every .html and .js file for this
# reason. It copies web/playground.wasm when it exists. A build that compiles
# the wasm directly into DEST does not need that copy. The webfonts come from
# the docs site, so the playground and the site use the same type. cp -L
# copies the target of a symlink, so the staged files are real files.
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: web/stage.sh DEST" >&2
  exit 2
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dest="$1"
fonts="$here/../site/src/assets/fonts"

mkdir -p "$dest/fonts"
cp -L "$here"/*.html "$here"/*.js "$dest/"
if [ -f "$here/playground.wasm" ]; then
  cp -L "$here/playground.wasm" "$dest/"
fi
cp -L "$fonts/ibm-plex-mono-400-latin.woff2" "$fonts/ibm-plex-mono-500-latin.woff2" \
  "$fonts/fraunces-400-700-latin.woff2" "$fonts/OFL.txt" "$dest/fonts/"
