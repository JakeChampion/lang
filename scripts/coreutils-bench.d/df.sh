# df does a fixed, tiny amount of work: read /proc/self/mountinfo, stat
# and statfs once per mount, format a page of text. There is no input to
# scale, so every row here is dominated by process startup — which is the
# point, since that is where a static Fern binary is meant to win.
#
# What DOES vary is how many mount entries the run touches, and the two
# ways of choosing them cost differently: an operand resolves ONE mount
# and issues one statfs, while a listing stats and statfs's every entry
# in the table. The machine's own table is the input for the listing
# rows, so their absolute numbers are not comparable between machines —
# only down their own column, which is what `docs/COREUTILS.md` says of
# every row in it.
#
# A synthesized mount table is not on offer: /proc/self/mountinfo is the
# kernel's, a mount(2) needs privileges the bench does not assume, and
# pointing the run at a file somewhere else would measure a code path
# neither implementation has. So the listing rows measure this machine,
# and the operand rows — which touch one mount whatever is mounted —
# carry the comparison that travels.
probe="$out/df-probe"
mkdir -p "$probe/sub"
: > "$probe/sub/f"

printf 'one operand\tn\t{} %s\n' "$probe/sub/f"
printf 'one operand, human-readable\tn\t{} -h %s\n' "$probe/sub/f"
printf 'one operand, inodes\tn\t{} -i %s\n' "$probe/sub/f"
printf 'a pseudo-filesystem operand\tn\t{} /proc\n'
printf 'eight operands\tn\t{} %s %s /proc /sys /proc /sys %s %s\n' "$probe" "$probe/sub" "$probe/sub/f" "$probe"
printf 'the whole table\tn\t{}\n'
printf 'the whole table, \055a\tn\t{} -a\n'
printf 'the whole table, \055T \055\055total\tn\t{} -T --total\n'
printf 'the whole table, \055\055output\tn\t{} --output\n'
printf 'the whole table, \055h\tn\t{} -h\n'
