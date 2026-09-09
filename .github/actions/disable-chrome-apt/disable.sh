#!/usr/bin/env bash
set -euo pipefail

# Accept a fixture directory for local validation; CI uses the system apt root.
apt_root="${1:-/etc/apt}"
shopt -s nullglob
scratch=$(mktemp)
trap 'rm -f "$scratch"' EXIT
for source in "$apt_root/sources.list" "$apt_root/sources.list.d/"*.list "$apt_root/sources.list.d/"*.sources; do
  [ -f "$source" ] || continue
  format=list
  [[ "$source" != *.sources ]] || format=deb822
  # Remove only Chrome URI entries. A deb822 stanza may contain other URIs,
  # so deleting a matching file or whole stanza can also lose Ubuntu sources.
  if awk -v format="$format" '
    function chrome(uri) {
      return uri ~ /^https?:\/\/dl[.]google[.]com\/linux\/chrome(-stable)?\/deb\/?$/
    }
    function filter_uris(line, prefix,    parts, n, j, kept) {
      if (tolower(line) ~ /^uris:/) sub(/^[^:]*:[[:space:]]*/, "", line)
      n = split(line, parts, /[[:space:]]+/)
      kept = ""
      for (j = 1; j <= n; j++) {
        if (parts[j] == "") continue
        if (chrome(parts[j])) { removed++; dropped++ }
        else { kept = kept (kept == "" ? "" : " ") parts[j]; remaining++ }
      }
      if (kept == "") return prefix == "URIs: " ? "URIs:\n" : ""
      return prefix kept "\n"
    }
    BEGIN { if (format == "deb822") { RS = ""; ORS = "\n\n" } }
    format == "list" {
      drop = 0
      if ($1 == "deb" || $1 == "deb-src") {
        for (i = 2; i <= NF; i++) if (chrome($i)) drop = 1
      }
      if (drop) removed++; else print
      next
    }
    {
      n = split($0, lines, "\n")
      out = ""; in_uris = 0; dropped = 0; remaining = 0
      for (i = 1; i <= n; i++) {
        line = lines[i]
        if (tolower(line) ~ /^uris:/) {
          in_uris = 1
          out = out filter_uris(line, "URIs: ")
        } else if (in_uris && line ~ /^[[:space:]]+[^#[:space:]]/) {
          out = out filter_uris(line, " ")
        } else {
          if (line !~ /^[[:space:]]*#/) in_uris = 0
          out = out line "\n"
        }
      }
      if (!dropped) print
      else if (remaining) { sub(/\n$/, "", out); print out }
    }
    END { if (!removed) exit 3 }
  ' "$source" > "$scratch"; then
    cat "$scratch" > "$source"
    echo "Disabled Chrome apt source in $source"
  else
    awk_status=$?
    [ "$awk_status" -eq 3 ] || exit "$awk_status"
  fi
done
