# ls(1) is readdir plus, when something on the line needs it, one lstat
# per entry — and the whole point of the rows below is to separate those
# two. A plain `ls` never stats at all: it prints names readdir already
# handed over, so that row is startup plus one getdents drain. `-l` stats
# every entry AND asks the passwd and group databases for the owner
# columns, which is the most expensive shape anyone runs.
#
# The tree's SHAPE is the workload and no file's size matters. A wide flat
# directory separates the per-entry cost from the per-directory one, which
# a deep narrow tree under `-R` is what measures.
#
# The sort rows are the comparator: `-U` turns sorting off entirely and is
# the floor, plain `ls` is a byte compare, `-v` is filevercmp — which is a
# suffix scan and a digit-run comparison per pair, not a memcmp — and `-t`
# and `-S` are the two that need the stat as well.
#
# `-C` is the column layout, which is a candidate per possible column
# count over every name, and `--color=always` is the per-entry mode
# classification plus a suffix match against every LS_COLORS rule. The
# quoting rows say what the escaping costs on names that need it.
tree="$out/ls-tree"
if [ ! -d "$tree" ]; then
  mkdir -p "$tree/flat"
  i=0
  while [ "$i" -lt 4000 ]; do : > "$tree/flat/file$i.txt"; i=$((i + 1)); done
  mkdir -p "$tree/mixed"
  i=0
  while [ "$i" -lt 500 ]; do
    : > "$tree/mixed/f$i"
    mkdir -p "$tree/mixed/d$i"
    ln -s "f$i" "$tree/mixed/l$i"
    i=$((i + 1))
  done
  d="$tree/deep"
  i=0
  while [ "$i" -lt 40 ]; do
    d="$d/l$i"
    mkdir -p "$d"
    j=0
    while [ "$j" -lt 20 ]; do : > "$d/f$j"; j=$((j + 1)); done
    i=$((i + 1))
  done
  sync
fi
printf '4000 names, no stat\tn\t{} %s\n' "$tree/flat"
printf '\055l over 4000 names\tn\t{} -l %s\n' "$tree/flat"
printf '\055U (unsorted) over 4000 names\tn\t{} -U %s\n' "$tree/flat"
printf '\055v (filevercmp) over 4000 names\tn\t{} -v %s\n' "$tree/flat"
printf '\055t over 4000 names\tn\t{} -t %s\n' "$tree/flat"
printf '\055S over 4000 names\tn\t{} -S %s\n' "$tree/flat"
printf '\055C \055w 200 over 4000 names\tn\t{} -C -w 200 %s\n' "$tree/flat"
printf '\055x \055w 200 over 4000 names\tn\t{} -x -w 200 %s\n' "$tree/flat"
printf '\055m \055w 200 over 4000 names\tn\t{} -m -w 200 %s\n' "$tree/flat"
printf '\055i \055s over 4000 names\tn\t{} -i -s %s\n' "$tree/flat"
printf '\055F over 1500 mixed entries\tn\t{} -F %s\n' "$tree/mixed"
printf '\055l over 1500 mixed entries\tn\t{} -l %s\n' "$tree/mixed"
printf '\055\055color=always over 1500 mixed\tn\t{} --color=always %s\n' "$tree/mixed"
printf '\055R over a 40-deep tree\tn\t{} -R %s\n' "$tree/deep"
printf '\055lR over a 40-deep tree\tn\t{} -lR %s\n' "$tree/deep"
printf '\055b over 4000 names\tn\t{} -b %s\n' "$tree/flat"
printf '\055\055quoting-style=shell-escape\tn\t{} --quoting-style=shell-escape %s\n' "$tree/flat"
printf '\055\055time-style=full-iso \055l\tn\t{} -l --time-style=full-iso %s\n' "$tree/flat"
