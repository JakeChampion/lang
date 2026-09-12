# chgrp is chown with the user half taken away, so the rows are chown's minus
# the ones that need a user: the syscall is the same, the walk is the same,
# and what is left to separate the implementations is the group lookup and the
# -v formatting.
#
# It is worth measuring separately rather than assuming it tracks chown. The
# operand grammar is a different entry point — one group token, no split — so a
# spec parse that got slower on one would not show up in the other's rows.
#
# Every row names the group the process is already in, so the call succeeds for
# an unprivileged runner and all three implementations do the same work.
d="$out/chgrpbench"
mkdir -p "$d/t/a" "$d/t/b"
nums=$(seq 200 | tr '\n' ' ')
for n in $nums; do
  : > "$d/f.$n"
  : > "$d/t/a/f.$n"
  : > "$d/t/b/f.$n"
done
g=$(id -g)
gname=$(id -gn)
printf 'chgrp one numeric group\ty\t{} %s %s/f.1\n' "$g" "$d"
printf 'chgrp 200 numeric groups in one call\ty\t{} %s %s/f.*\n' "$g" "$d"
printf 'chgrp 200 numeric groups, one call each\ty\tfor n in %s; do {} %s %s/f.$n; done\n' "$nums" "$g" "$d"
printf 'chgrp 200 named groups in one call\ty\t{} %s %s/f.*\n' "$gname" "$d"
printf 'chgrp -R a 400-file tree\ty\t{} -R %s %s/t\n' "$g" "$d"
printf 'chgrp -Rv a 400-file tree\ty\t{} -Rv %s %s/t\n' "$g" "$d"
printf 'chgrp -Rc a 400-file tree\ty\t{} -Rc %s %s/t\n' "$g" "$d"
printf 'chgrp -R by name a 400-file tree\ty\t{} -R %s %s/t\n' "$gname" "$d"
printf 'chgrp -R --reference a 400-file tree\ty\t{} -R --reference=%s/f.1 %s/t\n' "$d" "$d"
printf 'chgrp -Rh a 400-file tree\ty\t{} -Rh %s %s/t\n' "$g" "$d"
