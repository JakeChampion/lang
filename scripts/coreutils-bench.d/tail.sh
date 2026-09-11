# tail -n 10 of a seekable file reads one block from the end, so it
# is startup-bound; from a pipe it has to stream the whole input.
# A large count and -c walk backwards over many blocks.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'tail -n 10 of a 62 MiB file\tn\t{} -n 10 %s\n' "$big"
printf 'tail -n 4000000 of a 62 MiB file\ty\t{} -n 4000000 %s > /dev/null\n' "$big"
printf 'tail -c 32M of a 62 MiB file\ty\t{} -c 33554432 %s > /dev/null\n' "$big"
printf 'tail -n 10 from a pipe\ty\t{gnu}/cat %s | {} -n 10 > /dev/null\n' "$big"
printf 'tail -n +4000000 of a 62 MiB file\ty\t{} -n +4000000 %s > /dev/null\n' "$big"
