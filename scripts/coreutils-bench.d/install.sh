# install writes its destination and sets its mode, so every row reseeds
# before it measures. The seeding is the same shell work for all three
# implementations — `: > f` is a redirection rather than a fork, and the
# `mkdir` / `rm` come from the GNU directory — so it is a constant term,
# as `cp`'s and `mv`'s are.
#
# The rows separate what install costs beyond a copy, because a single
# "install a file" row cannot tell them apart:
#
#   - process startup, measured once and 200 times. A small install is
#     almost entirely startup, so the 200-file rows are where per-file
#     overhead shows.
#   - the mode call. install chmods every destination outright rather
#     than filtering through the umask, so `-m` is not a discount and
#     the plain row already pays it; the pair is here to show that.
#   - the DIRECTORY forms, which do no copying at all. `-d` on 200 fresh
#     names is mkdir plus chmod per name, and the nested row prices the
#     component walk.
#   - `-C`, which is two stats and a full read of both files when they
#     match, and a read plus an unlink plus a copy when they do not. The
#     matching row is the one that should be cheapest of all and is the
#     reason the option exists.
#   - `-s`, which forks and execs per file. `/bin/true` stands in for
#     `strip` so the row prices the fork rather than binutils.
d="$out/installbench"
mkdir -p "$d"
nums=$(seq 200 | tr '\n' ' ')

printf 'install one file\ty\t: > %s/v; {} %s/v %s/w\n' "$d" "$d" "$d"
printf 'install 200 files\ty\tfor n in %s; do : > %s/v.$n; done; for n in %s; do {} %s/v.$n %s/w.$n; done\n' \
  "$nums" "$d" "$nums" "$d" "$d"
printf 'install -m 200 files\ty\tfor n in %s; do : > %s/m.$n; done; for n in %s; do {} -m 640 %s/m.$n %s/n.$n; done\n' \
  "$nums" "$d" "$nums" "$d" "$d"
printf 'install 200 over existing\ty\tfor n in %s; do : > %s/o.$n; : > %s/p.$n; done; for n in %s; do {} %s/o.$n %s/p.$n; done\n' \
  "$nums" "$d" "$d" "$nums" "$d" "$d"
printf 'install 200 into a directory\ty\t{gnu}/rm -rf %s/into; {gnu}/mkdir -p %s/into; for n in %s; do : > %s/s.$n; done; {} %s/s.* %s/into\n' \
  "$d" "$d" "$nums" "$d" "$d" "$d"
printf 'install -d 200 directories\ty\t{gnu}/rm -rf %s/dd; {gnu}/mkdir -p %s/dd; for n in %s; do {} -d %s/dd/$n; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'install -d 200 nested\ty\t{gnu}/rm -rf %s/nn; {gnu}/mkdir -p %s/nn; for n in %s; do {} -d %s/nn/$n/a/b; done\n' \
  "$d" "$d" "$nums" "$d"
printf 'install -D 200 files\ty\t{gnu}/rm -rf %s/lead; {gnu}/mkdir -p %s/lead; for n in %s; do : > %s/l.$n; done; for n in %s; do {} -D %s/l.$n %s/lead/$n/out; done\n' \
  "$d" "$d" "$nums" "$d" "$nums" "$d" "$d"
printf 'install -C 200 matching\ty\t{gnu}/dd if=/dev/urandom of=%s/cs bs=4096 count=1 status=none; for n in %s; do {gnu}/cp %s/cs %s/c.$n; {} -C %s/cs %s/c.$n; done\n' \
  "$d" "$nums" "$d" "$d" "$d" "$d"
printf 'install -C 200 differing\ty\t{gnu}/dd if=/dev/urandom of=%s/ds bs=4096 count=1 status=none; for n in %s; do : > %s/e.$n; {} -C %s/ds %s/e.$n; done\n' \
  "$d" "$nums" "$d" "$d" "$d"
printf 'install -s 200 files\ty\tfor n in %s; do : > %s/t.$n; done; for n in %s; do {} -s --strip-program=/bin/true %s/t.$n %s/u.$n; done\n' \
  "$nums" "$d" "$nums" "$d" "$d"
printf 'install 64 MiB\ty\t{gnu}/dd if=/dev/zero of=%s/big bs=1M count=64 status=none; {} %s/big %s/big.out\n' \
  "$d" "$d" "$d"
