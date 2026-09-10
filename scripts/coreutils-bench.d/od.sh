# od's cost is per FIELD and per BLOCK, and the two are separable:
# a wider -w has the same fields over a quarter of the blocks. The
# float rows run over a much smaller file because their conversion
# is the shortest-round-trip search, which is two orders of
# magnitude dearer than a base conversion (#8828).
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
small="$out/text-1m.txt"
[ -f "$small" ] || "$gnu/head" -c 1048576 "$big" > "$small"
printf 'od default of a 62 MiB file\ty\t{} %s > /dev/null\n' "$big"
printf 'od -t x1 of a 62 MiB file\ty\t{} -t x1 %s > /dev/null\n' "$big"
printf 'od -t x1 -w64 of a 62 MiB file\ty\t{} -t x1 -w64 %s > /dev/null\n' "$big"
printf 'od -t x8 of a 62 MiB file\ty\t{} -t x8 %s > /dev/null\n' "$big"
printf 'od -c of a 62 MiB file\ty\t{} -c %s > /dev/null\n' "$big"
printf 'od -A n -t x1 of a 62 MiB file\ty\t{} -A n -t x1 %s > /dev/null\n' "$big"
printf 'od -S 4 of a 62 MiB file\ty\t{} -S 4 %s > /dev/null\n' "$big"
printf 'od -t f8 of a 1 MiB file\ty\t{} -t f8 %s > /dev/null\n' "$small"
