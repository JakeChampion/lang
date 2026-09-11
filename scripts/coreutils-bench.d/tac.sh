# tac reads its input BACKWARDS, so a file is 8 KiB blocks walked
# from the end and a pipe is the whole stream held first. `-r` is
# the separate engine: a backward regexp search per record.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
small="$out/text-600k.txt"
[ -f "$small" ] || "$gnu/seq" 1 100000 > "$small"
one="$out/tac-one-line.txt"
[ -f "$one" ] || printf 'x\n' > "$one"
printf 'tac a 62 MiB file\ty\t{} %s > /dev/null\n' "$big"
printf 'tac -b a 62 MiB file\ty\t{} -b %s > /dev/null\n' "$big"
printf 'tac -s a 62 MiB file\ty\t{} -s 5 %s > /dev/null\n' "$big"
printf 'tac a 62 MiB file from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$big"
printf 'tac -r over a 588 KiB file\ty\t{} -r -s "[0-9]" %s > /dev/null\n' "$small"
printf 'tac a one-line file\tn\t{} %s\n' "$one"
