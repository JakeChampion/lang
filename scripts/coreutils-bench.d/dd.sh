# dd is a read loop and a write loop with a block size between them, so the
# rows are that block size: the default 512 makes 16384 read/write pairs per
# 8 MiB where `bs=1M` makes 8, and everything else here is what happens
# BETWEEN the two.
#
# `bs=` is the row that goes straight through — one read becomes one write,
# with nothing held — while `ibs=`/`obs=` differing is the assembling path,
# which copies every byte into a block buffer before writing it. A
# byte-rewriting `conv=` forces that same assembly even under `bs=`, so the
# conv rows measure the rewrite AND the copy it implies.
#
# The /dev/zero rows take the filesystem out: what is left is the syscall
# pair and the loop around it, which is where a small `bs=` is decided. The
# pipe row is the other end of that — a read that returns less than it was
# asked for is a partial record, and `iflag=fullblock` is what makes it keep
# reading instead.
d="$out/ddbench"
mkdir -p "$d"
src="$d/src8m"
[ -f "$src" ] || "$gnu/head" -c 8388608 /dev/urandom > "$src"
lines="$d/lines"
[ -f "$lines" ] || "$gnu/seq" 1 400000 > "$lines"
printf 'dd one small copy\ty\t{} if=%s of=%s bs=4096 count=1 status=none\n' "$src" "$d/one"
printf 'dd 8MiB bs=512\ty\t{} if=%s of=%s bs=512 status=none\n' "$src" "$d/out"
printf 'dd 8MiB bs=4096\ty\t{} if=%s of=%s bs=4096 status=none\n' "$src" "$d/out"
printf 'dd 8MiB bs=64k\ty\t{} if=%s of=%s bs=64k status=none\n' "$src" "$d/out"
printf 'dd 8MiB bs=1M\ty\t{} if=%s of=%s bs=1M status=none\n' "$src" "$d/out"
printf 'dd 8MiB default block\ty\t{} if=%s of=%s status=none\n' "$src" "$d/out"
printf 'dd 8MiB ibs 4k obs 64k\ty\t{} if=%s of=%s ibs=4096 obs=65536 status=none\n' "$src" "$d/out"
printf 'dd 8MiB conv=swab\ty\t{} if=%s of=%s bs=64k conv=swab status=none\n' "$src" "$d/out"
printf 'dd 8MiB conv=ucase\ty\t{} if=%s of=%s bs=64k conv=ucase status=none\n' "$src" "$d/out"
printf 'dd 8MiB conv=sync\ty\t{} if=%s of=%s bs=64k conv=sync status=none\n' "$src" "$d/out"
printf 'dd conv=block cbs=16\ty\t{} if=%s of=%s bs=64k conv=block cbs=16 status=none\n' "$lines" "$d/out"
printf 'dd conv=unblock cbs=16\ty\t{} if=%s of=%s bs=64k conv=unblock cbs=16 status=none\n' "$d/out" "$d/out2"
printf 'dd 8MiB skip and seek\ty\t{} if=%s of=%s bs=4096 skip=512 seek=512 status=none\n' "$src" "$d/out"
printf 'dd 64MiB zero to null bs=512\ty\t{} if=/dev/zero of=/dev/null bs=512 count=131072 status=none\n'
printf 'dd 64MiB zero to null bs=64k\ty\t{} if=/dev/zero of=/dev/null bs=65536 count=1024 status=none\n'
printf 'dd 8MiB from a pipe\ty\t{gnu}/cat %s | {} of=%s bs=64k status=none\n' "$src" "$d/out"
printf 'dd 8MiB from a pipe fullblock\ty\t{gnu}/cat %s | {} of=%s bs=64k iflag=fullblock status=none\n' "$src" "$d/out"
