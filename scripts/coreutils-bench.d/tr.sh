# tr's whole cost is one table lookup per byte, so the shapes are:
# a pure translation, a deletion (which also compacts), a squeeze
# (which carries a run across the block boundary), and the
# complemented delete, whose table is built by inversion.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'tr 0-9 a-j over a 62 MiB file\ty\t{} 0-9 a-j < %s > /dev/null\n' "$big"
printf 'tr -d 0-4 over a 62 MiB file\ty\t{} -d 0-4 < %s > /dev/null\n' "$big"
printf 'tr -s 0-9 over a 62 MiB file\ty\t{} -s 0-9 < %s > /dev/null\n' "$big"
printf 'tr -cd digits over a 62 MiB file\ty\t{} -cd "[:digit:]" < %s > /dev/null\n' "$big"
printf 'tr from a pipe\ty\t{gnu}/cat %s | {} 0-9 a-j > /dev/null\n' "$big"
