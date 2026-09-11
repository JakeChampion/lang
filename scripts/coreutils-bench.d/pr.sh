# pr's cost splits three ways and each shape isolates one: the page
# machinery (a header and a trailer every 56 lines), the per-line
# rendering (the clump loop, which -n and -v walk byte by byte), and
# the read-ahead a COLUMN layout needs, since a page cannot be written
# until its last line has been read and the columns balanced.
big="$out/text-64m.txt"
[ -f "$big" ] || "$gnu/seq" 1 8000000 > "$big"
printf 'pr a 62 MiB file\ty\t{} %s > /dev/null\n' "$big"
printf 'pr -t a 62 MiB file\ty\t{} -t %s > /dev/null\n' "$big"
printf 'pr -4 a 62 MiB file\ty\t{} -4 %s > /dev/null\n' "$big"
printf 'pr -4 -a a 62 MiB file\ty\t{} -4 -a %s > /dev/null\n' "$big"
printf 'pr -n a 62 MiB file\ty\t{} -n %s > /dev/null\n' "$big"
printf 'pr -d a 62 MiB file\ty\t{} -d %s > /dev/null\n' "$big"
printf 'pr -v a 62 MiB file\ty\t{} -v %s > /dev/null\n' "$big"
printf 'pr -F a 62 MiB file\ty\t{} -F %s > /dev/null\n' "$big"
printf 'pr from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$big"
# Two files in parallel is the other cursor shape: a row is one line
# from each, so the reads interleave and neither side can read ahead.
printf 'pr -m two 62 MiB files\ty\t{} -m %s %s > /dev/null\n' "$big" "$big"
# Tabs are the branch the clump loop takes most often in real text,
# and -e turns each one into spaces the output tabifier then folds
# back up.
tabbed="$out/tabbed-32m.txt"
[ -f "$tabbed" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(1000000):
        f.write("col%d\tcol%d\tcol%d\tcol%d\n" % (i, i, i, i))
' "$tabbed"
printf 'pr -t -e a tabbed file\ty\t{} -t -e %s > /dev/null\n' "$tabbed"
printf 'pr -t -2 a tabbed file\ty\t{} -t -2 %s > /dev/null\n' "$tabbed"
# Startup: one small page, which is where a static binary with no
# dynamic loader is ahead.
small="$out/pr-small.txt"
[ -f "$small" ] || "$gnu/seq" 1 20 > "$small"
printf 'pr a small file\tn\t{} %s > /dev/null\n' "$small"
