. scripts/coreutils-bench.d/_utmp.sh

# users is one read, a filter and a sort, so the three shapes are: no
# database at all (startup and a failed open, which is every container),
# one login, and 4000 of them, where the sort is the whole difference.
printf 'users, no database\tn\t{} /nonexistent\n'
printf 'users of one login\tn\t{} %s\n' "$utmp_one"
printf 'users of 4000 logins\ty\t{} %s > /dev/null\n' "$utmp_many"
