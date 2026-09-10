# The same shape from the other side. `: > f` is a shell redirection
# rather than a fork, so seeding the operands costs an open and a close
# each and does not swamp the 200 process startups being measured.
d="$out/unlinkbench"
mkdir -p "$d"
printf 'unlink one file\ty\t: > %s/v; {} %s/v\n' "$d" "$d"
printf 'unlink 200 files\ty\tfor n in %s; do : > %s/v.$n; done; for n in %s; do {} %s/v.$n; done\n' "$(seq 200 | tr '\n' ' ')" "$d" "$(seq 200 | tr '\n' ' ')" "$d"
