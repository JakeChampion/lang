# tee is a read and N writes with no per-byte work at all, so the
# shapes are the number of outputs and where they go. The outputs
# land under $out, which is removed when the run ends.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'tee 62 MiB to stdout alone\ty\t{} < %s > /dev/null\n' "$big"
printf 'tee 62 MiB to one file\ty\t{} %s/tee1 < %s > /dev/null\n' "$out" "$big"
printf 'tee 62 MiB to four files\ty\t{} %s/tee1 %s/tee2 %s/tee3 %s/tee4 < %s > /dev/null\n' "$out" "$out" "$out" "$out" "$big"
printf 'tee 62 MiB from a pipe to one file\ty\t{gnu}/cat %s | {} %s/tee1 > /dev/null\n' "$big" "$out"
printf 'tee 62 MiB down a pipe\ty\t{} %s/tee1 < %s | {gnu}/cat > /dev/null\n' "$out" "$big"
