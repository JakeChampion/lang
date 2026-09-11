# cat's fast path is a straight copy with no per-byte work; the
# numbered and squeezed forms are the byte-at-a-time scans, and
# -A adds an escape decision per byte.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'cat a 62 MiB file\ty\t{} %s > /dev/null\n' "$big"
printf 'cat two 62 MiB files\ty\t{} %s %s > /dev/null\n' "$big" "$big"
printf 'cat from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$big"
printf 'cat -n a 62 MiB file\ty\t{} -n %s > /dev/null\n' "$big"
printf 'cat -s a 62 MiB file\ty\t{} -s %s > /dev/null\n' "$big"
printf 'cat -A a 62 MiB file\ty\t{} -A %s > /dev/null\n' "$big"
