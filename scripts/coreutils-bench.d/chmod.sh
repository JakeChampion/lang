# chmod is one fchmodat(2) per entry, so the plain rows are process startup
# measured once and 200 times, and what separates the implementations is the
# work around the syscall: parsing the mode (which an octal skips almost
# entirely and a long symbolic list does not), walking a tree under -R, and
# formatting the -v line for every entry. The tree is built once and the
# rows only change modes on it, so no row reseeds; the `seq`-driven loops
# fork a shell per iteration for all three implementations alike, which is
# the same constant term ln's rows carry.
d="$out/chmodbench"
mkdir -p "$d/t/a" "$d/t/b"
nums=$(seq 200 | tr '\n' ' ')
for n in $nums; do
  : > "$d/f.$n"
  : > "$d/t/a/f.$n"
  : > "$d/t/b/f.$n"
done
printf 'chmod one octal mode\ty\t{} 0644 %s/f.1\n' "$d"
printf 'chmod 200 octal modes in one call\ty\t{} 0644 %s/f.*\n' "$d"
printf 'chmod 200 octal modes, one call each\ty\tfor n in %s; do {} 0644 %s/f.$n; done\n' "$nums" "$d"
printf 'chmod 200 symbolic modes in one call\ty\t{} u=rw,go=r %s/f.*\n' "$d"
printf 'chmod 200 long symbolic modes in one call\ty\t{} u+rwX-w,g=u,o=g,a-st,u+w %s/f.*\n' "$d"
printf 'chmod -R a 400-file tree\ty\t{} -R 0644 %s/t\n' "$d"
printf 'chmod -Rv a 400-file tree\ty\t{} -Rv 0644 %s/t\n' "$d"
printf 'chmod -R --reference a 400-file tree\ty\t{} -R --reference=%s/f.1 %s/t\n' "$d" "$d"
