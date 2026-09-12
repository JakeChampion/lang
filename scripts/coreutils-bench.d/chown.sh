# chown is one fchownat(2) per entry, so the plain rows are process startup
# measured once and 200 times, and what separates the implementations is the
# work around the syscall: resolving the spec (which a number skips entirely
# and a name pays a /etc/passwd or /etc/group pass for), walking a tree under
# -R, and formatting the -v line for every entry.
#
# Every row names an ownership the process ALREADY HAS, so the call succeeds
# for an unprivileged runner and the row measures the same work on all three
# implementations. A row that failed would be measuring diagnostics instead,
# and the three do not write the same number of them.
#
# The name rows are the ones worth watching: a symbolic group means a linear
# scan of /etc/group per lookup unless the implementation caches it, and the
# -R rows multiply that by the entry count.
d="$out/chownbench"
mkdir -p "$d/t/a" "$d/t/b"
nums=$(seq 200 | tr '\n' ' ')
for n in $nums; do
  : > "$d/f.$n"
  : > "$d/t/a/f.$n"
  : > "$d/t/b/f.$n"
done
g=$(id -g)
gname=$(id -gn)
printf 'chown one numeric group\ty\t{} :%s %s/f.1\n' "$g" "$d"
printf 'chown 200 numeric groups in one call\ty\t{} :%s %s/f.*\n' "$g" "$d"
printf 'chown 200 numeric groups, one call each\ty\tfor n in %s; do {} :%s %s/f.$n; done\n' "$nums" "$g" "$d"
printf 'chown 200 named groups in one call\ty\t{} :%s %s/f.*\n' "$gname" "$d"
printf 'chown -R a 400-file tree\ty\t{} -R :%s %s/t\n' "$g" "$d"
printf 'chown -Rv a 400-file tree\ty\t{} -Rv :%s %s/t\n' "$g" "$d"
printf 'chown -Rc a 400-file tree\ty\t{} -Rc :%s %s/t\n' "$g" "$d"
printf 'chown -R --reference a 400-file tree\ty\t{} -R --reference=%s/f.1 %s/t\n' "$d" "$d"
printf 'chown -R --from a 400-file tree\ty\t{} -R --from=:%s :%s %s/t\n' "$g" "$g" "$d"
printf 'chown -Rh a 400-file tree\ty\t{} -Rh :%s %s/t\n' "$g" "$d"
