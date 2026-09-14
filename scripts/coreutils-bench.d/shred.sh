# shred destroys and often unlinks its input, so every row reseeds before it
# measures. The seeding comes from the GNU directory (`head -c` for the large
# files, `rm`/`mkdir` for the tree) so it is the same constant term for all
# three implementations, exactly as `yes`'s `head` is.
#
# The rows split three ways. The small-file rows are process startup plus a
# handful of syscalls per pass, measured once and 200 times. The 4 MiB rows
# are the write path itself: a pass is a rewind and then block-sized writes
# to the end, with one fdatasync at the end of each, so passes multiply the
# bytes written and `-n 1` against the 3-pass default shows what one pass
# costs. The `--random-source` rows read the pattern bytes from a file
# instead of the kernel's generator, which is the same write path with a
# cheaper source, and they are the only rows whose byte stream is identical
# across implementations.
#
# The rows that are shred's own are `-u` (which renames the file through
# shorter and shorter names, each rename followed by a directory fsync,
# before unlinking), `-n 0 -u` (that name-wiping loop with no data pass at
# all), `-z`'s extra zero pass, `-x`, which suppresses the rounding of the
# length up to the block size, and the sub-block row, where that rounding
# means the whole schedule runs TWICE — once at the file's own length and
# once at the block's.
#
# `rsrc` is far larger than any row needs because a `--random-source` that
# runs out is a fatal error: a row whose source dried up would be timing
# an early exit against an early exit. Every random pass takes its own
# bytes from it, so `-n 4` over 4 MiB can want 16 MiB of source.
d="$out/shredbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')
"$gnu/head" -c 4194304 /dev/urandom > "$d/src"
"$gnu/head" -c 20971520 /dev/urandom > "$d/rsrc"
printf 'shred one small file\ty\t{gnu}/head -c 4096 %s/src > %s/one; {} %s/one\n' "$d" "$d" "$d"
printf 'shred 200 small files\ty\t{gnu}/rm -rf %s/many; {gnu}/mkdir %s/many; for n in %s; do {gnu}/head -c 4096 %s/src > %s/many/f.$n; done; for n in %s; do {} %s/many/f.$n; done\n' \
  "$d" "$d" "$nums" "$d" "$d" "$nums" "$d"
printf 'shred 200 small files in one run\ty\t{gnu}/rm -rf %s/batch; {gnu}/mkdir %s/batch; for n in %s; do {gnu}/head -c 4096 %s/src > %s/batch/f.$n; done; {} %s/batch/f.*\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'shred 4MiB default passes\ty\t{gnu}/cp %s/src %s/big; {} %s/big\n' "$d" "$d" "$d"
printf 'shred 4MiB -n 1\ty\t{gnu}/cp %s/src %s/big1; {} -n 1 %s/big1\n' "$d" "$d" "$d"
printf 'shred 4MiB -n 1 -z\ty\t{gnu}/cp %s/src %s/bigz; {} -n 1 -z %s/bigz\n' "$d" "$d" "$d"
printf 'shred 4MiB -n 1 from a file source\ty\t{gnu}/cp %s/src %s/bigs; {} -n 1 --random-source=%s/rsrc %s/bigs\n' "$d" "$d" "$d" "$d"
printf 'shred 4MiB -n 4 from a file source\ty\t{gnu}/cp %s/src %s/bigs4; {} -n 4 --random-source=%s/rsrc %s/bigs4\n' "$d" "$d" "$d" "$d"
printf 'shred 4MiB -n 1 -x\ty\t{gnu}/cp %s/src %s/bigx; {} -n 1 -x %s/bigx\n' "$d" "$d" "$d"
printf 'shred 200 sub-block files\ty\t{gnu}/rm -rf %s/sub; {gnu}/mkdir %s/sub; for n in %s; do {gnu}/head -c 1000 %s/src > %s/sub/f.$n; done; {} %s/sub/f.*\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'shred -u 200 small files\ty\t{gnu}/rm -rf %s/rm; {gnu}/mkdir %s/rm; for n in %s; do {gnu}/head -c 4096 %s/src > %s/rm/f.$n; done; {} -u %s/rm/f.*\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'shred -n 0 -u 200 small files\ty\t{gnu}/rm -rf %s/rm0; {gnu}/mkdir %s/rm0; for n in %s; do {gnu}/head -c 4096 %s/src > %s/rm0/f.$n; done; {} -n 0 -u %s/rm0/f.*\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'shred -s 4096 of a 4MiB file\ty\t{gnu}/cp %s/src %s/bigp; {} -n 1 -s 4096 %s/bigp\n' "$d" "$d" "$d"
