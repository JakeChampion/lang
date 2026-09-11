# head's shapes are: stop early on a big file (the count is small,
# so the cost is startup plus one read), copy a lot of it, and the
# two elisions, which seek over a regular file and buffer the tail
# only when the input cannot seek — so each is measured both ways.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'head -n 10 of a 62 MiB file\tn\t{} -n 10 %s\n' "$big"
printf 'head -n 4000000 of a 62 MiB file\ty\t{} -n 4000000 %s > /dev/null\n' "$big"
printf 'head -c 32M of a 62 MiB file\ty\t{} -c 33554432 %s > /dev/null\n' "$big"
printf 'head -n 10 from a pipe\ty\t{gnu}/cat %s | {} -n 10 > /dev/null\n' "$big"
printf 'head -n -10 of a 62 MiB file\ty\t{} -n -10 %s > /dev/null\n' "$big"
printf 'head -c -10 of a 62 MiB file\ty\t{} -c -10 %s > /dev/null\n' "$big"
printf 'head -n -10 from a pipe\ty\t{gnu}/cat %s | {} -n -10 > /dev/null\n' "$big"
printf 'head -c -10 from a pipe\ty\t{gnu}/cat %s | {} -c -10 > /dev/null\n' "$big"
