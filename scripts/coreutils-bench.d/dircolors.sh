# dircolors is startup-bound in the shape people run it in — one
# compiled-in database parsed and written back as 1.8 KiB of shell — so
# the first three rows are that, once per emitter. TERM and SHELL are set
# in the template because both change the output, and the environment the
# bench inherits is not the environment the corpus proves.
env='SHELL=/bin/bash TERM=linux COLORTERM='
printf 'dircolors\ty\t%s {} > /dev/null\n' "$env"
printf 'dircolors -p\ty\t%s {} -p > /dev/null\n' "$env"
printf 'dircolors --print-ls-colors\ty\t%s {} --print-ls-colors > /dev/null\n' "$env"

# The throughput shape: a config with more entries than any real one, so
# the per-entry cost — the line scan, the keyword lookup and the shell
# escaper's toggle — is what is being timed rather than process startup.
big="$out/dircolors-200k.conf"
[ -f "$big" ] || python3 - "$big" <<'PY'
import sys
with open(sys.argv[1], "w") as f:
    for i in range(200000):
        f.write(".ext%06d 01;3%d\n" % (i, i % 8))
PY
printf 'dircolors a 200k-entry config\ty\t%s {} %s > /dev/null\n' "$env" "$big"
printf 'dircolors --print-ls-colors a 200k-entry config\ty\t%s {} --print-ls-colors %s > /dev/null\n' "$env" "$big"
