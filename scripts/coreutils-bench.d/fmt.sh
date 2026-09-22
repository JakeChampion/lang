# The break chooser is a dynamic program over the words of a paragraph,
# so the shapes that matter are how LONG a paragraph is (the inner walk
# is bounded by the width, and the outer one by the word count) and how
# much punctuation it carries — the cost function reads the bytes around
# every candidate break.
prose="$out/fmt-prose.txt"
[ -f "$prose" ] || python3 -c '
import sys
sent = "the quick brown fox jumps over the lazy dog.  "
with open(sys.argv[1], "w") as f:
    for _ in range(300000):
        f.write(sent * 3 + "\n\n")
' "$prose"
# One paragraph per line above; this one wraps every paragraph over many
# input lines instead, which is the refill path rather than the split one.
wrapped="$out/fmt-wrapped.txt"
[ -f "$wrapped" ] || python3 -c '
import sys
line = "the quick brown fox jumps over the lazy dog "
with open(sys.argv[1], "w") as f:
    for _ in range(150000):
        for _ in range(6):
            f.write(line + "\n")
        f.write("\n")
' "$wrapped"
# One paragraph of 200 000 lines: the shape where the per-line work is
# what counts, since every word of the file is in one chooser run and
# the reader appends to one word array for the whole file (#9983).
tall="$out/fmt-tall.txt"
[ -f "$tall" ] || python3 -c '
import sys
with open(sys.argv[1], "w") as f:
    for i in range(200000):
        f.write("line %d of text\n" % i)
' "$tall"
printf 'fmt (default) of a 40 MiB file\ty\t{} %s > /dev/null\n' "$prose"
printf 'fmt -w 40 of a 40 MiB file\ty\t{} -w 40 %s > /dev/null\n' "$prose"
printf 'fmt -s -w 40 of a 40 MiB file\ty\t{} -s -w 40 %s > /dev/null\n' "$prose"
printf 'fmt -u -w 40 of a 40 MiB file\ty\t{} -u -w 40 %s > /dev/null\n' "$prose"
printf 'fmt (default) of a 38 MiB wrapped file\ty\t{} %s > /dev/null\n' "$wrapped"
printf 'fmt -c -w 60 of a 38 MiB wrapped file\ty\t{} -c -w 60 %s > /dev/null\n' "$wrapped"
printf 'fmt -p "" -w 40 of a 38 MiB wrapped file\ty\t{} -p "" -w 40 %s > /dev/null\n' "$wrapped"
printf 'fmt (default) from a pipe\ty\tcat %s | {} > /dev/null\n' "$prose"
printf 'fmt (default) of one 200k-line paragraph\ty\t{} %s > /dev/null\n' "$tall"
