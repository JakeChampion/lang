# Four shapes, and on this host they measure one thing: startup. GNU answers
# every one of them in 1.3-1.4 ms with 1.1-1.2 ms of that in user time, so the
# loader and libc init are the whole cost and kill's own work is below the
# noise — printing 62 names costs what converting one costs.
#
# That is the `true` / `false` / `hostid` class, where a Fern static binary
# with no dynamic loader is about 5x faster, so these rows are a startup
# measurement wearing kill's clothes. They are still worth having: -l and -t
# walk the whole signal table and would show it if that walk ever stopped
# being free.
#
# uutils has no `kill` (0.11.0 answers `unknown program 'kill'`), nor uptime,
# chroot or stdbuf, so this utility has no uutils column by absence rather
# than by omission.
#
# THE SENDING ROW USES SIGNAL 0. It checks permission and delivers nothing, so
# the bench cannot signal anything real; pid 1 exists on every host, which
# keeps the row off the error path. A row that actually sent would be timing
# the target's death, not kill.
printf 'kill -l\tn\t{} -l\n'
printf 'kill -t\tn\t{} -t\n'
printf 'kill -l 9\tn\t{} -l 9\n'
printf 'kill -0 1\tn\t{} -0 1\n'
