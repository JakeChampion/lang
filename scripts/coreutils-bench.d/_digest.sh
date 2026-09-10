# Compute-bound on the digest kernel, so the shapes that matter are
# a big file (throughput), the same bytes through a pipe (the read
# path rather than the file one), a small file (startup), and -c
# over many names (one open and one digest each, plus the line
# grammar).
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
small="$out/sum-small.txt"
[ -f "$small" ] || "$gnu/seq" 1 100 > "$small"
many="$out/sum-many"
if [ ! -d "$many" ]; then
  mkdir -p "$many"
  for i in $(seq 500); do "$gnu/seq" 1 "$i" > "$many/f$i"; done
fi
sums="$out/$1-many.txt"
[ -f "$sums" ] || (cd "$many" && "$gnu/$1" f* > "$sums")
printf '%s of a 62 MiB file\tn\t{} %s\n' "$1" "$big"
printf '%s of a 62 MiB file from a pipe\ty\t{gnu}/cat %s | {}\n' "$1" "$big"
printf '%s --tag of a 62 MiB file\tn\t{} --tag %s\n' "$1" "$big"
printf '%s of a small file\tn\t{} %s\n' "$1" "$small"
printf '%s -c over 500 small files\ty\tcd %s && {} -c %s > /dev/null\n' "$1" "$many" "$sums"
if [ "$1" = b2sum ]; then
  printf 'b2sum -l 256 of a 62 MiB file\tn\t{} -l 256 %s\n' "$big"
fi
