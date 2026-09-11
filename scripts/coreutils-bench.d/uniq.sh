# uniq's cost is per LINE, so the three shapes are: a run of
# duplicates to collapse, lines that are all distinct (every line
# becomes the one held for the next comparison), and long lines,
# where the comparison itself starts to dominate the fixed cost.
dup="$out/uniq-dup.txt"
[ -f "$dup" ] || python3 - "$dup" <<'PY2'
import sys
with open(sys.argv[1], "w") as f:
    for i in range(4000000):
        f.write("line %d\n" % (i // 4))
PY2
distinct="$out/uniq-distinct.txt"
[ -f "$distinct" ] || "$gnu/seq" 1 4000000 > "$distinct"
paths="$out/uniq-paths.txt"
[ -f "$paths" ] || python3 - "$paths" <<'PY2'
import sys
with open(sys.argv[1], "w") as f:
    for i in range(2000000):
        f.write("/usr/lib/x86_64-linux-gnu/libfoo-%07d.so\n" % i)
PY2
printf 'uniq over 4M lines in groups of 4\ty\t{} %s > /dev/null\n' "$dup"
printf 'uniq -c over 4M lines in groups of 4\ty\t{} -c %s > /dev/null\n' "$dup"
printf 'uniq -d over 4M lines in groups of 4\ty\t{} -d %s > /dev/null\n' "$dup"
printf 'uniq over 4M distinct lines\ty\t{} %s > /dev/null\n' "$distinct"
printf 'uniq -u over 4M distinct lines\ty\t{} -u %s > /dev/null\n' "$distinct"
printf 'uniq over 2M 44-byte distinct lines\ty\t{} %s > /dev/null\n' "$paths"
printf 'uniq -f1 -c over 4M lines\ty\t{} -f1 -c %s > /dev/null\n' "$dup"
printf 'uniq from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$dup"
