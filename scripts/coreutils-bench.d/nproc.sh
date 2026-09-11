# Startup plus one sched_getaffinity(2); --all instead reads the
# per-CPU directories out of /sys, so it is startup plus a
# getdents walk.
printf 'nproc\tn\t{}\n'
printf 'nproc --all\tn\t{} --all\n'
printf 'nproc --ignore=1\tn\t{} --ignore=1\n'
