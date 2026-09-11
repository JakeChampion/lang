# realpath is startup plus one readlink(2) per component, so the rows
# separate on how many components there are and on which of the three
# link settings is in force: -s reads no link at all and asks one
# access(2) about the last component, -L walks the name twice. The
# 200-operand rows pay the walk 200 times in one process, which takes
# the startup term out of the comparison.
d="$out/realpathbench"
rm -rf "$d"; mkdir -p "$d/a/b/c/d/e/f/g/h"
: > "$d/a/b/c/d/e/f/g/h/target"
ln -sf a/b/c/d/e/f/g/h/target "$d/link"
printf 'realpath one link\tn\t{} %s/link\n' "$d"
printf 'realpath -s one link\tn\t{} -s %s/link\n' "$d"
printf 'realpath -L one link\tn\t{} -L %s/link\n' "$d"
printf 'realpath -m a missing deep path\tn\t{} -m %s/a/b/c/d/e/f/g/h/i/j/k\n' "$d"
printf 'realpath --relative-to one link\tn\t{} --relative-to=%s/a/b/c %s/link\n' "$d" "$d"
printf 'realpath 200 links\tn\t{} %s\n' "$(seq 200 | sed "s|.*|$d/link|" | tr '\n' ' ')"
printf 'realpath -s 200 links\tn\t{} -s %s\n' "$(seq 200 | sed "s|.*|$d/link|" | tr '\n' ' ')"
