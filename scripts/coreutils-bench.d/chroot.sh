# chroot cannot be measured doing its job. The syscall needs CAP_SYS_CHROOT,
# and a row that had it would be timing a process that then execs something
# else — so these three measure the part that runs before the kernel says no,
# which is the part this utility is made of: the argument grammar, the passwd
# and group parse, and startup.
#
# NEWROOT names a path that does not exist, which is the same answer at every
# privilege ON LINUX — sys_chroot resolves the path before checking the
# capability, so an unprivileged run gets ENOENT rather than EPERM and the
# row measures the same work either way.
#
# The third row is the one with content. When NEWROOT is not already "/",
# chroot does its user and group lookups BEFORE the chroot, so that row reads
# /etc/passwd and /etc/group and resolves a spec and a group list, then fails.
# The first two are startup wearing chroot's clothes, the same class as
# `true` and `hostid`, where a Fern static binary with no dynamic loader wins
# on the loader alone.
#
# uutils has no `chroot` (0.11.0 answers `unknown program 'chroot'`), the same
# absence that leaves kill, uptime and stdbuf without a uutils column.
printf 'chroot --help\tn\t{} --help\n'
printf 'chroot missing root\tn\t{} /no-such-root-bench true\n'
printf 'chroot userspec and groups\tn\t{} --userspec=root:root --groups=root /no-such-root-bench true\n'
