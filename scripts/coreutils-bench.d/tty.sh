# Startup, one isatty(2) and one write: with stdin a pipe the answer is
# `not a tty` and neither side ever looks at /proc or /dev, so the row is
# the static-binary margin and nothing else. The `-s` row drops the write.
# The third takes a real terminal — /dev/ptmx is a pty master — so ttyname
# runs: one readlink of /proc/self/fd/0 and one stat of what it names.
printf 'tty from a pipe\ty\ttrue | {}\n'
printf 'tty -s from a pipe\ty\ttrue | {} -s\n'
printf 'tty on a terminal\ty\t{} < /dev/ptmx\n'
