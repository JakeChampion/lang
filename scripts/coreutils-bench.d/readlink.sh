# Bare readlink is startup plus one readlink(2); the canonicalising
# modes are startup plus a readlink(2) per component, so the deep-path
# rows are where the per-component walk is visible against the
# constant. The 200-operand rows pay the walk 200 times in one process,
# which takes the startup term out of the comparison.
d="$out/readlinkbench"
rm -rf "$d"; mkdir -p "$d/a/b/c/d/e/f/g/h"
: > "$d/a/b/c/d/e/f/g/h/target"
ln -sf a/b/c/d/e/f/g/h/target "$d/link"
ln -sf link "$d/link2"
ln -sf nowhere "$d/dangling"
printf 'readlink one link\tn\t{} %s/link\n' "$d"
printf 'readlink -f one link\tn\t{} -f %s/link\n' "$d"
printf 'readlink -e one link\tn\t{} -e %s/link\n' "$d"
printf 'readlink -m a missing deep path\tn\t{} -m %s/a/b/c/d/e/f/g/h/i/j/k\n' "$d"
printf 'readlink 200 links\tn\t{} %s\n' "$(seq 200 | sed "s|.*|$d/link|" | tr '\n' ' ')"
printf 'readlink -f 200 links\tn\t{} -f %s\n' "$(seq 200 | sed "s|.*|$d/link|" | tr '\n' ' ')"
