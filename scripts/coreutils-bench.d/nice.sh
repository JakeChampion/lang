# nice(1) is startup plus one getpriority in the read rows, and startup
# plus a setpriority and an exec in the rest. The command it runs has to
# be the same on every side, so it is /bin/true throughout: what the rows
# separate is how much of the time is nice's own.
#
# The renice row asks to go DOWN, which needs privilege. An unprivileged
# run takes the warning path instead — one write to stderr rather than a
# second syscall — so the row measures whichever the box allows; both are
# nice's own work and neither is the command's.
#
# The obsolescent -N form goes through the pre-pass that rewrites it into
# -n N, and the long form through getopt_long's prefix match, so the two
# spelling rows are what says the argument handling costs nothing next to
# the exec.
printf 'nice reads the niceness\ty\t{}\n'
printf 'nice runs a command\ty\t{} /bin/true\n'
printf 'nice -n 5 a command\ty\t{} -n 5 /bin/true\n'
printf 'nice -5 a command\ty\t{} -5 /bin/true\n'
printf 'nice --adjustment=5 a command\ty\t{} --adjustment=5 /bin/true\n'
printf 'nice -n -5 a command\ty\t{} -n -5 /bin/true 2>/dev/null\n'
printf 'nice a command not found\ty\t{} nosuchcmd9090 2>/dev/null\n'
printf 'nice an invalid adjustment\ty\t{} -n abc /bin/true 2>/dev/null\n'
