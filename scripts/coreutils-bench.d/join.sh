# Two sorted 1M-line files: the whole cost is splitting each line
# into fields and reassembling one output line, so the shapes are
# a plain pairing, one where half the lines are unpairable, an
# explicit -o format, and a single-character separator.
ja="$out/join-a.txt"
jb="$out/join-b.txt"
jc="$out/join-c.txt"
if [ ! -f "$ja" ]; then
  "$gnu/seq" -f 'k%09.0f v1 v2' 1 1000000 > "$ja"
  "$gnu/seq" -f 'k%09.0f w1 w2' 1 1000000 > "$jb"
  "$gnu/seq" -f 'k%09.0f w1 w2' 500001 1500000 > "$jc"
fi
printf 'join two 1M-line files\ty\t{} %s %s > /dev/null\n' "$ja" "$jb"
printf 'join with half unpairable\ty\t{} %s %s > /dev/null\n' "$ja" "$jc"
printf 'join -a1 -a2 two 1M-line files\ty\t{} -a1 -a2 %s %s > /dev/null\n' "$ja" "$jc"
printf 'join -o 0,1.2,2.3 two 1M-line files\ty\t{} -o 0,1.2,2.3 %s %s > /dev/null\n' "$ja" "$jb"
printf 'join -v1 two 1M-line files\ty\t{} -v1 %s %s > /dev/null\n' "$ja" "$jc"
