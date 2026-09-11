# mkdir is one mkdir(2) per operand, so one directory is process startup and
# nothing else, and the 200-operand row is that cost once against 200 syscalls.
# The `rm` and `mkdir` that reseed each run come from the GNU directory, so
# they are the same constant term for all three implementations, exactly as
# `yes`'s `head` is.
#
# The rows that are mkdir's own are the ones that do more than one syscall per
# operand: a MODE carrying a special bit creates the directory with group and
# other write held back and then chmods it, a symbolic MODE is a parse before
# any of that, and `-p` is a walk that tries every ancestor — twice over, since
# the chain that already exists is the same walk with every mkdir(2) answering
# EEXIST and a stat behind it.
d="$out/mkdirbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')
deep=1/2/3/4/5/6/7/8/9/10/11/12/13/14/15
printf 'mkdir one directory\ty\t{gnu}/rm -rf %s/one; {} %s/one\n' "$d" "$d"
printf 'mkdir 200 directories\ty\t{gnu}/rm -rf %s/many; {gnu}/mkdir %s/many; for n in %s; do {} %s/many/d.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkdir -m 755 200 directories\ty\t{gnu}/rm -rf %s/mode; {gnu}/mkdir %s/mode; for n in %s; do {} -m 755 %s/mode/d.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkdir -m 1777 200 directories\ty\t{gnu}/rm -rf %s/sticky; {gnu}/mkdir %s/sticky; for n in %s; do {} -m 1777 %s/sticky/d.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkdir -m symbolic 200 directories\ty\t{gnu}/rm -rf %s/sym; {gnu}/mkdir %s/sym; for n in %s; do {} -m u+rwx,go-w %s/sym/d.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkdir -p a 16-deep chain\ty\t{gnu}/rm -rf %s/p; {} -p %s/p/%s\n' "$d" "$d" "$deep"
printf 'mkdir -p a 16-deep chain that exists\ty\t{gnu}/mkdir -p %s/q/%s; {} -p %s/q/%s\n' "$d" "$deep" "$d" "$deep"
printf 'mkdir -v 200 directories\ty\t{gnu}/rm -rf %s/verb; {gnu}/mkdir %s/verb; for n in %s; do {} -v %s/verb/d.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkdir 200 names already taken\ty\t{gnu}/mkdir -p %s; for n in %s; do {} %s/taken/e.$n; done 2>/dev/null\n' \
  "$(for n in $(seq 200); do printf '%s/taken/e.%s ' "$d" "$n"; done)" "$nums" "$d"
