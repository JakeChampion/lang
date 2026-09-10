# sum's workloads. Sourced by scripts/coreutils-bench with the utility
# name in $1; `$out` is the scratch directory and `$gnu` the reference
# directory.
#
# Both algorithms are one arithmetic step per byte with no way to skip
# ahead, so the shapes are: a big file under each of them (throughput),
# the same bytes through a pipe (the read path rather than the file
# one), a small file (startup), and many small operands, where the
# per-file open and the per-line output are what is being paid rather
# than the kernel.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
small="$out/sum-small.txt"
[ -f "$small" ] || "$gnu/seq" 1 100 > "$small"
many="$out/sum-many"
if [ ! -d "$many" ]; then
  mkdir -p "$many"
  for i in $(seq 500); do "$gnu/seq" 1 "$i" > "$many/f$i"; done
fi
printf 'sum a 62 MiB file\tn\t{} %s\n' "$big"
printf 'sum -s a 62 MiB file\tn\t{} -s %s\n' "$big"
printf 'sum a 62 MiB file from a pipe\ty\t{gnu}/cat %s | {}\n' "$big"
printf 'sum -s a 62 MiB file from a pipe\ty\t{gnu}/cat %s | {} -s\n' "$big"
printf 'sum a small file\tn\t{} %s\n' "$small"
printf 'sum 500 small files\ty\tcd %s && {} f* > /dev/null\n' "$many"
