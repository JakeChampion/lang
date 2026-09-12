# truncate is one open(2) plus one ftruncate(2) per operand, so a single
# operand is process startup and nothing else, and the 200-operand rows are
# that cost once against 200 pairs of syscalls. The `rm` and `mkdir` that
# reseed each run come from the GNU directory, so they are the same constant
# term for all three implementations, exactly as `yes`'s `head` is.
#
# The rows that are truncate's own are the ones that do more than the pair: a
# RELATIVE size needs an fstat before it knows what to ask for, `-o` needs the
# same fstat for the block size, `-r` pays one stat for the whole run however
# many operands follow, and a SIZE with a suffix or a modifier is a parse that
# a bare number is not. The creating row is there because the open is the
# expensive half when the file is not already there.
d="$out/truncbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')
printf 'truncate one file\ty\t{gnu}/rm -f %s/one; {} -s 4096 %s/one\n' "$d" "$d"
printf 'truncate 200 existing files\ty\t{gnu}/rm -rf %s/many; {gnu}/mkdir %s/many; for n in %s; do {} -s 4096 %s/many/f.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'truncate 200 files in one run\ty\t{gnu}/rm -rf %s/batch; {gnu}/mkdir %s/batch; {} -s 4096 %s\n' \
  "$d" "$d" "$(for n in $(seq 200); do printf '%s/batch/f.%s ' "$d" "$n"; done)"
printf 'truncate -s with a suffix\ty\t{gnu}/rm -rf %s/suf; {gnu}/mkdir %s/suf; for n in %s; do {} -s 4KiB %s/suf/f.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'truncate -s relative\ty\t{gnu}/rm -rf %s/rel; {gnu}/mkdir %s/rel; for n in %s; do {} -s +1 %s/rel/f.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'truncate -s rounding up\ty\t{gnu}/rm -rf %s/rup; {gnu}/mkdir %s/rup; for n in %s; do {} -s %%4096 %s/rup/f.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'truncate -o 200 files\ty\t{gnu}/rm -rf %s/blk; {gnu}/mkdir %s/blk; for n in %s; do {} -o -s 1 %s/blk/f.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'truncate -r 200 files in one run\ty\t{gnu}/rm -rf %s/ref; {gnu}/mkdir %s/ref; {gnu}/truncate -s 4096 %s/refsize; {} -r %s/refsize %s\n' \
  "$d" "$d" "$d" "$d" "$(for n in $(seq 200); do printf '%s/ref/f.%s ' "$d" "$n"; done)"
printf 'truncate -c 200 missing files\ty\tfor n in %s; do {} -c -s 4096 %s/gone.$n; done\n' "$nums" "$d"
printf 'truncate 200 missing files under -s\ty\t{gnu}/rm -rf %s/new; {gnu}/mkdir %s/new; for n in %s; do {} -s 4096 %s/new/f.$n; done\n' \
  "$d" "$d" "$nums" "$d"
