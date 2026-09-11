# wc -l is the SIMD count, wc -c is fstat and no read at all, and
# -w / -L are the byte-at-a-time scan the C locale asks for.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'wc -l of a 62 MiB file\tn\t{} -l %s\n' "$big"
printf 'wc -c of a 62 MiB file\tn\t{} -c %s\n' "$big"
printf 'wc of a 62 MiB file\tn\t{} %s\n' "$big"
printf 'wc -w of a 62 MiB file\tn\t{} -w %s\n' "$big"
printf 'wc -L of a 62 MiB file\tn\t{} -L %s\n' "$big"
printf 'wc -l from a pipe\ty\t{gnu}/cat %s | {} -l\n' "$big"
