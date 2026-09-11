# A tabbed file is the conversion proper; a file with no tabs at
# all is the path every line takes when there is nothing to do.
tabbed="$out/tabbed.txt"
[ -f "$tabbed" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(1200000):
        f.write("\tname%d\tvalue%d\tnote%d\n" % (i, i, i))
' "$tabbed"
untabbed="$out/untabbed.txt"
[ -f "$untabbed" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(1200000):
        f.write("    name%d value%d note%d\n" % (i, i, i))
' "$untabbed"
printf 'expand a 45 MiB tabbed file\ty\t{} %s > /dev/null\n' "$tabbed"
printf 'expand -t4 a 45 MiB tabbed file\ty\t{} -t4 %s > /dev/null\n' "$tabbed"
printf 'expand -i a 45 MiB tabbed file\ty\t{} -i %s > /dev/null\n' "$tabbed"
printf 'expand -t 4,8,16 a 45 MiB tabbed file\ty\t{} -t 4,8,16 %s > /dev/null\n' "$tabbed"
printf 'expand a 42 MiB file with no tabs\ty\t{} %s > /dev/null\n' "$untabbed"
