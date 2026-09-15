# timeout(1) is startup, a setpgid, two forks and a wait, and the rows
# separate how much of that is timeout's own from what the command costs.
#
# EVERY row runs a command that finishes well inside its deadline, so the
# deadline never fires and the measurement is the setup-and-teardown path —
# the one every invocation pays. A row that timed out would measure its own
# sleep instead, which is the wall clock and not the implementation.
#
# That is also the row where Fern and GNU differ structurally. GNU arms a
# SIGALRM and waits; Fern forks a timer child, because it cannot observe a
# delivered signal (#9243), so it pays one extra fork and one extra reap per
# invocation. The 0-duration row is the control: it takes no timer at all on
# either side, so the gap between it and the plain row IS that cost.
#
# --foreground skips the setpgid, and the exec-failure row stops before the
# fork of the timer, so between them they bracket where the time goes.
printf 'timeout a command\ty\t{} 10 /bin/true\n'
printf 'timeout zero disables\ty\t{} 0 /bin/true\n'
printf 'timeout --foreground\ty\t{} --foreground 10 /bin/true\n'
printf 'timeout -s KILL\ty\t{} -s KILL 10 /bin/true\n'
printf 'timeout -k with a grace period\ty\t{} -k 10 10 /bin/true\n'
printf 'timeout -v\ty\t{} -v 10 /bin/true\n'
printf 'timeout --preserve-status\ty\t{} --preserve-status 10 /bin/true\n'
printf 'timeout a fractional duration\ty\t{} 10.5s /bin/true\n'
printf 'timeout a suffixed duration\ty\t{} 1h /bin/true\n'
printf 'timeout a command with arguments\ty\t{} 10 /bin/echo a b c >/dev/null\n'
printf 'timeout a command not found\ty\t{} 10 nosuchcmd9090 2>/dev/null\n'
printf 'timeout an invalid duration\ty\t{} x /bin/true 2>/dev/null\n'
