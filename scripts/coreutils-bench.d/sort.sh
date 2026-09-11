# sort pays the comparison count times the cost of one comparison,
# so the workloads separate the two: the byte compare over a
# shuffled file, the same file under -n and under a key, -u and -r
# which change what the comparison answers, the check that never
# sorts at all, and the merge that never compares within a file.
words="$out/sort-words.txt"
nums="$out/sort-nums.txt"
fields="$out/sort-fields.txt"
[ -f "$words" ] || python3 - "$words" "$nums" "$fields" <<'PY'
import random, sys
rnd = random.Random(8302)
alpha = "abcdefghijklmnopqrstuvwxyz"
with open(sys.argv[1], "w") as w, open(sys.argv[2], "w") as n, open(sys.argv[3], "w") as f:
    for i in range(500000):
        s = "".join(rnd.choice(alpha) for _ in range(11))
        w.write(s + "\n")
        n.write("%d\n" % rnd.randrange(-10**9, 10**9))
        f.write("%s %d\n" % (s[:4], rnd.randrange(0, 1000000)))
PY
sorted_a="$out/sort-sorted-a.txt"
sorted_b="$out/sort-sorted-b.txt"
[ -f "$sorted_a" ] || "$gnu/sort" "$words" > "$sorted_a"
[ -f "$sorted_b" ] || "$gnu/sort" "$nums" > "$sorted_b"
printf 'sort 500k lines\ty\t{} %s > /dev/null\n' "$words"
printf 'sort -n 500k numbers\ty\t{} -n %s > /dev/null\n' "$nums"
printf 'sort -k2,2n 500k lines\ty\t{} -k2,2n %s > /dev/null\n' "$fields"
printf 'sort -k1,1 500k lines\ty\t{} -k1,1 %s > /dev/null\n' "$fields"
printf 'sort -u 500k lines\ty\t{} -u %s > /dev/null\n' "$words"
printf 'sort -r 500k lines\ty\t{} -r %s > /dev/null\n' "$words"
printf 'sort -s 500k lines\ty\t{} -s %s > /dev/null\n' "$words"
printf 'sort 500k lines from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$words"
printf 'sort a sorted 500k-line file\ty\t{} %s > /dev/null\n' "$sorted_a"
printf 'sort -c a sorted 500k-line file\tn\t{} -c %s\n' "$sorted_a"
printf 'sort -m two sorted files\ty\t{} -m %s %s > /dev/null\n' "$sorted_a" "$sorted_a"
