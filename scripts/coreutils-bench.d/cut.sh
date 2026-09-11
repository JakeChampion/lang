# A field at a time out of a large table, the shape scripts use
# cut for, and the byte range, which is the same work with no
# delimiter scan at all.
csv="$out/table-5col.csv"
[ -f "$csv" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(1500000):
        f.write("alpha%d,beta%d,gamma%d,delta%d,epsilon%d\n" % (i, i, i, i, i))
' "$csv"
printf 'cut -f2 -d, of a 90 MiB table\ty\t{} -f2 -d, %s > /dev/null\n' "$csv"
printf 'cut -f1,3-5 -d, of a 90 MiB table\ty\t{} -f1,3-5 -d, %s > /dev/null\n' "$csv"
printf 'cut --complement -f2 -d, of a 90 MiB table\ty\t{} --complement -f2 -d, %s > /dev/null\n' "$csv"
printf 'cut -s -f4 -d, of a 90 MiB table\ty\t{} -s -f4 -d, %s > /dev/null\n' "$csv"
printf 'cut -c1-10 of a 90 MiB table\ty\t{} -c1-10 %s > /dev/null\n' "$csv"
printf 'cut -f2 -d, from a pipe\ty\t{gnu}/cat %s | {} -f2 -d, > /dev/null\n' "$csv"
