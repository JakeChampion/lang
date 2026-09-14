# sync(1) is one syscall per run with no operand and one open, one flush
# and one close per operand otherwise, so every row is process startup
# plus that. The 200-operand rows separate the per-operand cost from the
# startup; -f flushes a whole file system per operand, which on a quiet
# box with nothing dirty is the cheapest of the three, and the missing-
# name row is the diagnostic path with no flush at all.
d="$out/syncbench"
mkdir -p "$d"
i=0
while [ "$i" -lt 200 ]; do : > "$d/f$i"; i=$((i + 1)); done
files=$(for n in $(seq 0 199); do printf '%s/f%s ' "$d" "$n"; done)
printf 'sync\ty\t{}\n'
printf 'sync one file\ty\t{} %s/f0\n' "$d"
printf 'sync -d one file\ty\t{} -d %s/f0\n' "$d"
printf 'sync -f one file\ty\t{} -f %s/f0\n' "$d"
printf 'sync 200 files in one run\ty\t{} %s\n' "$files"
printf 'sync -f 200 files in one run\ty\t{} -f %s\n' "$files"
printf 'sync 200 missing names\ty\t{} %s 2>/dev/null\n' "$(for n in $(seq 200); do printf '%s/missing.%s ' "$d" "$n"; done)"
