# Startup plus one uname(2). -a is the whole record through the
# same one syscall, so the two rows differ only in the formatting.
printf 'uname\tn\t{}\n'
printf 'uname -a\tn\t{} -a\n'
