. scripts/coreutils-bench.d/_utmp.sh

# uptime prints ONE line, so every row is dominated by process startup --
# which is the point: this is a utility a prompt or a status bar runs on a
# timer, and the line it prints is a fixed cost after the database is read.
#
# What separates the rows is how much database there is to read for that one
# line. `users` and `who` format a row per record; uptime only COUNTS them,
# so the 4000-login row is the per-record scan with none of the formatting,
# and the gap between it and the one-login row is the whole of the scan.
#
# The `--help` row is the option path with no file read at all, which is the
# floor for both implementations.
printf 'uptime of one login\tn\t{} %s\n' "$utmp_one"
printf 'uptime of 4000 logins\tn\t{} %s\n' "$utmp_many"
printf 'uptime, no database\tn\t{} /nonexistent 2>/dev/null\n'
printf 'uptime --help\tn\t{} --help > /dev/null\n'
