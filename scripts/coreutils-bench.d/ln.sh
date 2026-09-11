# Most of ln's work is one syscall per operand, so the plain rows are
# process startup measured once and 200 times, the way link's are. The
# rows that are ln's own are the three that do more than link(2): the
# replace path under -f (a second link and a rename rather than one
# link), a numbered backup (which reads the destination's directory to
# pick a number and then renames twice), and --relative (which
# canonicalises both sides, so it is a readlink walk per component). The
# `rm` that clears the previous round comes from the GNU directory, so
# the constant it adds is the same for all three implementations.
d="$out/lnbench"
mkdir -p "$d/deep/a/b/c" "$d/other/x/y"
: > "$d/src"
: > "$d/deep/a/b/c/f"
nums=$(seq 200 | tr '\n' ' ')
printf 'ln one hard link\ty\t{gnu}/rm -f %s/l; {} %s/src %s/l\n' "$d" "$d" "$d"
printf 'ln 200 hard links\ty\t{gnu}/rm -f %s/l.*; for n in %s; do {} %s/src %s/l.$n; done\n' "$d" "$nums" "$d" "$d"
printf 'ln 200 symbolic links\ty\t{gnu}/rm -f %s/s.*; for n in %s; do {} -s %s/src %s/s.$n; done\n' "$d" "$nums" "$d" "$d"
printf 'ln 200 forced replacements\ty\t{gnu}/rm -f %s/f.*; for n in %s; do {} -f %s/src %s/f.$n; done\n' "$d" "$nums" "$d" "$d"
printf 'ln 200 numbered backups\ty\t{gnu}/rm -f %s/bk*; : > %s/bk; for n in %s; do {} --backup=numbered -f %s/src %s/bk; done\n' "$d" "$d" "$nums" "$d" "$d"
printf 'ln 200 relative symbolic links\ty\t{gnu}/rm -f %s/other/x/y/r.*; for n in %s; do {} -sr %s/deep/a/b/c/f %s/other/x/y/r.$n; done\n' "$d" "$nums" "$d" "$d"
