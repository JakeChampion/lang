# du spends its time on the readdir / lstat pair per entry and on the
# already-seen (dev, ino) probe, so the tree's SHAPE is the workload and no
# file's size matters. A wide flat directory and a deep narrow one separate
# the per-entry cost from the per-directory one; `-a` is the same walk with a
# line per file instead of per directory; and `-l`, which turns the seen set
# off entirely, is what says how much of the time is the probe.
tree="$out/du-tree"
if [ ! -d "$tree" ]; then
  mkdir -p "$tree/flat"
  i=0
  while [ "$i" -lt 4000 ]; do : > "$tree/flat/f$i"; i=$((i + 1)); done
  d="$tree/deep"
  i=0
  while [ "$i" -lt 60 ]; do
    d="$d/l$i"
    mkdir -p "$d"
    j=0
    while [ "$j" -lt 20 ]; do : > "$d/f$j"; j=$((j + 1)); done
    i=$((i + 1))
  done
  sync
fi
printf '4000 files in one directory\tn\t{} -s %s\n' "$tree/flat"
printf '\055a over 4000 files\tn\t{} -a %s\n' "$tree/flat"
printf 'a 60-deep tree\tn\t{} -s %s\n' "$tree/deep"
printf '\055a over a 60-deep tree\tn\t{} -a %s\n' "$tree/deep"
printf '\055\055apparent-size of the whole tree\tn\t{} --apparent-size -s %s\n' "$tree"
printf '\055h \055c of two trees\tn\t{} -h -c -s %s %s\n' "$tree/flat" "$tree/deep"
printf '\055\055inodes of the whole tree\tn\t{} --inodes -s %s\n' "$tree"
printf '\055l (no seen set) over 4000 files\tn\t{} -l -s %s\n' "$tree/flat"
printf 'du of one small directory\tn\t{} -s %s\n' "$tree/deep/l0"
