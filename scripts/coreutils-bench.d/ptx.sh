# ptx holds the whole input in memory, finds one occurrence per word
# and sorts them, so its cost is words-in rather than bytes-in and it
# grows n log n. The shapes are the default (a letter alphabet and a
# sentence regexp over the whole buffer), -G (a non-whitespace alphabet
# and a line per context, which is the cheap end), the two typesetter
# formats, and -W, which runs lib/bre.fern over the buffer instead of
# the break-character scan.
words="$out/ptx-words.txt"
[ -f "$words" ] || "$gnu/seq" -f 'word%g' 1 120000 | "$gnu/paste" -sd' ' - |
	"$gnu/fold" -w 200 > "$words"
prose="$out/ptx-prose.txt"
[ -f "$prose" ] || "$gnu/yes" \
	'the quick brown fox jumps over the lazy dog.  pack my box with five dozen liquor jugs.' |
	"$gnu/head" -n 4000 > "$prose"
printf 'ptx 120k words\ty\t{} %s > /dev/null\n' "$words"
printf 'ptx -G 120k words\ty\t{} -G %s > /dev/null\n' "$words"
printf 'ptx -O 120k words\ty\t{} -O %s > /dev/null\n' "$words"
printf 'ptx -T 120k words\ty\t{} -T %s > /dev/null\n' "$words"
printf 'ptx -W a regexp alphabet\ty\t{} -W "[a-z][a-z0-9]*" %s > /dev/null\n' "$words"
printf 'ptx prose with sentences\ty\t{} %s > /dev/null\n' "$prose"
printf 'ptx -A prose\ty\t{} -A %s > /dev/null\n' "$prose"
printf 'ptx from a pipe\ty\t{gnu}/cat %s | {} > /dev/null\n' "$words"
