# env's own work is small and its dominating cost is process startup, so the
# rows are chosen to separate the two. The dump rows never exec anything, so
# they measure the environment walk and the writes alone; the exec rows pay one
# startup for env plus one for the child, and `{gnu}/true` is the same child for
# all three implementations exactly as `mkdir`'s `rm` is.
#
# The rows that are env's OWN work are the ones that do something per entry or
# per byte: `-u` is a scan of the vector for every name given, a long list of
# assignments is a lookup-and-replace each, and `-S` is a character-at-a-time
# tokeniser over a string that gets long on purpose. `-v` adds a write per step,
# which is what makes it worth having beside the same run without it.
names=$(seq 60 | while read -r n; do printf 'V%s=%s ' "$n" "$n"; done)
unsets=$(seq 60 | while read -r n; do printf -- '-u V%s ' "$n"; done)
longs=$(seq 40 | while read -r n; do printf 'tok%s ' "$n"; done)

printf 'env dump the inherited environment\ty\t{}\n'
printf 'env dump with 60 assignments\ty\t{} %s\n' "$names"
printf 'env -i dump with 60 assignments\ty\t{} -i %s\n' "$names"
printf 'env -0 dump with 60 assignments\ty\t{} -0 %s\n' "$names"
printf 'env -u 60 names off a 60-entry vector\ty\t{} -i %s %s\n' "$names" "$unsets"
printf 'env exec true\ty\t{} {gnu}/true\n'
printf 'env -i exec true\ty\t{} -i {gnu}/true\n'
printf 'env 60 assignments then exec true\ty\t{} %s {gnu}/true\n' "$names"
printf 'env -v exec true\ty\t{} -v {gnu}/true 2>/dev/null\n'
printf 'env -v 60 assignments then exec true\ty\t{} -v %s {gnu}/true 2>/dev/null\n' "$names"
printf 'env PATH search for a bare name\ty\t{} true\n'
printf 'env -S split 40 tokens\ty\t{} -S"{gnu}/true %s"\n' "$longs"
printf 'env -S split with expansions\ty\t{} -S"{gnu}/true ${PATH} ${HOME} ${LANG} %s"\n' "$longs"
printf 'env -S split a quoted string\ty\t{} -S"{gnu}/true '"'"'%s'"'"'"\n' "$longs"
printf 'env --block-signal all signals\ty\t{} --block-signal {gnu}/true\n'
printf 'env --ignore-signal=INT,TERM,HUP\ty\t{} --ignore-signal=INT,TERM,HUP {gnu}/true\n'
printf 'env --list-signal-handling with three set\ty\t{} --block-signal=INT,QUIT --ignore-signal=TERM --list-signal-handling {gnu}/true 2>/dev/null\n'
printf 'env -C then exec\ty\t{} -C / {gnu}/true\n'
