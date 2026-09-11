# Column counting is the default and byte counting is -b; -s adds
# the backward scan for a blank.
prose="$out/prose.txt"
[ -f "$prose" ] || python3 -c '
import sys
line = "the quick brown fox jumps over the lazy dog " * 4
with open(sys.argv[1], "w") as f:
    for _ in range(400000):
        f.write(line + "\n")
' "$prose"
printf 'fold -w40 of a 68 MiB file\ty\t{} -w40 %s > /dev/null\n' "$prose"
printf 'fold -b -w40 of a 68 MiB file\ty\t{} -b -w40 %s > /dev/null\n' "$prose"
printf 'fold -s -w40 of a 68 MiB file\ty\t{} -s -w40 %s > /dev/null\n' "$prose"
printf 'fold (default 80) of a 68 MiB file\ty\t{} %s > /dev/null\n' "$prose"
