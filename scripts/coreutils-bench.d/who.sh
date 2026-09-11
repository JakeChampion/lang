. scripts/coreutils-bench.d/_utmp.sh

# who's cost splits three ways: startup and a failed open (a container,
# where the database is not there at all), one stat per row for the
# message status and idle time, and the row formatting itself. -q does
# none of the per-row work, so the pair of 4000-login rows says what the
# stats and the columns cost.
printf 'who, no database\tn\t{} /nonexistent\n'
printf 'who of one login\tn\t{} %s\n' "$utmp_one"
printf 'who of 4000 logins\ty\t{} %s > /dev/null\n' "$utmp_many"
printf 'who -a of 4000 logins\ty\t{} -a %s > /dev/null\n' "$utmp_many"
printf 'who -q of 4000 logins\ty\t{} -q %s > /dev/null\n' "$utmp_many"
