# split's output is FILES, so every workload runs in a scratch
# directory of its own and overwrites the same names each run. The
# shapes: a record count (one SIMD tally per block), a byte count
# (a straight copy), the record-budget form (-C, one reverse scan
# per piece), and the three -n divisions. -n over a PIPE is the
# one that has to spool the input to a temporary file first,
# because chunking needs a size a pipe cannot give.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
sdir="$out/split-out"
mkdir -p "$sdir"
printf 'split -l 100000 of a 62 MiB file\ty\tcd %s && {} -l 100000 %s\n' "$sdir" "$big"
printf 'split -b 8M of a 62 MiB file\ty\tcd %s && {} -b 8M %s\n' "$sdir" "$big"
printf 'split -C 8M of a 62 MiB file\ty\tcd %s && {} -C 8M %s\n' "$sdir" "$big"
printf 'split -n 8 of a 62 MiB file\ty\tcd %s && {} -n 8 %s\n' "$sdir" "$big"
printf 'split -n l/8 of a 62 MiB file\ty\tcd %s && {} -n l/8 %s\n' "$sdir" "$big"
printf 'split -n r/8 of a 62 MiB file\ty\tcd %s && {} -n r/8 %s\n' "$sdir" "$big"
printf 'split -l 100000 from a pipe\ty\tcd %s && {gnu}/cat %s | {} -l 100000\n' "$sdir" "$big"
printf 'split -n 8 from a pipe\ty\tcd %s && {gnu}/cat %s | {} -n 8\n' "$sdir" "$big"
