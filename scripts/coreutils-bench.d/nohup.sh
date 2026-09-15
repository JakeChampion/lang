# nohup(1) is startup, three isatty questions, a signal disposition and an
# exec. None of the redirections fire here: hyperfine gives the child pipes,
# and every path that opens /dev/null or nohup.out needs a TERMINAL on the
# descriptor, which no benchmark harness hands out. So what these rows
# separate is how much of the time is nohup's own before the exec — which is
# the whole of it for a caller whose streams are already redirected, the
# case a shell script hits.
#
# The command is /bin/true throughout so the exec's own cost is a constant
# term, and the not-found row is the one that never execs at all: it reaches
# the PATH search, fails it, and prints one line.
printf 'nohup a command\ty\t{} /bin/true\n'
printf 'nohup a command with arguments\ty\t{} /bin/echo hi\n'
printf 'nohup a command not found\ty\t{} nosuchcmd9090 2>/dev/null\n'
printf 'nohup no operand\ty\t{} 2>/dev/null\n'
printf 'nohup a status passed through\ty\t{} /bin/false\n'
