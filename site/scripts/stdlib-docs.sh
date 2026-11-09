#!/usr/bin/env bash
# Generate the standard-library reference pages in
# site/src/content/docs/stdlib/, with a link from each declaration to its
# line in the source on GitHub.
#
#   site/scripts/stdlib-docs.sh
#
# FERN names the compiler (default bin/fern-selfhost). The script runs from
# the repository root, so each link has the path of the source in the
# repository.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../.."
fern="${FERN:-bin/fern-selfhost}"
source='https://github.com/JakeChampion/lang/blob/main/{path}?plain=1#L{line}'
for tree in std core; do
  "$fern" -doc -doc-source "$source" -o site/src/content/docs/stdlib "internal/stdlib/$tree"
done
