# shuf's cost splits three ways and the workloads separate them: the
# draws, which are the whole of `-i` with no input to read; the read and
# the permutation over a file, where the array is the size of the input;
# and the reservoir, which is one draw PER LINE and is reached by a
# `-n` over an input past 8 MiB (a pipe counts as past it, whatever it
# carries). The last pair are the shapes where the answer is a handful of
# lines: a head count off a huge file, and a range far larger than the
# count, which never materialises the range at all.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'shuf -i 1-1000000\ty\t{} -i 1-1000000 > /dev/null\n'
printf 'shuf a 62 MiB file\ty\t{} %s > /dev/null\n' "$big"
printf 'shuf a 62 MiB file from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$big"
printf 'shuf -n 10 of a 62 MiB file\ty\t{} -n 10 %s\n' "$big"
printf 'shuf -n 1000000 of a 62 MiB file\ty\t{} -n 1000000 %s > /dev/null\n' "$big"
printf 'shuf -r -n 1000000 of a 62 MiB file\ty\t{} -r -n 1000000 %s > /dev/null\n' "$big"
printf 'shuf -n 10 -i 1-1000000000\tn\t{} -n 10 -i 1-1000000000\n'
printf 'shuf -e 200 operands\tn\t{} -e %s > /dev/null\n' "$("$gnu/seq" -s ' ' 1 200)"
