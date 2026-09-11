# mktemp is startup-bound: one process draws a name and either prints it
# (-u) or creates an entry first. What costs anything beyond startup is
# whether the filesystem is touched at all and how long the random run
# is, so the sweep is -u against a real create, a file against a
# directory, and a 6-character run against a 40-character one. The
# created entries land under $out, which is removed when the run ends.
mdir="$out/mktemp-out"
mkdir -p "$mdir"
printf 'mktemp -u default template\tn\t{} -u\n'
printf 'mktemp -u fooXXXXXX\tn\t{} -u fooXXXXXX\n'
printf 'mktemp -u 40 random characters\tn\t{} -u XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX\n'
printf 'mktemp create a file\tn\t{} -p %s XXXXXX\n' "$mdir"
printf 'mktemp create a directory\tn\t{} -d -p %s XXXXXX\n' "$mdir"
