# nl's cost is one line number formatted and one line copied, so
# the shapes are the default (only non-empty lines numbered), -ba
# (every line), -bn (none, a straight copy with a fixed pad), and
# the regex style, which runs lib/bre.fern over every line.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'nl a 62 MiB file\ty\t{} %s > /dev/null\n' "$big"
printf 'nl -ba a 62 MiB file\ty\t{} -ba %s > /dev/null\n' "$big"
printf 'nl -bn a 62 MiB file\ty\t{} -bn %s > /dev/null\n' "$big"
printf 'nl -n rz -w12 a 62 MiB file\ty\t{} -n rz -w12 %s > /dev/null\n' "$big"
# The regex style runs a compiled BRE over every line, so it gets a
# smaller input than the copying styles: at 62 MiB it is a minute a
# run and the ratio is the same.
med="$out/text-3m.txt"
[ -f "$med" ] || "$gnu/seq" 1 500000 > "$med"
printf 'nl -bp7 a 3 MiB file\ty\t{} -bp7 %s > /dev/null\n' "$med"
printf 'nl from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$big"
