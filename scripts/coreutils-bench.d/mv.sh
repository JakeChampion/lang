# mv destroys its own input, so every row reseeds before it measures. The
# seeding is the same shell work for all three implementations — `: > f` is
# a redirection rather than a fork, and the `mkdir` / `rm` that build and
# clear the tree come from the GNU directory — so it is a constant term,
# exactly as `yes`'s `head` is.
#
# The plain rows are one rename(2) per operand, which is process startup
# measured once and 200 times. The rows that are mv's own are the three
# that do more than rename: a numbered backup (which reads the
# destination's directory to pick a number and then renames twice), the
# --update comparison (a stat of each side before the rename), and moving
# a whole directory, where one rename moves an arbitrary number of files
# and the cost is the same as moving one.
d="$out/mvbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')
printf 'mv one file\ty\t: > %s/v; {} %s/v %s/w\n' "$d" "$d" "$d"
printf 'mv 200 files\ty\tfor n in %s; do : > %s/v.$n; done; for n in %s; do {} %s/v.$n %s/w.$n; done\n' \
  "$nums" "$d" "$nums" "$d" "$d"
printf 'mv 200 files into a directory\ty\t{gnu}/rm -rf %s/into; {gnu}/mkdir -p %s/into; for n in %s; do : > %s/s.$n; done; {} %s/s.* %s/into\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'mv 200 over existing files\ty\tfor n in %s; do : > %s/o.$n; : > %s/p.$n; done; for n in %s; do {} -f %s/o.$n %s/p.$n; done\n' \
  "$nums" "$d" "$d" "$nums" "$d" "$d"
printf 'mv 200 numbered backups\ty\t{gnu}/rm -f %s/bk*; : > %s/bk; for n in %s; do : > %s/b.$n; done; for n in %s; do {} --backup=numbered %s/b.$n %s/bk; done\n' \
  "$d" "$d" "$nums" "$d" "$nums" "$d" "$d"
printf 'mv 200 --update comparisons\ty\tfor n in %s; do : > %s/u.$n; : > %s/t.$n; done; for n in %s; do {} -u %s/u.$n %s/t.$n; done\n' \
  "$nums" "$d" "$d" "$nums" "$d" "$d"
printf 'mv a 400-file tree\ty\t{gnu}/rm -rf %s/t %s/t2; {gnu}/mkdir -p %s/t/a %s/t/b; for n in %s; do : > %s/t/a/f.$n; : > %s/t/b/f.$n; done; {} %s/t %s/t2\n' \
  "$d" "$d" "$d" "$d" "$nums" "$d" "$d" "$d" "$d"
