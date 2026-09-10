# -u is the shape a script runs in a loop and reads nothing but
# the kernel; the composite line is the one that scans both
# databases, once for the names and once for the group list.
printf 'id -u\tn\t{} -u\n'
printf 'id -un\tn\t{} -un\n'
printf 'id\tn\t{}\n'
printf 'id -G\tn\t{} -G\n'
printf 'id root\tn\t{} root\n'
