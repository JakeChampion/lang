# One mknod(2) for the one node a run makes, so the whole cost is process
# startup — mknod takes a single NAME, so there is no per-operand loop to
# amortise it against and every row below is startup plus one syscall. The
# `rm` and `mkdir` that reseed each run come from the GNU directory, so they
# are the same constant term for all three implementations.
#
# The rows that are mknod's own are the parses in front of that syscall: `-m`
# reads the umask, compiles the MODE and chmods afterwards, and a device needs
# two base-zero number parses that a fifo does not. The device rows fail with
# EPERM for an unprivileged runner — which is the path being timed, since it
# is the one every machine this runs on takes — so they measure the parse and
# the refused syscall rather than a creation.
d="$out/nodbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')
printf 'mknod one fifo\ty\t{gnu}/rm -rf %s/one; {gnu}/mkdir %s/one; {} %s/one/p p\n' "$d" "$d" "$d"
printf 'mknod 200 fifos\ty\t{gnu}/rm -rf %s/many; {gnu}/mkdir %s/many; for n in %s; do {} %s/many/p.$n p; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mknod -m 600 200 fifos\ty\t{gnu}/rm -rf %s/mode; {gnu}/mkdir %s/mode; for n in %s; do {} -m 600 %s/mode/p.$n p; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mknod -m symbolic 200 fifos\ty\t{gnu}/rm -rf %s/sym; {gnu}/mkdir %s/sym; for n in %s; do {} -m u+rw,go-rwx %s/sym/p.$n p; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'mknod 200 character devices\ty\tfor n in %s; do {} %s/c.$n c 1 3; done 2>/dev/null\n' "$nums" "$d"
printf 'mknod 200 hexadecimal device numbers\ty\tfor n in %s; do {} %s/h.$n c 0x1 0x3; done 2>/dev/null\n' "$nums" "$d"
printf 'mknod 200 invalid device types\ty\tfor n in %s; do {} %s/q.$n q 1 3; done 2>/dev/null\n' "$nums" "$d"
printf 'mknod 200 names already taken\ty\t{gnu}/rm -rf %s/taken; {gnu}/mkdir %s/taken; {gnu}/mkfifo %s; for n in %s; do {} %s/taken/p.$n p; done 2>/dev/null\n' \
  "$d" "$d" "$(for n in $(seq 200); do printf '%s/taken/p.%s ' "$d" "$n"; done)" "$nums" "$d"
