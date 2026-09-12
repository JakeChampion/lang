# One mknod(2) per operand, so a single pipe is process startup and nothing
# else, and the 200-operand rows are that cost once against 200 syscalls. The
# `rm` and `mkdir` that reseed each run come from the GNU directory, so they
# are the same constant term for all three implementations, exactly as `yes`'s
# `head` is.
#
# The rows that are mkfifo's own are the ones that do more than one syscall per
# operand: `-m` reads the umask, parses the MODE and then chmods every pipe it
# made, so it is three syscalls and a parse where the default is one syscall —
# and a SYMBOLIC mode is a longer parse than an octal one.
d="$out/fifobench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')
printf 'mkfifo one pipe\ty\t{gnu}/rm -rf %s/one; {gnu}/mkdir %s/one; {} %s/one/p\n' "$d" "$d" "$d"
printf 'mkfifo 200 pipes\ty\t{gnu}/rm -rf %s/many; {gnu}/mkdir %s/many; for n in %s; do {} %s/many/p.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkfifo 200 pipes in one run\ty\t{gnu}/rm -rf %s/batch; {gnu}/mkdir %s/batch; {} %s\n' \
  "$d" "$d" "$(for n in $(seq 200); do printf '%s/batch/p.%s ' "$d" "$n"; done)"
printf 'mkfifo -m 600 200 pipes\ty\t{gnu}/rm -rf %s/mode; {gnu}/mkdir %s/mode; for n in %s; do {} -m 600 %s/mode/p.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkfifo -m symbolic 200 pipes\ty\t{gnu}/rm -rf %s/sym; {gnu}/mkdir %s/sym; for n in %s; do {} -m u+rw,go-rwx %s/sym/p.$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mkfifo 200 names already taken\ty\t{gnu}/rm -rf %s/taken; {gnu}/mkdir %s/taken; {gnu}/mkfifo %s; for n in %s; do {} %s/taken/p.$n; done 2>/dev/null\n' \
  "$d" "$d" "$(for n in $(seq 200); do printf '%s/taken/p.%s ' "$d" "$n"; done)" "$nums" "$d"
