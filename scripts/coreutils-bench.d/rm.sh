# rm destroys its own input, so every row reseeds before it measures. The
# seeding is the same shell work for all three implementations — `: > f` is
# a redirection rather than a fork, and the `mkdir` that builds the tree
# comes from the GNU directory — so it is a constant term, exactly as
# `yes`'s `head` is.
d="$out/rmbench"
mkdir -p "$d"
seq200=$(seq 200 | tr '\n' ' ')
printf 'rm one file\ty\t: > %s/v; {} %s/v\n' "$d" "$d"
printf 'rm 200 files in one call\ty\tfor n in %s; do : > %s/v.$n; done; {} %s/v.*\n' \
  "$seq200" "$d" "$d"
printf 'rm -r a 400-file tree\ty\t{gnu}/mkdir -p %s/t/a %s/t/b; for n in %s; do : > %s/t/a/f.$n; : > %s/t/b/f.$n; done; {} -r %s/t\n' \
  "$d" "$d" "$seq200" "$d" "$d" "$d"
