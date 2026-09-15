# stty(1) needs a TERMINAL: on a pipe every invocation stops at
# `Inappropriate ioctl for device`, and hyperfine hands its children pipes.
# `-F /dev/ptmx` is the way out — each open of it is a fresh pseudo-terminal
# MASTER, which answers TCGETS, TCSETS and TIOCGWINSZ like any other
# terminal, and closes with the process. So these rows do the utility's real
# work: one ioctl in, the formatting or the table walk, and one ioctl back.
#
# The rows separate the three output forms, because they cost different
# things. `-a` formats every name and every control character; `-g` is
# thirty-six hex fields; the default form walks the same table but prints
# only what differs from `sane`, so it is the comparison and not the
# formatting that dominates. The setting rows separate one flag from a
# combination that expands to twenty, and from a `-g` restore, which parses
# thirty-six numbers before it sets anything.
#
# The last row never reaches a terminal at all: it is the argument scan and
# the diagnostic, which is what a script hits when it gets a name wrong.
printf 'stty print all\ty\t{} -F /dev/ptmx -a\n'
printf 'stty print stty-readable\ty\t{} -F /dev/ptmx -g\n'
printf 'stty print the deviations\ty\t{} -F /dev/ptmx\n'
printf 'stty print the size\ty\t{} -F /dev/ptmx size\n'
printf 'stty set one flag\ty\t{} -F /dev/ptmx -echo\n'
printf 'stty set six settings\ty\t{} -F /dev/ptmx -echo -icanon min 1 time 0 erase ^H intr ^C\n'
printf 'stty set the reference set\ty\t{} -F /dev/ptmx sane\n'
printf 'stty set a combination\ty\t{} -F /dev/ptmx raw\n'
printf 'stty restore a saved line\ty\t{} -F /dev/ptmx 500:5:bf:8a3b:3:1c:7f:15:4:0:1:0:11:13:1a:0:12:f:17:16:0:0:0:0:0:0:0:0:0:0:0:0:0:0:0:0\n'
printf 'stty reject a bad name\ty\t{} -F /dev/ptmx bogusmode 2>/dev/null\n'
