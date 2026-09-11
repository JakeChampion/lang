# Leading blanks only is the default; -a walks every run on the
# line, and a file with no blanks is the do-nothing path.
indented="$out/indented.txt"
[ -f "$indented" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(1200000):
        f.write("        name%d        value%d\n" % (i, i))
' "$indented"
noblank="$out/noblank.txt"
[ -f "$noblank" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(1200000):
        f.write("name%d-value%d-note%d\n" % (i, i, i))
' "$noblank"
printf 'unexpand a 42 MiB indented file\ty\t{} %s > /dev/null\n' "$indented"
printf 'unexpand -a a 42 MiB indented file\ty\t{} -a %s > /dev/null\n' "$indented"
printf 'unexpand -t4 a 42 MiB indented file\ty\t{} -t4 %s > /dev/null\n' "$indented"
printf 'unexpand a 32 MiB file with no blanks\ty\t{} %s > /dev/null\n' "$noblank"
