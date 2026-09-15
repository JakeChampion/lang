# cp writes its destination, so every row reseeds before it measures. The
# seeding is the same shell work for all three implementations — `: > f` is
# a redirection rather than a fork, and the `mkdir` / `rm` that build and
# clear the tree come from the GNU directory — so it is a constant term,
# exactly as `mv`'s is.
#
# The rows are chosen to separate the four things a copy costs, because a
# single "copy a file" row cannot tell them apart:
#
#   - process startup, measured once and 200 times. A small copy is almost
#     entirely startup, which is why the 200-file rows are where an
#     implementation's per-file overhead shows.
#   - throughput, on a 64 MiB file: read, write, and nothing else. This is
#     the row a copy_file_range offload would move and the one where the
#     read buffer size is visible.
#   - the hole path, on a 64 MiB file that is one block of data and all
#     hole. `--sparse=never` writes it out in full and is the control;
#     the default detects it and writes one block, so the pair measures
#     the detection rather than the copying.
#   - the syscall path, on a 400-file tree: two stats, an open pair and a
#     chmod per entry, where the data is a rounding error. `-a` adds the
#     ownership and timestamp calls on top of the same walk, so the two
#     tree rows together price the attributes.
#
# `-l` is the fourth shape and its own row: it copies no data at all, so
# it prices the walk with the data removed.
d="$out/cpbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')

# One 64 MiB dense file and one 64 MiB sparse file, built once. `dd` comes
# from the GNU directory so the seeding cost is identical for every
# implementation, and both files are made before any row runs.
printf 'cp one file\ty\t: > %s/v; {} %s/v %s/w\n' "$d" "$d" "$d"
printf 'cp 200 files\ty\tfor n in %s; do : > %s/v.$n; done; for n in %s; do {} %s/v.$n %s/w.$n; done\n' \
  "$nums" "$d" "$nums" "$d" "$d"
printf 'cp 200 files into a directory\ty\t{gnu}/rm -rf %s/into; {gnu}/mkdir -p %s/into; for n in %s; do : > %s/s.$n; done; {} %s/s.* %s/into\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'cp 200 over existing files\ty\tfor n in %s; do : > %s/o.$n; : > %s/p.$n; done; for n in %s; do {} %s/o.$n %s/p.$n; done\n' \
  "$nums" "$d" "$d" "$nums" "$d" "$d"
printf 'cp 64 MiB\ty\t{gnu}/dd if=/dev/zero of=%s/big bs=1M count=64 status=none; {} %s/big %s/big.out\n' \
  "$d" "$d" "$d"
printf 'cp 64 MiB sparse\ty\t{gnu}/truncate -s 64M %s/sp; {gnu}/dd if=/dev/zero of=%s/sp bs=4096 count=1 seek=16383 conv=notrunc status=none; {} %s/sp %s/sp.out\n' \
  "$d" "$d" "$d" "$d"
printf 'cp 64 MiB sparse --sparse=never\ty\t{gnu}/truncate -s 64M %s/sn; {gnu}/dd if=/dev/zero of=%s/sn bs=4096 count=1 seek=16383 conv=notrunc status=none; {} --sparse=never %s/sn %s/sn.out\n' \
  "$d" "$d" "$d" "$d"
printf 'cp -p 200 files\ty\tfor n in %s; do : > %s/q.$n; done; for n in %s; do {} -p %s/q.$n %s/r.$n; done\n' \
  "$nums" "$d" "$nums" "$d" "$d"
printf 'cp -l 200 files\ty\t{gnu}/rm -f %s/h.*; for n in %s; do : > %s/g.$n; done; for n in %s; do {} -l %s/g.$n %s/h.$n; done\n' \
  "$d" "$nums" "$d" "$nums" "$d" "$d"
printf 'cp -r a 400-file tree\ty\t{gnu}/rm -rf %s/t %s/t2; {gnu}/mkdir -p %s/t/a %s/t/b; for n in %s; do : > %s/t/a/f.$n; : > %s/t/b/f.$n; done; {} -r %s/t %s/t2\n' \
  "$d" "$d" "$d" "$d" "$nums" "$d" "$d" "$d" "$d"
printf 'cp -a a 400-file tree\ty\t{gnu}/rm -rf %s/u %s/u2; {gnu}/mkdir -p %s/u/a %s/u/b; for n in %s; do : > %s/u/a/f.$n; : > %s/u/b/f.$n; done; {} -a %s/u %s/u2\n' \
  "$d" "$d" "$d" "$d" "$nums" "$d" "$d" "$d" "$d"
