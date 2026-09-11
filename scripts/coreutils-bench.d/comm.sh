# Two sorted files where every other line is missing from the
# second, so all three columns are exercised; and the same file
# twice, where everything pairs and the order check therefore
# never turns on — the two halves of comm's cost.
ca="$out/comm-a.txt"
cb="$out/comm-b.txt"
if [ ! -f "$ca" ]; then
  python3 - "$ca" "$cb" <<'PY2'
import sys
a = open(sys.argv[1], "w")
b = open(sys.argv[2], "w")
for i in range(2000000):
    a.write("key-%09d\n" % i)
    if i % 2 == 0:
        b.write("key-%09d\n" % i)
PY2
fi
printf 'comm over 2M + 1M sorted lines\ty\t{} %s %s > /dev/null\n' "$ca" "$cb"
printf 'comm -12 over 2M + 1M sorted lines\ty\t{} -12 %s %s > /dev/null\n' "$ca" "$cb"
printf 'comm --total over 2M + 1M sorted lines\ty\t{} --total %s %s > /dev/null\n' "$ca" "$cb"
printf 'comm of a 2M-line file with itself\ty\t{} %s %s > /dev/null\n' "$ca" "$ca"
printf 'comm -123 of a 2M-line file with itself\ty\t{} -123 %s %s > /dev/null\n' "$ca" "$ca"
