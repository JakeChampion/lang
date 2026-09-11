# Two files side by side is the parallel path; -s is the serial
# one, which is a join over one file.
pa="$out/paste-a.txt"
pb="$out/paste-b.txt"
[ -f "$pa" ] || "$gnu/seq" 1 4000000 > "$pa"
[ -f "$pb" ] || "$gnu/seq" 4000001 8000000 > "$pb"
printf 'paste two 27 MiB files\ty\t{} %s %s > /dev/null\n' "$pa" "$pb"
printf 'paste -d, two 27 MiB files\ty\t{} -d, %s %s > /dev/null\n' "$pa" "$pb"
printf 'paste -s a 27 MiB file\ty\t{} -s %s > /dev/null\n' "$pa"
printf 'paste one 27 MiB file\ty\t{} %s > /dev/null\n' "$pa"
