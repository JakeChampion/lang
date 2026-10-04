# The coreutils under both Fern compilers — 2026-10-03

Status: [measurement] — the whole corpus of `scripts/coreutils-bench`'s
two-compiler leg, every utility the host's GNU tree provides. `fern` is
`bin/fern`'s build of a utility and `fern-sh` is `bin/fern-selfhost`'s build
of the same source, both with `-O`. The standing definition is
`docs/COREUTILS.md`; the summary lives in that file's Performance section.
This file is the raw run.

Host: Apple Silicon, arm64-darwin (macOS), GNU coreutils 9.12 (Homebrew),
uutils 0.6.0, `fern f9a9e3bf1` for every batch. Wall time under hyperfine,
mean ± σ over at least 20 runs; a cell names its run count where the row was
capped below 20. **Only comparable within one run on one machine** — never
paste a column from here beside another machine's. The earlier runs in
`docs/COREUTILS.md` were Linux x86-64; the ratios below are not a progress bar
against them, only the direction is.

Run in four batches with nothing else on the machine (a-l, m-p, q-s, t-y),
about six hours in all; one two-hour batch of sixty utilities did not finish
and was split. A ratio is mean(first) / mean(second), so above 1 the
second-named build is faster; in a `gnu / …` or `uutils / …` column, above 1
means Fern is faster.

## Headline

650 rows over 101 utilities; 628 comparable (both Fern builds ran the row).

| | native build | self-host build |
|---|---|---|
| rows faster than GNU | 417 / 628 (66%) | **452 / 628 (72%)** |
| rows faster than uutils | 485 / 620 (78%) | **530 / 620 (85%)** |
| median `fern / fern-sh` | — | **1.09x** (self-host faster) |
| rows where the self-host build is faster | — | 455 / 628 |

On 2026-09-17 (Linux x86-64) the self-host build lost to GNU on thirty rows
the native build won and the median row was 0.53x. The self-host compiler is
now the faster of the two on this host, on most rows, and it beats GNU and
uutils on more rows than the native compiler does. The accumulation cliffs
that run recorded (`tsort` 751x, `tac` unfinished, `seq` 7.6x) are gone:
`seq -w 1 1000000` is 3.1 ms under the self-host against GNU's 189 ms.

## Where the table says the self-host build is slower than native — and is not

29 rows show the self-host build 1.25-2.75x slower than native, every one a
small operation whose whole run is a few milliseconds (`install one file`
0.76 → 2.09 ms, `chown 200 numeric groups` 3.63 → 9.79 ms, `rm one file`
1.09 → 2.18 ms, `cp 200 files into a directory` 50.35 → 89.27 ms). Timed
again directly, with the same binaries, interleaved and with the seeding
outside the timed region, the gap is not there:

| workload | native | self-host |
|---|---|---|
| `rm --version` / `rm nosuchfile` / `rm f1` | 1.7 / 1.5 / 1.7 ms | 1.4 / 1.4 / 1.7 ms |
| `chown :20` over 200 files | 3.7 ± 0.2 ms | 3.9 ± 2.0 ms |
| `cp` 200 files into a directory | 25.3 ± 1.7 ms | 25.3 ± 2.5 ms |

Two things made the rows: the bench's `cp` / `install` / `mkdir` workloads
seed their trees inside the timed command (the comment in
`scripts/coreutils-bench.d/cp.sh` says why — the same shell work lands on
all three implementations), which puts 200 shell redirections and a
`rm -rf` in a 50 ms row with a σ of 27 ms native and 70 ms self-host; and
below about 5 ms a row is process startup plus one syscall, where the σ in
the table is the size of the difference it reports. Read the ratio columns
only above that. The self-host build has no small-operation cost the native
build lacks on this host.

## Where the self-host build is faster than native

Mostly the compute loops, which is the SSA emitter's register allocation
against the flat emitter's stack slots: `sha512sum` 3.0x, `sha384sum` 3.0x,
`factor` of 64-bit semiprimes 3.4x, `comm` 2.3-2.7x, `cksum -a sm3` 2.6x,
`sha256sum` 2.2x, `cut --complement` 2.3x, `join` 1.8-1.9x, `head -c` /
`head -n -N` over a 62 MiB file 2.9-3.7x, `tee` to stdout 4.1x, `numfmt`
200k lines 1.9x.

## What still loses to GNU, both builds

- **The digests and base encoders**, 0.07-0.4x: `b2sum`, `base32`, `base64`,
  `basenc`, `cksum -a sha256 / sm3 / blake2b / md5`, `md5sum`, `sha1sum`.
  GNU's are hand-tuned or vectorised kernels (and on this host some are the
  system's). The self-host build halves the gap on the SHA family (3.0x
  faster than native on sha512) but does not close it.
- **`fmt`**, 0.4-0.5x native / 0.7-0.8x self-host on every row (the
  2026-09-22 note on `fmt` in `docs/COREUTILS.md` stands).
- **`du` / `ls` / `dir`**, 0.6-0.9x on every directory-walk row.
- **`stat` with user and group names over 4000 operands**: 536 / 458 ms
  against 69 ms — each operand resolves its names again (every other `stat`
  row is at parity; fixed in #11339). The **`who` over 4000 logins** rows
  (0.21-0.27x) are not a comparison on this host: GNU's `who` on macOS reads
  utmpx records and ignored the Linux-shaped fixture, printing the live
  logins instead, so only Fern did the 4000 rows' work — 61% of it the
  per-row `stat` of `/dev/LINE` GNU also does where it reads the file.
- **`sync -f` over 200 files**: 11.2 s against 60 ms — a full filesystem
  flush per operand on this host (56 ms each), where GNU's `syncfs` fallback
  here is cheap.
- **`hostid`**: 23 ms against 1.6 ms — the hostname resolved through NSS for
  `gethostid`'s address fallback, which GNU answers from the C library.
- `pr -v` 0.26 / 0.41x, `dd conv=swab` 0.25 / 0.39x, `date -d` 0.16 / 0.38x,
  `shuf` 0.36x, `factor 1..200000` 0.92 / 1.37x.

## Rows this host could not compare

22 rows where both Fern builds exited non-zero on this host, so no time:
`df` (8 rows), `install -d` (2), `groups`, `logname`, `tty`, `yes`, and
`nice` (6 rows, exit 140 under both compilers). These are Darwin gaps in the
utilities or the runtime, not performance; `nice` is filed.

## A workload fixed

`date -d relative` spelled `… 3 months ago +0100`, which GNU refuses as an
invalid date (a numeric zone after relative items), so the row timed two
error exits and read as the self-host being 11x faster. The trailing zone is
gone from `scripts/coreutils-bench.d/date.sh`; the row is not in the table
above as a result worth reading.

## The run

| utility | workload | fern (ms) | fern-sh (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern | gnu / fern-sh | uutils / fern-sh | fern / fern-sh |
|---|---|---|---|---|---|---|---|---|---|---|
| `[` | [ string equality | 1.39 ± 0.11 | 1.36 ± 0.10 | 1.64 ± 0.50 | 3.15 ± 0.19 | 1.18× | 2.26× | 1.21× | 2.32× | 1.03× |
| `[` | [ integer compare | 1.32 ± 0.07 | 1.37 ± 0.72 | 1.68 ± 0.07 | 3.17 ± 0.10 | 1.27× | 2.41× | 1.23× | 2.32× | 0.96× |
| `[` | [ -f on a file | 1.35 ± 0.07 | 1.38 ± 0.10 | 1.69 ± 0.17 | 3.19 ± 0.12 | 1.25× | 2.36× | 1.22× | 2.31× | 0.98× |
| `[` | [ -nt on two files | 1.36 ± 0.09 | 1.38 ± 0.08 | 1.68 ± 0.07 | 3.24 ± 1.19 | 1.24× | 2.38× | 1.22× | 2.35× | 0.99× |
| `[` | [ 40-term and/or chain | 1.35 ± 0.49 | 1.35 ± 0.06 | 1.65 ± 0.09 | 3.40 ± 0.87 | 1.22× | 2.52× | 1.22× | 2.52× | 1.00× |
| `b2sum` | b2sum of a 62 MiB file | 313.39 ± 8.28 | 176.91 ± 1.72 | 66.77 ± 0.60 | 54.24 ± 3.85 | 0.21× | 0.17× | 0.38× | 0.31× | 1.77× |
| `b2sum` | b2sum of a 62 MiB file from a pipe | 319.76 ± 2.04 | 181.70 ± 2.25 | 80.00 ± 3.22 | 63.58 ± 1.36 | 0.25× | 0.20× | 0.44× | 0.35× | 1.76× |
| `b2sum` | b2sum --tag of a 62 MiB file | 311.65 ± 1.24 | 177.07 ± 4.83 | 67.93 ± 6.08 | 53.57 ± 0.66 | 0.22× | 0.17× | 0.38× | 0.30× | 1.76× |
| `b2sum` | b2sum of a small file | 1.32 ± 0.17 | 1.31 ± 0.12 | 1.70 ± 0.29 | 3.35 ± 0.14 | 1.28× | 2.53× | 1.30× | 2.56× | 1.01× |
| `b2sum` | b2sum -c over 500 small files | 13.55 ± 0.93 | 11.52 ± 0.90 | 10.09 ± 0.77 | 13.65 ± 1.18 | 0.74× | 1.01× | 0.88× | 1.18× | 1.18× |
| `b2sum` | b2sum -l 256 of a 62 MiB file | 311.48 ± 1.37 | 179.95 ± 13.47 | 69.53 ± 4.14 | 54.03 ± 1.07 | 0.22× | 0.17× | 0.39× | 0.30× | 1.73× |
| `base32` | base32 a 16 KiB file | 1.53 ± 0.52 | 1.45 ± 0.42 | 1.71 ± 0.05 | 3.46 ± 1.35 | 1.12× | 2.27× | 1.18× | 2.39× | 1.05× |
| `base32` | base32 -d a 16 KiB file | 1.45 ± 0.16 | 1.49 ± 0.06 | 1.97 ± 0.75 | 4.33 ± 1.03 | 1.36× | 2.99× | 1.32× | 2.90× | 0.97× |
| `base32` | base32 a 64 MiB file | 485.80 ± 7.95 | 298.54 ± 9.01 | 90.63 ± 1.58 | 47.79 ± 10.20 | 0.19× | 0.10× | 0.30× | 0.16× | 1.63× |
| `base32` | base32 -w0 a 64 MiB file | 413.31 ± 4.43 | 258.79 ± 2.46 | 41.67 ± 1.84 | 46.64 ± 3.57 | 0.10× | 0.11× | 0.16× | 0.18× | 1.60× |
| `base32` | base32 -d a 64 MiB file | 383.42 ± 3.46 | 234.26 ± 4.92 | 135.27 ± 1.99 | 493.89 ± 8.90 | 0.35× | 1.29× | 0.58× | 2.11× | 1.64× |
| `base64` | base64 a 16 KiB file | 1.59 ± 0.31 | 1.53 ± 0.09 | 1.76 ± 0.14 | 3.91 ± 0.99 | 1.11× | 2.46× | 1.15× | 2.55× | 1.04× |
| `base64` | base64 -d a 16 KiB file | 1.53 ± 0.29 | 1.54 ± 0.07 | 1.70 ± 0.10 | 3.51 ± 0.12 | 1.11× | 2.29× | 1.10× | 2.27× | 0.99× |
| `base64` | base64 a 64 MiB file | 438.09 ± 1.62 | 287.94 ± 3.52 | 72.60 ± 4.03 | 33.09 ± 0.87 | 0.17× | 0.08× | 0.25× | 0.11× | 1.52× |
| `base64` | base64 -w0 a 64 MiB file | 385.65 ± 4.73 | 255.23 ± 3.55 | 28.65 ± 0.84 | 30.73 ± 0.70 | 0.07× | 0.08× | 0.11× | 0.12× | 1.51× |
| `base64` | base64 -d a 64 MiB file | 332.29 ± 25.60 | 193.57 ± 2.08 | 145.50 ± 1.27 | 137.71 ± 10.63 | 0.44× | 0.41× | 0.75× | 0.71× | 1.72× |
| `basename` | basename one path | 1.41 ± 0.10 | 1.47 ± 0.09 | 1.76 ± 0.19 | 3.77 ± 0.29 | 1.24× | 2.66× | 1.20× | 2.57× | 0.96× |
| `basename` | basename -a 200 operands | 1.54 ± 0.26 | 1.56 ± 0.16 | 1.79 ± 0.12 | 3.94 ± 0.22 | 1.17× | 2.56× | 1.15× | 2.52× | 0.98× |
| `basenc` | basenc --base64 a 16 KiB file | 1.61 ± 0.12 | 1.54 ± 0.50 | 1.97 ± 0.17 | 3.84 ± 0.19 | 1.22× | 2.38× | 1.28× | 2.49× | 1.04× |
| `basenc` | basenc --base64 -w0 a 64 MiB file | 395.42 ± 16.97 | 250.85 ± 4.28 | 29.50 ± 0.89 | 32.14 ± 0.83 | 0.07× | 0.08× | 0.12× | 0.13× | 1.58× |
| `basenc` | basenc --base32hex -w0 a 64 MiB file | 419.24 ± 4.12 | 259.73 ± 4.89 | 76.03 ± 0.80 | 45.02 ± 1.05 | 0.18× | 0.11× | 0.29× | 0.17× | 1.61× |
| `basenc` | basenc --base16 -w0 a 64 MiB file | 628.57 ± 33.34 | 393.37 ± 15.15 | 47.89 ± 0.89 | 64.70 ± 1.21 | 0.08× | 0.10× | 0.12× | 0.16× | 1.60× |
| `basenc` | basenc --base2msbf -w0 an 8 MiB slice | 216.71 ± 23.01 | 126.33 ± 1.06 | 28.36 ± 0.89 | 25.97 ± 1.24 | 0.13× | 0.12× | 0.22× | 0.21× | 1.72× |
| `basenc` | basenc --z85 a 64 MiB file | 648.10 ± 28.51 | 316.22 ± 8.99 | 126.47 ± 1.22 | 63.20 ± 1.77 | 0.20× | 0.10× | 0.40× | 0.20× | 2.05× |
| `basenc` | basenc --base16 -d a 128 MiB stream | 445.83 ± 21.41 | 212.25 ± 1.93 | 136.02 ± 11.53 | 182.39 ± 5.68 | 0.31× | 0.41× | 0.64× | 0.86× | 2.10× |
| `cat` | cat a 62 MiB file | 6.32 ± 0.46 | 6.24 ± 0.51 | 6.33 ± 0.38 | 9.73 ± 0.89 | 1.00× | 1.54× | 1.02× | 1.56× | 1.01× |
| `cat` | cat two 62 MiB files | 10.84 ± 2.14 | 10.14 ± 0.71 | 10.36 ± 0.59 | 13.70 ± 0.67 | 0.96× | 1.26× | 1.02× | 1.35× | 1.07× |
| `cat` | cat from a pipe | 15.53 ± 1.32 | 18.00 ± 1.18 | 15.90 ± 0.92 | 17.49 ± 1.22 | 1.02× | 1.13× | 0.88× | 0.97× | 0.86× |
| `cat` | cat -n a 62 MiB file | 127.86 ± 9.90 | 111.66 ± 1.42 | 156.87 ± 6.84 | 112.33 ± 1.03 | 1.23× | 0.88× | 1.40× | 1.01× | 1.15× |
| `cat` | cat -s a 62 MiB file | 63.33 ± 5.78 | 65.38 ± 4.17 | 87.58 ± 2.13 | 85.71 ± 1.08 | 1.38× | 1.35× | 1.34× | 1.31× | 0.97× |
| `cat` | cat -A a 62 MiB file | 44.72 ± 0.89 | 39.91 ± 1.78 | 66.80 ± 2.86 | 107.37 ± 1.80 | 1.49× | 2.40× | 1.67× | 2.69× | 1.12× |
| `chgrp` | chgrp one numeric group | 2.62 ± 1.30 | 2.05 ± 1.50 | 2.68 ± 0.57 | 5.39 ± 0.69 | 1.02× | 2.06× | 1.31× | 2.63× | 1.28× |
| `chgrp` | chgrp 200 numeric groups in one call | 8.29 ± 0.96 | 7.67 ± 0.97 | 9.74 ± 1.18 | 12.39 ± 0.88 | 1.17× | 1.49× | 1.27× | 1.62× | 1.08× |
| `chgrp` | chgrp 200 numeric groups, one call each | 410.19 ± 48.56 | 374.43 ± 12.86 | 601.82 ± 23.11 | 1181.61 ± 26.63 | 1.47× | 2.88× | 1.61× | 3.16× | 1.10× |
| `chgrp` | chgrp 200 named groups in one call | 7.76 ± 0.96 | 8.90 ± 0.85 | 8.61 ± 1.74 | 11.37 ± 1.13 | 1.11× | 1.47× | 0.97× | 1.28× | 0.87× |
| `chgrp` | chgrp -R a 400-file tree | 8.90 ± 0.87 | 10.24 ± 6.44 | 39.18 ± 11.49 | 35.79 ± 15.86 | 4.40× | 4.02× | 3.82× | 3.49× | 0.87× |
| `chgrp` | chgrp -Rv a 400-file tree | 23.45 ± 2.93 | 18.93 ± 1.72 | 9.86 ± 0.86 | 14.02 ± 1.19 | 0.42× | 0.60× | 0.52× | 0.74× | 1.24× |
| `chgrp` | chgrp -Rc a 400-file tree | 9.70 ± 3.46 | 9.00 ± 1.14 | 7.70 ± 0.54 | 11.69 ± 1.71 | 0.79× | 1.20× | 0.86× | 1.30× | 1.08× |
| `chgrp` | chgrp -R by name a 400-file tree | 8.60 ± 1.42 | 8.92 ± 3.92 | 8.17 ± 2.00 | 11.83 ± 0.83 | 0.95× | 1.38× | 0.91× | 1.33× | 0.96× |
| `chgrp` | chgrp -R --reference a 400-file tree | 8.28 ± 1.15 | 8.48 ± 0.83 | 8.13 ± 1.64 | 11.56 ± 0.77 | 0.98× | 1.40× | 0.96× | 1.36× | 0.98× |
| `chgrp` | chgrp -Rh a 400-file tree | 8.49 ± 2.14 | 8.49 ± 2.38 | 8.18 ± 3.35 | 12.43 ± 0.93 | 0.96× | 1.46× | 0.96× | 1.46× | 1.00× |
| `chmod` | chmod one octal mode | 0.26 ± 1.84 | 0.09 ± 0.32 | 0.29 ± 0.55 | 1.65 ± 0.81 | 1.13× | 6.36× | 3.42× | 19.32× | 3.04× |
| `chmod` | chmod 200 octal modes in one call | 7.67 ± 1.37 | 11.00 ± 11.09 | 10.68 ± 13.19 | 7.48 ± 1.38 | 1.39× | 0.98× | 0.97× | 0.68× | 0.70× |
| `chmod` | chmod 200 octal modes, one call each | 333.06 ± 10.38 | 342.49 ± 11.16 | 409.49 ± 15.26 | 902.11 ± 43.45 | 1.23× | 2.71× | 1.20× | 2.63× | 0.97× |
| `chmod` | chmod 200 symbolic modes in one call | 6.98 ± 0.83 | 7.53 ± 0.84 | 7.18 ± 0.96 | 8.32 ± 0.77 | 1.03× | 1.19× | 0.95× | 1.10× | 0.93× |
| `chmod` | chmod 200 long symbolic modes in one call | 7.45 ± 1.75 | 9.38 ± 4.22 | 6.85 ± 0.96 | 11.01 ± 0.85 | 0.92× | 1.48× | 0.73× | 1.17× | 0.79× |
| `chmod` | chmod -R a 400-file tree | 1.37 ± 1.53 | 1.06 ± 0.52 | 1.81 ± 0.65 | 3.68 ± 2.23 | 1.32× | 2.68× | 1.71× | 3.48× | 1.30× |
| `chmod` | chmod -Rv a 400-file tree | 1.32 ± 0.64 | 1.77 ± 2.35 | 1.61 ± 0.56 | 3.49 ± 0.66 | 1.22× | 2.65× | 0.91× | 1.97× | 0.75× |
| `chmod` | chmod -R --reference a 400-file tree | 0.11 ± 0.37 | 0.21 ± 0.40 | 0.41 ± 3.01 | 1.33 ± 0.76 | 3.65× | 11.79× | 2.00× | 6.46× | 0.55× |
| `chown` | chown one numeric group | 1.56 ± 0.75 | 1.23 ± 0.66 | 6.05 ± 10.40 | 7.35 ± 8.84 | 3.87× | 4.70× | 4.92× | 5.98× | 1.27× |
| `chown` | chown 200 numeric groups in one call | 3.63 ± 6.33 | 9.79 ± 18.85 | 3.44 ± 6.15 | 4.56 ± 1.82 | 0.95× | 1.26× | 0.35× | 0.47× | 0.37× |
| `chown` | chown 200 numeric groups, one call each | 355.78 ± 15.49 | 365.95 ± 17.22 | 551.50 ± 18.52 | 1043.95 ± 12.36 | 1.55× | 2.93× | 1.51× | 2.85× | 0.97× |
| `chown` | chown 200 named groups in one call | 6.93 ± 2.11 | 6.64 ± 0.67 | 8.74 ± 0.88 | 10.62 ± 1.10 | 1.26× | 1.53× | 1.32× | 1.60× | 1.04× |
| `chown` | chown -R a 400-file tree | 15.35 ± 18.62 | 9.14 ± 0.78 | 8.74 ± 0.70 | 12.31 ± 0.99 | 0.57× | 0.80× | 0.96× | 1.35× | 1.68× |
| `chown` | chown -Rv a 400-file tree | 22.97 ± 0.90 | 18.97 ± 3.65 | 9.99 ± 2.51 | 14.42 ± 1.93 | 0.44× | 0.63× | 0.53× | 0.76× | 1.21× |
| `chown` | chown -Rc a 400-file tree | 8.39 ± 0.64 | 9.21 ± 1.85 | 11.05 ± 3.62 | 14.93 ± 2.52 | 1.32× | 1.78× | 1.20× | 1.62× | 0.91× |
| `chown` | chown -R --reference a 400-file tree | 10.55 ± 4.20 | 8.08 ± 0.65 | 7.66 ± 3.21 | 11.13 ± 0.92 | 0.73× | 1.05× | 0.95× | 1.38× | 1.31× |
| `chown` | chown -R --from a 400-file tree | 8.33 ± 1.24 | 8.04 ± 0.57 | 8.54 ± 0.98 | 12.02 ± 0.64 | 1.03× | 1.44× | 1.06× | 1.49× | 1.04× |
| `chown` | chown -Rh a 400-file tree | 8.82 ± 1.48 | 16.63 ± 15.59 | 11.99 ± 9.90 | 34.92 ± 32.29 | 1.36× | 3.96× | 0.72× | 2.10× | 0.53× |
| `chroot` | chroot --help | 3.25 ± 3.75 | 1.81 ± 1.52 | 2.81 ± 2.17 | 8.20 ± 5.82 | 0.86× | 2.52× | 1.55× | 4.54× | 1.80× |
| `chroot` | chroot missing root | 3.54 ± 4.05 | 1.86 ± 0.81 | 2.82 ± 4.57 | 9.39 ± 6.29 | 0.80× | 2.66× | 1.51× | 5.04× | 1.90× |
| `chroot` | chroot userspec and groups | 3.24 ± 3.27 | 2.52 ± 2.34 | 4.66 ± 3.78 | 5.56 ± 3.71 | 1.44× | 1.71× | 1.85× | 2.21× | 1.29× |
| `cksum` | cksum of a 62 MiB file | 10.09 ± 3.30 | 8.54 ± 0.88 | 43.64 ± 13.68 | 13.27 ± 4.60 | 4.32× | 1.32× | 5.11× | 1.56× | 1.18× |
| `cksum` | cksum of a 62 MiB file from a pipe | 16.75 ± 10.83 | 12.53 ± 1.32 | 35.19 ± 0.69 | 15.95 ± 2.85 | 2.10× | 0.95× | 2.81× | 1.27× | 1.34× |
| `cksum` | cksum -a sysv of a 62 MiB file | 8.71 ± 0.29 | 8.71 ± 0.22 | 8.72 ± 0.42 | 9.07 ± 0.18 | 1.00× | 1.04× | 1.00× | 1.04× | 1.00× |
| `cksum` | cksum -a bsd of a 62 MiB file | 90.19 ± 0.87 | 89.88 ± 0.75 | 91.10 ± 0.76 | 76.17 ± 0.64 | 1.01× | 0.84× | 1.01× | 0.85× | 1.00× |
| `cksum` | cksum -a sha256 of a 62 MiB file | 541.04 ± 20.42 | 251.15 ± 4.03 | 144.27 ± 1.49 | 139.03 ± 0.70 | 0.27× | 0.26× | 0.57× | 0.55× | 2.15× |
| `cksum` | cksum -a sm3 of a 62 MiB file | 694.00 ± 24.47 | 267.93 ± 2.83 | 151.22 ± 0.86 | 147.35 ± 1.37 | 0.22× | 0.21× | 0.56× | 0.55× | 2.59× |
| `cksum` | cksum -a blake2b of a 62 MiB file | 314.10 ± 5.55 | 176.39 ± 0.97 | 66.68 ± 0.53 | 57.29 ± 2.22 | 0.21× | 0.18× | 0.38× | 0.32× | 1.78× |
| `cksum` | cksum --untagged -a md5 of a 62 MiB file | 361.39 ± 1.97 | 266.67 ± 0.94 | 94.89 ± 4.02 | 96.78 ± 0.60 | 0.26× | 0.27× | 0.36× | 0.36× | 1.36× |
| `cksum` | cksum --raw of a 62 MiB file | 8.88 ± 0.57 | 8.69 ± 0.27 | 36.80 ± 0.80 | 10.84 ± 0.39 | 4.14× | 1.22× | 4.23× | 1.25× | 1.02× |
| `cksum` | cksum of a small file | 1.55 ± 0.42 | 1.52 ± 0.09 | 2.00 ± 0.52 | 6.06 ± 4.16 | 1.29× | 3.90× | 1.32× | 3.99× | 1.02× |
| `cksum` | cksum -a sha256 -c over 500 small files | 16.37 ± 8.72 | 8.50 ± 0.70 | 7.40 ± 1.00 | 10.60 ± 1.27 | 0.45× | 0.65× | 0.87× | 1.25× | 1.92× |
| `comm` | comm over 2M + 1M sorted lines | 163.65 ± 1.90 | 68.74 ± 1.30 | 1075.11 ± 94.23 | 160.72 ± 11.82 | 6.57× | 0.98× | 15.64× | 2.34× | 2.38× |
| `comm` | comm -12 over 2M + 1M sorted lines | 167.94 ± 25.36 | 61.90 ± 0.96 | 1007.27 ± 52.56 | 167.34 ± 29.80 | 6.00× | 1.00× | 16.27× | 2.70× | 2.71× |
| `comm` | comm --total over 2M + 1M sorted lines | 172.56 ± 32.59 | 69.93 ± 21.79 | 1150.88 ± 270.39 | 141.71 ± 2.61 | 6.67× | 0.82× | 16.46× | 2.03× | 2.47× |
| `comm` | comm of a 2M-line file with itself | 132.62 ± 26.27 | 58.27 ± 1.55 | 213.76 ± 2.32 | 120.42 ± 2.70 | 1.61× | 0.91× | 3.67× | 2.07× | 2.28× |
| `comm` | comm -123 of a 2M-line file with itself | 104.65 ± 4.63 | 40.03 ± 2.86 | 85.30 ± 2.48 | 96.17 ± 1.52 | 0.82× | 0.92× | 2.13× | 2.40× | 2.61× |
| `cp` | cp one file | 1.45 ± 0.67 | 1.55 ± 0.74 | 2.19 ± 0.71 | 4.69 ± 1.33 | 1.51× | 3.23× | 1.42× | 3.03× | 0.94× |
| `cp` | cp 200 files | 382.65 ± 11.50 | 380.06 ± 11.35 | 504.74 ± 98.81 | 997.73 ± 22.94 | 1.32× | 2.61× | 1.33× | 2.63× | 1.01× |
| `cp` | cp 200 files into a directory | 50.35 ± 5.10 | 89.27 ± 79.58 | 78.01 ± 24.83 | 106.42 ± 71.94 | 1.55× | 2.11× | 0.87× | 1.19× | 0.56× |
| `cp` | cp 200 over existing files | 677.93 ± 302.56 | 448.37 ± 170.15 | 464.07 ± 27.75 | 1175.02 ± 347.16 | 0.68× | 1.73× | 1.04× | 2.62× | 1.51× |
| `cp` | cp 64 MiB | 50.96 ± 35.56 | 46.79 ± 38.93 | 71.40 ± 74.37 | 49.03 ± 74.32 | 1.40× | 0.96× | 1.53× | 1.05× | 1.09× |
| `cp` | cp 64 MiB sparse | 45.06 ± 182.68 | 25.88 ± 7.87 | 13.02 ± 1.95 | 9.08 ± 1.11 | 0.29× | 0.20× | 0.50× | 0.35× | 1.74× |
| `cp` | cp 64 MiB sparse --sparse=never | 25.36 ± 8.21 | 24.24 ± 4.79 | 43.58 ± 23.36 | exit 1 | 1.72× | — | 1.80× | — | 1.05× |
| `cp` | cp -p 200 files | 356.51 ± 10.08 | 361.89 ± 13.61 | 671.13 ± 244.78 | 1535.51 ± 491.88 | 1.88× | 4.31× | 1.85× | 4.24× | 0.99× |
| `cp` | cp -l 200 files | 739.37 ± 275.87 | 497.02 ± 120.63 | 529.48 ± 13.97 | 1233.25 ± 294.95 | 0.72× | 1.67× | 1.07× | 2.48× | 1.49× |
| `cp` | cp -r a 400-file tree | 138.69 ± 99.75 | 100.56 ± 4.16 | 115.17 ± 3.56 | 141.23 ± 31.54 | 0.83× | 1.02× | 1.15× | 1.40× | 1.38× |
| `cp` | cp -a a 400-file tree | 106.84 ± 5.22 | 115.51 ± 5.02 | 116.84 ± 3.19 | 204.36 ± 29.64 | 1.09× | 1.91× | 1.01× | 1.77× | 0.92× |
| `csplit` | csplit at line 4000000 of a 62 MiB file | 66.84 ± 10.44 | 50.88 ± 5.21 | 305.78 ± 33.56 | 302.05 ± 36.28 | 4.58× | 4.52× | 6.01× | 5.94× | 1.31× |
| `csplit` | csplit at /4000000/ of a 62 MiB file | 59.03 ± 7.24 | 52.02 ± 8.77 | 508.02 ± 10.51 | 312.63 ± 17.62 | 8.61× | 5.30× | 9.77× | 6.01× | 1.13× |
| `csplit` | csplit at /^4000000$/ of a 62 MiB file | 65.20 ± 14.37 | 48.77 ± 4.31 | 497.46 ± 20.79 | 296.68 ± 5.29 | 7.63× | 4.55× | 10.20× | 6.08× | 1.34× |
| `csplit` | csplit at a never-matching regexp | 80.58 ± 8.01 | 86.33 ± 10.79 | 936.01 ± 234.61 | 336.11 ± 29.68 | 11.62× | 4.17× | 10.84× | 3.89× | 0.93× |
| `csplit` | csplit into 80 pieces of a 62 MiB file | 123.09 ± 10.95 | 94.61 ± 5.54 | 348.15 ± 44.61 | 340.83 ± 34.29 | 2.83× | 2.77× | 3.68× | 3.60× | 1.30× |
| `csplit` | csplit at a literal-prefixed class | 59.75 ± 6.06 | 50.50 ± 5.15 | 683.74 ± 17.60 | 304.16 ± 4.75 | 11.44× | 5.09× | 13.54× | 6.02× | 1.18× |
| `csplit` | csplit at an alternation | 98.69 ± 6.19 | 87.36 ± 20.29 | 515.82 ± 20.41 | exit 1 | 5.23× | — | 5.90× | — | 1.13× |
| `cut` | cut -f2 -d, of a 90 MiB table | 64.19 ± 15.14 | 44.36 ± 2.27 | 90.05 ± 11.34 | 40.08 ± 2.96 | 1.40× | 0.62× | 2.03× | 0.90× | 1.45× |
| `cut` | cut -f1,3-5 -d, of a 90 MiB table | 94.98 ± 1.70 | 79.37 ± 10.86 | 264.78 ± 33.60 | 87.73 ± 17.90 | 2.79× | 0.92× | 3.34× | 1.11× | 1.20× |
| `cut` | cut --complement -f2 -d, of a 90 MiB table | 146.93 ± 69.40 | 65.12 ± 1.96 | 262.98 ± 44.65 | 70.71 ± 11.91 | 1.79× | 0.48× | 4.04× | 1.09× | 2.26× |
| `cut` | cut -s -f4 -d, of a 90 MiB table | 80.94 ± 1.42 | 57.79 ± 1.33 | 121.69 ± 17.23 | 57.82 ± 1.96 | 1.50× | 0.71× | 2.11× | 1.00× | 1.40× |
| `cut` | cut -c1-10 of a 90 MiB table | 38.36 ± 0.80 | 30.36 ± 0.70 | 318.67 ± 7.03 | 31.33 ± 0.87 | 8.31× | 0.82× | 10.50× | 1.03× | 1.26× |
| `cut` | cut -f2 -d, from a pipe | 65.27 ± 2.42 | 43.93 ± 1.55 | 88.52 ± 1.47 | 45.48 ± 3.51 | 1.36× | 0.70× | 2.01× | 1.04× | 1.49× |
| `date` | date | 1.49 ± 1.12 | 1.73 ± 0.81 | 4.53 ± 5.52 | 8.82 ± 5.37 | 3.03× | 5.91× | 2.62× | 5.11× | 0.86× |
| `date` | date -d fixed | 1.15 ± 1.41 | 0.47 ± 2.75 | 0.18 ± 0.32 | 4.16 ± 5.80 | 0.15× | 3.62× | 0.38× | 8.86× | 2.45× |
| `date` | date -d relative | 1.11 ± 1.98 | 0.10 ± 0.27 | 3.51 ± 3.45 | 4.21 ± 1.26 | 3.17× | 3.80× | 36.91× | 44.30× | 11.66× |
| `date` | date every conversion | 0.10 ± 0.83 | 0.14 ± 1.62 | 0.05 ± 0.22 | 1.78 ± 1.04 | 0.47× | 17.70× | 0.35× | 13.10× | 0.74× |
| `date` | date -f 10000 lines | 39.65 ± 0.81 | 29.30 ± 0.74 | 24.39 ± 4.88 | 53.94 ± 0.87 | 0.62× | 1.36× | 0.83× | 1.84× | 1.35× |
| `date` | date -u -R | 2.26 ± 0.66 | 2.06 ± 1.04 | 2.63 ± 3.29 | 5.37 ± 0.93 | 1.16× | 2.37× | 1.28× | 2.60× | 1.10× |
| `date` | date --debug | 1.81 ± 0.38 | 1.74 ± 0.42 | 2.61 ± 0.41 | 5.99 ± 2.00 | 1.44× | 3.31× | 1.50× | 3.45× | 1.04× |
| `dd` | dd one small copy | 0.92 ± 0.82 | 1.63 ± 1.63 | 2.04 ± 0.86 | 2.75 ± 1.39 | 2.21× | 2.98× | 1.25× | 1.69× | 0.57× |
| `dd` | dd 8MiB bs=512 | 35.60 ± 8.41 | 31.73 ± 1.72 | 29.80 ± 2.32 | 33.97 ± 7.98 | 0.84× | 0.95× | 0.94× | 1.07× | 1.12× |
| `dd` | dd 8MiB bs=4096 | 9.63 ± 4.33 | 7.82 ± 3.06 | 7.56 ± 1.57 | 10.23 ± 2.08 | 0.78× | 1.06× | 0.97× | 1.31× | 1.23× |
| `dd` | dd 8MiB bs=64k | 4.11 ± 2.36 | 4.65 ± 2.36 | 5.38 ± 1.92 | 6.37 ± 1.44 | 1.31× | 1.55× | 1.16× | 1.37× | 0.88× |
| `dd` | dd 8MiB bs=1M | 4.59 ± 2.36 | 5.06 ± 2.39 | 4.61 ± 1.27 | 7.25 ± 1.75 | 1.01× | 1.58× | 0.91× | 1.43× | 0.91× |
| `dd` | dd 8MiB default block | 32.18 ± 1.30 | 31.40 ± 2.71 | 31.94 ± 8.16 | 33.17 ± 1.34 | 0.99× | 1.03× | 1.02× | 1.06× | 1.02× |
| `dd` | dd 8MiB ibs 4k obs 64k | 5.77 ± 4.29 | 5.81 ± 1.90 | 6.72 ± 2.80 | 7.56 ± 2.35 | 1.16× | 1.31× | 1.16× | 1.30× | 0.99× |
| `dd` | dd 8MiB conv=swab | 22.48 ± 2.39 | 14.46 ± 3.19 | 5.61 ± 1.46 | 7.70 ± 2.17 | 0.25× | 0.34× | 0.39× | 0.53× | 1.55× |
| `dd` | dd 8MiB conv=ucase | 6.81 ± 2.62 | 6.64 ± 1.76 | 5.93 ± 1.54 | 8.24 ± 2.26 | 0.87× | 1.21× | 0.89× | 1.24× | 1.03× |
| `dd` | dd 8MiB conv=sync | 4.76 ± 2.91 | 4.59 ± 1.82 | 4.90 ± 1.75 | 7.14 ± 2.43 | 1.03× | 1.50× | 1.07× | 1.55× | 1.04× |
| `dd` | dd conv=block cbs=16 | 24.29 ± 2.34 | 25.53 ± 4.63 | 11.25 ± 2.59 | 26.77 ± 1.75 | 0.46× | 1.10× | 0.44× | 1.05× | 0.95× |
| `dd` | dd conv=unblock cbs=16 | 34.85 ± 4.14 | 31.82 ± 5.02 | 12.17 ± 1.53 | 9.27 ± 1.90 | 0.35× | 0.27× | 0.38× | 0.29× | 1.10× |
| `dd` | dd 8MiB skip and seek | 7.09 ± 2.58 | 6.11 ± 2.78 | 7.14 ± 3.78 | 9.61 ± 2.83 | 1.01× | 1.35× | 1.17× | 1.57× | 1.16× |
| `dd` | dd 64MiB zero to null bs=512 | 100.09 ± 1.65 | 96.92 ± 8.60 | 87.24 ± 1.50 | 89.49 ± 4.88 | 0.87× | 0.89× | 0.90× | 0.92× | 1.03× |
| `dd` | dd 64MiB zero to null bs=64k | 4.77 ± 2.40 | 5.15 ± 1.86 | 3.48 ± 1.43 | 5.96 ± 4.82 | 0.73× | 1.25× | 0.68× | 1.16× | 0.93× |
| `dd` | dd 8MiB from a pipe | 5.05 ± 2.17 | 3.95 ± 1.81 | 4.21 ± 1.99 | 6.69 ± 2.42 | 0.83× | 1.32× | 1.07× | 1.69× | 1.28× |
| `dd` | dd 8MiB from a pipe fullblock | 5.56 ± 2.06 | 5.79 ± 1.51 | 6.74 ± 2.47 | 8.14 ± 2.28 | 1.21× | 1.47× | 1.17× | 1.41× | 0.96× |
| `df` | one operand | exit 1 | exit 1 | 1.90 ± 0.16 | 4.02 ± 0.33 | — | — | — | — | — |
| `df` | one operand, human-readable | exit 1 | exit 1 | 1.88 ± 0.87 | 3.97 ± 0.28 | — | — | — | — | — |
| `df` | one operand, inodes | exit 1 | exit 1 | 1.81 ± 0.17 | 4.06 ± 1.78 | — | — | — | — | — |
| `df` | a pseudo-filesystem operand | 1.46 ± 0.12 | 1.37 ± 0.10 | 1.88 ± 0.56 | 3.87 ± 0.41 | 1.29× | 2.65× | 1.37× | 2.82× | 1.06× |
| `df` | eight operands | 1.41 ± 0.53 | 1.39 ± 0.12 | 2.18 ± 0.63 | 4.24 ± 0.89 | 1.54× | 3.00× | 1.57× | 3.05× | 1.02× |
| `df` | the whole table | exit 1 | exit 1 | 1.91 ± 0.18 | 3.88 ± 0.40 | — | — | — | — | — |
| `df` | the whole table, -a | exit 1 | exit 1 | 1.89 ± 0.64 | 3.80 ± 0.30 | — | — | — | — | — |
| `df` | the whole table, -T --total | exit 1 | exit 1 | 1.82 ± 0.54 | 3.83 ± 0.49 | — | — | — | — | — |
| `df` | the whole table, --output | exit 1 | exit 1 | 1.86 ± 0.17 | 3.96 ± 0.26 | — | — | — | — | — |
| `df` | the whole table, -h | exit 1 | exit 1 | 1.90 ± 0.23 | 4.35 ± 1.16 | — | — | — | — | — |
| `dir` | 4000 names, no stat | 6.54 ± 1.40 | 5.54 ± 1.18 | 11.18 ± 1.41 | 10.73 ± 0.50 | 1.71× | 1.64× | 2.02× | 1.94× | 1.18× |
| `dir` | -l over 4000 names | 18.08 ± 0.34 | 16.39 ± 1.53 | 22.37 ± 0.38 | 19.45 ± 0.45 | 1.24× | 1.08× | 1.37× | 1.19× | 1.10× |
| `dir` | -U (unsorted) over 4000 names | 5.46 ± 0.34 | 4.33 ± 0.85 | 3.95 ± 0.95 | 10.08 ± 0.38 | 0.72× | 1.85× | 0.91× | 2.33× | 1.26× |
| `dir` | -v (filevercmp) over 4000 names | 10.64 ± 0.39 | 7.94 ± 0.29 | 7.43 ± 9.05 | 35.23 ± 0.67 | 0.70× | 3.31× | 0.94× | 4.44× | 1.34× |
| `dir` | -t over 4000 names | 14.67 ± 0.67 | 13.76 ± 0.80 | 12.03 ± 0.95 | 16.39 ± 0.71 | 0.82× | 1.12× | 0.87× | 1.19× | 1.07× |
| `dir` | -S over 4000 names | 14.88 ± 0.43 | 13.96 ± 0.37 | 18.70 ± 0.44 | 15.80 ± 0.53 | 1.26× | 1.06× | 1.34× | 1.13× | 1.07× |
| `dir` | -C -w 200 over 4000 names | 6.78 ± 0.29 | 5.72 ± 0.59 | 11.36 ± 1.61 | 11.85 ± 2.11 | 1.67× | 1.75× | 1.99× | 2.07× | 1.19× |
| `dir` | -x -w 200 over 4000 names | 6.68 ± 0.93 | 5.85 ± 1.39 | 11.01 ± 0.35 | 10.89 ± 0.34 | 1.65× | 1.63× | 1.88× | 1.86× | 1.14× |
| `dir` | -m -w 200 over 4000 names | 6.89 ± 1.18 | 5.49 ± 1.00 | 10.73 ± 0.41 | 10.45 ± 0.35 | 1.56× | 1.52× | 1.96× | 1.90× | 1.26× |
| `dir` | -i -s over 4000 names | 16.83 ± 0.42 | 14.56 ± 0.34 | 19.33 ± 1.74 | 17.33 ± 0.76 | 1.15× | 1.03× | 1.33× | 1.19× | 1.16× |
| `dir` | -F over 1500 mixed entries | 7.86 ± 0.32 | 7.49 ± 0.35 | 5.15 ± 0.99 | 7.95 ± 1.31 | 0.66× | 1.01× | 0.69× | 1.06× | 1.05× |
| `dir` | -l over 1500 mixed entries | 9.07 ± 0.46 | 8.23 ± 0.55 | 10.53 ± 2.56 | 11.37 ± 0.43 | 1.16× | 1.25× | 1.28× | 1.38× | 1.10× |
| `dir` | --color=always over 1500 mixed | 6.99 ± 1.13 | 6.57 ± 0.33 | 6.07 ± 0.44 | 7.63 ± 0.44 | 0.87× | 1.09× | 0.92× | 1.16× | 1.06× |
| `dir` | -R over a 40-deep tree | 5.49 ± 1.60 | 4.80 ± 0.27 | 3.33 ± 0.15 | 6.55 ± 1.92 | 0.61× | 1.19× | 0.70× | 1.37× | 1.15× |
| `dir` | -lR over a 40-deep tree | 6.58 ± 0.41 | 6.23 ± 0.34 | 6.97 ± 0.70 | 9.49 ± 0.45 | 1.06× | 1.44× | 1.12× | 1.52× | 1.06× |
| `dir` | -b over 4000 names | 6.55 ± 0.36 | 5.50 ± 0.38 | 11.79 ± 2.02 | 10.88 ± 0.39 | 1.80× | 1.66× | 2.14× | 1.98× | 1.19× |
| `dir` | --quoting-style=shell-escape | 7.84 ± 1.41 | 6.50 ± 1.17 | 10.93 ± 0.33 | 11.17 ± 0.30 | 1.39× | 1.42× | 1.68× | 1.72× | 1.21× |
| `dir` | --time-style=full-iso -l | 18.93 ± 0.96 | 16.54 ± 0.58 | 23.93 ± 4.49 | 19.31 ± 0.63 | 1.26× | 1.02× | 1.45× | 1.17× | 1.14× |
| `dircolors` | dircolors | 1.42 ± 0.91 | 1.41 ± 1.53 | 1.24 ± 1.02 | 4.76 ± 5.61 | 0.87× | 3.34× | 0.88× | 3.37× | 1.01× |
| `dircolors` | dircolors -p | 2.06 ± 0.74 | 1.72 ± 0.65 | 2.07 ± 2.38 | 4.57 ± 0.84 | 1.00× | 2.22× | 1.20× | 2.66× | 1.20× |
| `dircolors` | dircolors --print-ls-colors | 1.59 ± 0.56 | 1.69 ± 1.86 | 1.82 ± 0.49 | 4.66 ± 0.86 | 1.14× | 2.93× | 1.08× | 2.76× | 0.94× |
| `dircolors` | dircolors a 200k-entry config | 23.18 ± 4.07 | 16.45 ± 1.06 | 24.19 ± 3.52 | 50.06 ± 1.47 | 1.04× | 2.16× | 1.47× | 3.04× | 1.41× |
| `dircolors` | dircolors --print-ls-colors a 200k-entry config | 18.79 ± 0.70 | 17.75 ± 5.64 | 27.08 ± 0.94 | 53.80 ± 1.92 | 1.44× | 2.86× | 1.53× | 3.03× | 1.06× |
| `dirname` | dirname one path | 2.24 ± 0.77 | 1.41 ± 0.35 | 1.65 ± 1.78 | 3.27 ± 0.11 | 0.74× | 1.46× | 1.17× | 2.32× | 1.59× |
| `dirname` | dirname 200 operands | 1.45 ± 0.07 | 1.43 ± 0.47 | 2.21 ± 3.12 | 3.72 ± 1.37 | 1.53× | 2.57× | 1.55× | 2.60× | 1.01× |
| `du` | 4000 files in one directory | 10.28 ± 0.45 | 11.37 ± 2.90 | 6.77 ± 0.46 | 9.41 ± 1.68 | 0.66× | 0.92× | 0.60× | 0.83× | 0.90× |
| `du` | -a over 4000 files | 10.75 ± 0.58 | 9.88 ± 0.49 | 8.33 ± 0.46 | 11.40 ± 1.49 | 0.77× | 1.06× | 0.84× | 1.15× | 1.09× |
| `du` | a 60-deep tree | 6.11 ± 2.17 | 6.27 ± 1.14 | 4.50 ± 0.19 | 7.11 ± 1.76 | 0.74× | 1.16× | 0.72× | 1.13× | 0.98× |
| `du` | -a over a 60-deep tree | 5.88 ± 0.24 | 5.85 ± 0.36 | 5.13 ± 0.31 | 7.61 ± 0.31 | 0.87× | 1.29× | 0.88× | 1.30× | 1.01× |
| `du` | --apparent-size of the whole tree | 15.10 ± 0.75 | 15.37 ± 3.03 | 14.18 ± 9.13 | 16.09 ± 1.16 | 0.94× | 1.07× | 0.92× | 1.05× | 0.98× |
| `du` | -h -c of two trees | 18.03 ± 0.41 | 17.22 ± 0.52 | 12.57 ± 0.53 | 16.01 ± 2.23 | 0.70× | 0.89× | 0.73× | 0.93× | 1.05× |
| `du` | --inodes of the whole tree | 17.47 ± 0.84 | 17.04 ± 0.79 | 11.73 ± 0.68 | 14.99 ± 1.66 | 0.67× | 0.86× | 0.69× | 0.88× | 1.03× |
| `du` | -l (no seen set) over 4000 files | 10.85 ± 0.93 | 10.44 ± 0.90 | 7.76 ± 0.58 | 10.93 ± 0.60 | 0.72× | 1.01× | 0.74× | 1.05× | 1.04× |
| `du` | du of one small directory | 7.07 ± 1.73 | 6.94 ± 1.12 | 5.11 ± 0.52 | 8.54 ± 2.26 | 0.72× | 1.21× | 0.74× | 1.23× | 1.02× |
| `echo` | echo hello world | 1.41 ± 0.10 | 1.35 ± 0.45 | 1.70 ± 0.28 | 3.57 ± 0.32 | 1.21× | 2.53× | 1.27× | 2.65× | 1.05× |
| `echo` | echo -e with escapes | 1.32 ± 0.10 | 1.32 ± 0.08 | 1.72 ± 0.12 | 3.23 ± 0.35 | 1.30× | 2.45× | 1.30× | 2.44× | 1.00× |
| `echo` | echo 200 operands | 1.33 ± 0.08 | 1.43 ± 0.56 | 1.73 ± 0.07 | 3.24 ± 0.78 | 1.30× | 2.43× | 1.21× | 2.26× | 0.93× |
| `env` | env dump the inherited environment | 1.55 ± 0.79 | 2.70 ± 0.67 | 2.01 ± 0.75 | 4.31 ± 0.86 | 1.29× | 2.78× | 0.75× | 1.60× | 0.58× |
| `env` | env dump with 60 assignments | 2.77 ± 0.99 | 1.54 ± 2.16 | 2.10 ± 0.91 | 4.98 ± 0.79 | 0.76× | 1.80× | 1.36× | 3.22× | 1.79× |
| `env` | env -i dump with 60 assignments | 1.58 ± 1.35 | 1.64 ± 2.03 | 2.97 ± 1.51 | 4.37 ± 1.00 | 1.88× | 2.76× | 1.81× | 2.67× | 0.97× |
| `env` | env -0 dump with 60 assignments | 1.92 ± 0.80 | 2.76 ± 0.74 | 2.84 ± 3.27 | 4.04 ± 0.76 | 1.48× | 2.10× | 1.03× | 1.46× | 0.69× |
| `env` | env -u 60 names off a 60-entry vector | 2.50 ± 0.83 | 2.56 ± 1.43 | 2.13 ± 0.76 | 6.03 ± 4.53 | 0.85× | 2.42× | 0.83× | 2.35× | 0.97× |
| `env` | env exec true | 3.32 ± 1.46 | 2.72 ± 0.88 | 3.81 ± 0.90 | 6.24 ± 3.71 | 1.15× | 1.88× | 1.40× | 2.30× | 1.22× |
| `env` | env -i exec true | 3.47 ± 2.39 | 3.81 ± 0.95 | 4.30 ± 1.31 | 5.59 ± 1.61 | 1.24× | 1.61× | 1.13× | 1.47× | 0.91× |
| `env` | env 60 assignments then exec true | 4.41 ± 2.33 | 4.12 ± 0.97 | 3.56 ± 0.77 | 6.59 ± 1.03 | 0.81× | 1.50× | 0.86× | 1.60× | 1.07× |
| `env` | env -v exec true | 3.24 ± 2.24 | 3.08 ± 0.69 | 3.81 ± 0.49 | 6.71 ± 0.70 | 1.18× | 2.07× | 1.24× | 2.18× | 1.05× |
| `env` | env -v 60 assignments then exec true | 3.73 ± 1.23 | 3.73 ± 2.30 | 4.01 ± 0.47 | 6.43 ± 0.75 | 1.08× | 1.72× | 1.08× | 1.72× | 1.00× |
| `env` | env PATH search for a bare name | 3.14 ± 1.09 | 4.11 ± 0.69 | 5.15 ± 3.97 | 5.87 ± 1.10 | 1.64× | 1.87× | 1.26× | 1.43× | 0.77× |
| `env` | env -S split 40 tokens | 3.81 ± 0.77 | 3.29 ± 1.60 | 3.00 ± 0.89 | 6.31 ± 3.76 | 0.79× | 1.66× | 0.91× | 1.92× | 1.16× |
| `env` | env -S split with expansions | 3.41 ± 0.92 | 3.03 ± 0.88 | 4.17 ± 0.89 | 6.61 ± 3.78 | 1.22× | 1.94× | 1.38× | 2.18× | 1.13× |
| `env` | env -S split a quoted string | 3.74 ± 2.43 | 4.34 ± 0.78 | 4.53 ± 2.80 | 7.32 ± 4.45 | 1.21× | 1.96× | 1.04× | 1.69× | 0.86× |
| `env` | env --block-signal all signals | 2.69 ± 0.81 | 2.66 ± 2.05 | 2.42 ± 2.83 | 4.79 ± 0.93 | 0.90× | 1.78× | 0.91× | 1.80× | 1.01× |
| `env` | env --ignore-signal=INT,TERM,HUP | 3.91 ± 2.27 | 3.06 ± 1.16 | 3.96 ± 1.01 | 6.54 ± 0.71 | 1.01× | 1.67× | 1.29× | 2.14× | 1.28× |
| `env` | env --list-signal-handling with three set | 3.53 ± 0.87 | 3.70 ± 2.39 | 4.08 ± 0.52 | 6.57 ± 0.72 | 1.16× | 1.86× | 1.10× | 1.77× | 0.95× |
| `env` | env -C then exec | 4.06 ± 0.95 | 4.72 ± 3.22 | 3.66 ± 0.95 | 6.55 ± 2.24 | 0.90× | 1.61× | 0.77× | 1.39× | 0.86× |
| `expand` | expand a 45 MiB tabbed file | 80.10 ± 3.85 | 60.66 ± 1.10 | 947.03 ± 19.50 | 193.03 ± 17.24 | 11.82× | 2.41× | 15.61× | 3.18× | 1.32× |
| `expand` | expand -t4 a 45 MiB tabbed file | 80.76 ± 1.31 | 61.05 ± 4.79 | 951.85 ± 34.30 | 181.70 ± 4.98 | 11.79× | 2.25× | 15.59× | 2.98× | 1.32× |
| `expand` | expand -i a 45 MiB tabbed file | 48.02 ± 1.44 | 40.39 ± 4.28 | 886.02 ± 7.27 | 173.58 ± 1.89 | 18.45× | 3.61× | 21.93× | 4.30× | 1.19× |
| `expand` | expand -t 4,8,16 a 45 MiB tabbed file | 109.69 ± 1.08 | 79.87 ± 1.75 | 954.51 ± 41.98 | 188.34 ± 5.57 | 8.70× | 1.72× | 11.95× | 2.36× | 1.37× |
| `expand` | expand a 42 MiB file with no tabs | 34.10 ± 8.01 | 29.75 ± 0.79 | 1055.72 ± 26.25 | 41.92 ± 0.87 | 30.96× | 1.23× | 35.49× | 1.41× | 1.15× |
| `expr` | expr 1 + 1 | 1.58 ± 0.45 | 1.49 ± 0.08 | 1.97 ± 0.31 | 3.65 ± 0.22 | 1.25× | 2.31× | 1.33× | 2.46× | 1.06× |
| `expr` | expr 40-digit product | 1.48 ± 0.13 | 1.46 ± 0.10 | 1.93 ± 0.58 | 3.51 ± 0.25 | 1.31× | 2.38× | 1.32× | 2.41× | 1.01× |
| `expr` | expr 400-digit product | 1.56 ± 0.16 | 1.58 ± 0.18 | 1.90 ± 0.13 | 4.55 ± 2.16 | 1.22× | 2.92× | 1.20× | 2.89× | 0.99× |
| `expr` | expr : extracts a suffix | 5.01 ± 1.39 | 3.07 ± 1.68 | 1.94 ± 0.15 | 4.05 ± 1.02 | 0.39× | 0.81× | 0.63× | 1.32× | 1.64× |
| `expr` | expr : counts a 4000-byte match | 1.99 ± 0.57 | 1.88 ± 0.41 | 2.53 ± 1.08 | 3.95 ± 0.88 | 1.27× | 1.98× | 1.35× | 2.10× | 1.06× |
| `expr` | expr : anchored class over 4000 bytes | 3.02 ± 0.23 | 2.34 ± 0.10 | 1.97 ± 0.11 | 3.43 ± 0.74 | 0.65× | 1.14× | 0.84× | 1.46× | 1.29× |
| `expr` | expr length of 100000 bytes | 1.49 ± 0.14 | 1.62 ± 0.26 | 2.73 ± 0.90 | 3.95 ± 1.00 | 1.83× | 2.65× | 1.69× | 2.44× | 0.92× |
| `factor` | factor 1..200000 from stdin | 29.45 ± 7.78 | 19.88 ± 6.28 | 27.21 ± 10.05 | 178.38 ± 4.29 | 0.92× | 6.06× | 1.37× | 8.97× | 1.48× |
| `factor` | factor 55 64-bit semiprimes | 170.97 ± 1.68 | 50.52 ± 0.95 | 29.20 ± 8.73 | 4.91 ± 0.89 | 0.17× | 0.03× | 0.58× | 0.10× | 3.38× |
| `factor` | factor one 64-bit semiprime | 1.49 ± 0.11 | 1.40 ± 0.09 | 1.81 ± 0.09 | 3.62 ± 0.67 | 1.22× | 2.44× | 1.29× | 2.58× | 1.06× |
| `false` | false | 1.60 ± 0.69 | 1.76 ± 1.01 | 1.53 ± 0.25 | 3.82 ± 0.33 | 0.95× | 2.38× | 0.87× | 2.17× | 0.91× |
| `fmt` | fmt (default) of a 40 MiB file | 718.92 ± 59.51 | 432.78 ± 20.90 | 330.50 ± 10.25 | 344.80 ± 20.25 | 0.46× | 0.48× | 0.76× | 0.80× | 1.66× |
| `fmt` | fmt -w 40 of a 40 MiB file | 738.48 ± 5.69 | 446.36 ± 22.33 | 312.49 ± 5.76 | 399.41 ± 23.36 | 0.42× | 0.54× | 0.70× | 0.89× | 1.65× |
| `fmt` | fmt -s -w 40 of a 40 MiB file | 728.63 ± 26.48 | 438.13 ± 24.42 | 307.93 ± 15.89 | 394.83 ± 4.67 | 0.42× | 0.54× | 0.70× | 0.90× | 1.66× |
| `fmt` | fmt -u -w 40 of a 40 MiB file | 723.27 ± 14.25 | 435.53 ± 27.64 | 304.10 ± 15.06 | 358.68 ± 10.53 | 0.42× | 0.50× | 0.70× | 0.82× | 1.66× |
| `fmt` | fmt (default) of a 38 MiB wrapped file | 773.85 ± 35.13 | 460.14 ± 24.53 | 364.01 ± 18.80 | 372.42 ± 16.94 | 0.47× | 0.48× | 0.79× | 0.81× | 1.68× |
| `fmt` | fmt -c -w 60 of a 38 MiB wrapped file | 742.53 ± 6.35 | 471.62 ± 34.74 | 334.05 ± 1.78 | 400.31 ± 30.94 | 0.45× | 0.54× | 0.71× | 0.85× | 1.57× |
| `fmt` | fmt -p "" -w 40 of a 38 MiB wrapped file | 767.33 ± 29.10 | 444.76 ± 3.17 | 309.94 ± 11.01 | 386.97 ± 3.34 | 0.40× | 0.50× | 0.70× | 0.87× | 1.73× |
| `fmt` | fmt (default) from a pipe | 700.72 ± 24.57 | 429.02 ± 4.44 | 346.14 ± 25.24 | 377.97 ± 17.51 | 0.49× | 0.54× | 0.81× | 0.88× | 1.63× |
| `fmt` | fmt (default) of one 200k-line paragraph | 90.66 ± 2.64 | 58.60 ± 1.06 | 41.12 ± 1.49 | 71.02 ± 1.68 | 0.45× | 0.78× | 0.70× | 1.21× | 1.55× |
| `fold` | fold -w40 of a 68 MiB file | 41.34 ± 1.05 | 37.66 ± 1.02 | 530.18 ± 21.00 | 91.06 ± 2.13 | 12.83× | 2.20× | 14.08× | 2.42× | 1.10× |
| `fold` | fold -b -w40 of a 68 MiB file | 37.53 ± 1.01 | 36.28 ± 6.28 | 392.58 ± 21.24 | 61.30 ± 9.92 | 10.46× | 1.63× | 10.82× | 1.69× | 1.03× |
| `fold` | fold -s -w40 of a 68 MiB file | 45.57 ± 4.21 | 41.54 ± 8.54 | 758.50 ± 83.07 | 98.42 ± 3.44 | 16.64× | 2.16× | 18.26× | 2.37× | 1.10× |
| `fold` | fold (default 80) of a 68 MiB file | 34.48 ± 2.96 | 33.87 ± 1.15 | 493.92 ± 25.33 | 77.29 ± 1.02 | 14.33× | 2.24× | 14.58× | 2.28× | 1.02× |
| `groups` | groups | exit 1 | exit 1 | 3.43 ± 0.16 | 5.48 ± 1.08 | — | — | — | — | — |
| `groups` | groups root | 1.45 ± 0.45 | 1.46 ± 0.47 | 4.27 ± 0.19 | 6.03 ± 0.28 | 2.94× | 4.15× | 2.92× | 4.13× | 1.00× |
| `head` | head -n 10 of a 62 MiB file | 1.83 ± 0.76 | 1.78 ± 0.84 | 2.60 ± 1.74 | 3.90 ± 0.92 | 1.42× | 2.13× | 1.46× | 2.19× | 1.03× |
| `head` | head -n 4000000 of a 62 MiB file | 14.15 ± 0.60 | 5.56 ± 0.58 | 36.77 ± 2.53 | 15.53 ± 2.33 | 2.60× | 1.10× | 6.61× | 2.79× | 2.54× |
| `head` | head -c 32M of a 62 MiB file | 14.50 ± 0.58 | 5.02 ± 2.33 | 18.25 ± 2.35 | 9.80 ± 0.69 | 1.26× | 0.68× | 3.64× | 1.95× | 2.89× |
| `head` | head -n 10 from a pipe | 2.57 ± 2.64 | 3.37 ± 1.45 | 3.69 ± 1.16 | 4.64 ± 0.90 | 1.44× | 1.81× | 1.10× | 1.38× | 0.76× |
| `head` | head -n -10 of a 62 MiB file | 24.30 ± 0.77 | 6.98 ± 1.91 | 31.35 ± 0.69 | 14.21 ± 0.49 | 1.29× | 0.58× | 4.49× | 2.04× | 3.48× |
| `head` | head -c -10 of a 62 MiB file | 24.43 ± 0.89 | 6.67 ± 0.44 | 30.83 ± 4.60 | 14.24 ± 0.65 | 1.26× | 0.58× | 4.62× | 2.13× | 3.66× |
| `head` | head -n -10 from a pipe | 32.47 ± 0.98 | 19.74 ± 2.38 | 82.58 ± 15.96 | 18.96 ± 1.55 | 2.54× | 0.58× | 4.18× | 0.96× | 1.64× |
| `head` | head -c -10 from a pipe | 29.32 ± 1.39 | 17.03 ± 1.33 | 39.43 ± 1.20 | 18.00 ± 5.56 | 1.34× | 0.61× | 2.32× | 1.06× | 1.72× |
| `hostid` | hostid | 23.07 ± 6.06 | 29.45 ± 3.02 | 1.73 ± 0.50 | 4.24 ± 1.03 | 0.08× | 0.18× | 0.06× | 0.14× | 0.78× |
| `id` | id -u | 1.85 ± 0.29 | 1.50 ± 0.74 | 1.82 ± 0.15 | 3.62 ± 0.22 | 0.98× | 1.96× | 1.21× | 2.41× | 1.23× |
| `id` | id -un | 2.21 ± 0.15 | 2.30 ± 0.69 | 2.60 ± 0.90 | 4.35 ± 0.26 | 1.18× | 1.97× | 1.13× | 1.89× | 0.96× |
| `id` | id | 2.30 ± 0.39 | 2.12 ± 0.63 | 3.40 ± 0.20 | 5.49 ± 1.33 | 1.48× | 2.38× | 1.60× | 2.59× | 1.09× |
| `id` | id -G | 1.37 ± 0.15 | 1.30 ± 0.09 | 1.70 ± 0.08 | 3.38 ± 0.10 | 1.23× | 2.46× | 1.31× | 2.60× | 1.06× |
| `id` | id root | 1.57 ± 0.42 | 1.51 ± 0.09 | 4.20 ± 0.12 | 6.04 ± 1.14 | 2.67× | 3.85× | 2.78× | 3.99× | 1.04× |
| `install` | install one file | 0.76 ± 0.64 | 2.09 ± 1.77 | 2.21 ± 1.80 | 4.33 ± 3.19 | 2.92× | 5.70× | 1.06× | 2.07× | 0.36× |
| `install` | install 200 files | 380.71 ± 35.77 | 377.34 ± 48.30 | 467.29 ± 36.70 | 980.68 ± 107.93 | 1.23× | 2.58× | 1.24× | 2.60× | 1.01× |
| `install` | install -m 200 files | 403.33 ± 17.03 | 402.26 ± 12.71 | 486.51 ± 18.70 | 1031.45 ± 138.65 | 1.21× | 2.56× | 1.21× | 2.56× | 1.00× |
| `install` | install 200 over existing | 385.11 ± 18.43 | 379.17 ± 16.27 | 477.09 ± 23.85 | 937.94 ± 72.89 | 1.24× | 2.44× | 1.26× | 2.47× | 1.02× |
| `install` | install 200 into a directory | 42.66 ± 2.11 | 41.66 ± 1.49 | 41.97 ± 6.51 | 48.45 ± 2.34 | 0.98× | 1.14× | 1.01× | 1.16× | 1.02× |
| `install` | install -d 200 directories | exit 1 | exit 1 | 454.20 ± 15.88 | 968.71 ± 120.68 | — | — | — | — | — |
| `install` | install -d 200 nested | exit 1 | exit 1 | 510.44 ± 13.63 | 969.39 ± 33.39 | — | — | — | — | — |
| `install` | install -D 200 files | exit 1 | exit 1 | 493.38 ± 14.77 | 937.76 ± 25.69 | — | — | — | — | — |
| `install` | install -C 200 matching | 754.18 ± 18.66 | 781.44 ± 38.38 | 849.42 ± 23.47 | 1296.89 ± 59.55 | 1.13× | 1.72× | 1.09× | 1.66× | 0.97× |
| `install` | install -C 200 differing | 367.28 ± 15.83 | 368.02 ± 16.27 | 470.92 ± 16.95 | 970.09 ± 98.68 | 1.28× | 2.64× | 1.28× | 2.64× | 1.00× |
| `install` | install -s 200 files | 400.35 ± 14.75 | 397.64 ± 13.29 | 518.49 ± 14.37 | 897.21 ± 21.18 | 1.30× | 2.24× | 1.30× | 2.26× | 1.01× |
| `install` | install 64 MiB | 58.54 ± 32.98 | 41.06 ± 19.66 | 42.41 ± 30.85 | 66.48 ± 5.58 | 0.72× | 1.14× | 1.03× | 1.62× | 1.43× |
| `join` | join two 1M-line files | 273.80 ± 31.79 | 146.05 ± 2.06 | 719.85 ± 29.27 | 427.94 ± 23.55 | 2.63× | 1.56× | 4.93× | 2.93× | 1.87× |
| `join` | join with half unpairable | 248.09 ± 25.27 | 138.53 ± 16.00 | 855.04 ± 28.87 | 376.53 ± 10.24 | 3.45× | 1.52× | 6.17× | 2.72× | 1.79× |
| `join` | join -a1 -a2 two 1M-line files | 287.56 ± 1.69 | 161.73 ± 5.47 | 973.48 ± 40.78 | 394.35 ± 25.70 | 3.39× | 1.37× | 6.02× | 2.44× | 1.78× |
| `join` | join -o 0,1.2,2.3 two 1M-line files | 244.32 ± 1.48 | 135.50 ± 5.07 | 625.77 ± 9.88 | 419.48 ± 11.12 | 2.56× | 1.72× | 4.62× | 3.10× | 1.80× |
| `join` | join -v1 two 1M-line files | 226.60 ± 15.86 | 127.45 ± 1.73 | 812.06 ± 20.44 | 372.95 ± 20.77 | 3.58× | 1.65× | 6.37× | 2.93× | 1.78× |
| `link` | link one hard link | 4.23 ± 0.72 | 3.14 ± 1.06 | 3.25 ± 0.78 | 6.51 ± 0.65 | 0.77× | 1.54× | 1.03× | 2.07× | 1.35× |
| `link` | link 200 hard links | 384.92 ± 15.73 | 383.56 ± 15.31 | 589.85 ± 178.57 | 947.05 ± 38.51 | 1.53× | 2.46× | 1.54× | 2.47× | 1.00× |
| `ln` | ln one hard link | 4.41 ± 0.92 | 4.67 ± 0.70 | 4.00 ± 1.50 | 5.73 ± 0.91 | 0.91× | 1.30× | 0.86× | 1.23× | 0.94× |
| `ln` | ln 200 hard links | 387.93 ± 16.08 | 386.00 ± 12.55 | 464.89 ± 12.21 | 990.87 ± 114.81 | 1.20× | 2.55× | 1.20× | 2.57× | 1.00× |
| `ln` | ln 200 symbolic links | 356.62 ± 25.64 | 350.10 ± 26.53 | 426.13 ± 34.35 | 1007.50 ± 187.94 | 1.19× | 2.83× | 1.22× | 2.88× | 1.02× |
| `ln` | ln 200 forced replacements | 407.81 ± 29.76 | 400.45 ± 16.81 | 504.26 ± 63.44 | 1060.75 ± 64.52 | 1.24× | 2.60× | 1.26× | 2.65× | 1.02× |
| `ln` | ln 200 numbered backups | 643.08 ± 23.87 | 700.66 ± 53.37 | 728.08 ± 19.32 | 1078.04 ± 87.22 | 1.13× | 1.68× | 1.04× | 1.54× | 0.92× |
| `ln` | ln 200 relative symbolic links | 374.15 ± 31.66 | 392.25 ± 37.34 | 455.99 ± 28.56 | 948.65 ± 42.41 | 1.22× | 2.54× | 1.16× | 2.42× | 0.95× |
| `logname` | logname | exit 1 | exit 1 | 1.94 ± 0.75 | 3.50 ± 0.16 | — | — | — | — | — |
| `ls` | 4000 names, no stat | 5.41 ± 1.42 | 4.52 ± 0.76 | 11.35 ± 2.11 | 7.55 ± 0.44 | 2.10× | 1.40× | 2.51× | 1.67× | 1.20× |
| `ls` | -l over 4000 names | 17.69 ± 2.87 | 16.06 ± 2.71 | 22.41 ± 0.66 | 16.85 ± 0.42 | 1.27× | 0.95× | 1.39× | 1.05× | 1.10× |
| `ls` | -U (unsorted) over 4000 names | 4.25 ± 0.83 | 3.54 ± 0.17 | 3.76 ± 0.84 | 7.41 ± 0.41 | 0.89× | 1.75× | 1.06× | 2.09× | 1.20× |
| `ls` | -v (filevercmp) over 4000 names | 9.75 ± 0.36 | 8.47 ± 2.54 | 5.90 ± 0.32 | 33.80 ± 4.15 | 0.61× | 3.47× | 0.70× | 3.99× | 1.15× |
| `ls` | -t over 4000 names | 14.32 ± 2.66 | 12.63 ± 0.55 | 11.57 ± 1.41 | 13.22 ± 0.48 | 0.81× | 0.92× | 0.92× | 1.05× | 1.13× |
| `ls` | -S over 4000 names | 13.73 ± 0.97 | 11.88 ± 0.69 | 17.85 ± 3.13 | 12.02 ± 0.78 | 1.30× | 0.88× | 1.50× | 1.01× | 1.16× |
| `ls` | -C -w 200 over 4000 names | 6.18 ± 0.08 | 5.62 ± 1.50 | 10.64 ± 0.40 | 7.67 ± 0.16 | 1.72× | 1.24× | 1.89× | 1.36× | 1.10× |
| `ls` | -x -w 200 over 4000 names | 6.45 ± 1.92 | 5.00 ± 0.24 | 11.02 ± 0.54 | 8.74 ± 1.81 | 1.71× | 1.35× | 2.20× | 1.75× | 1.29× |
| `ls` | -m -w 200 over 4000 names | 6.37 ± 0.18 | 4.97 ± 0.22 | 10.80 ± 0.29 | 7.82 ± 0.38 | 1.70× | 1.23× | 2.17× | 1.57× | 1.28× |
| `ls` | -i -s over 4000 names | 14.97 ± 0.48 | 14.05 ± 2.89 | 19.18 ± 1.79 | 14.60 ± 0.38 | 1.28× | 0.97× | 1.37× | 1.04× | 1.07× |
| `ls` | -F over 1500 mixed entries | 7.36 ± 0.35 | 7.46 ± 0.75 | 5.59 ± 1.02 | 6.37 ± 0.55 | 0.76× | 0.87× | 0.75× | 0.85× | 0.99× |
| `ls` | -l over 1500 mixed entries | 9.04 ± 0.51 | 8.21 ± 0.52 | 10.09 ± 0.55 | 10.91 ± 0.33 | 1.12× | 1.21× | 1.23× | 1.33× | 1.10× |
| `ls` | --color=always over 1500 mixed | 6.87 ± 2.08 | 6.84 ± 1.45 | 5.99 ± 0.56 | 7.14 ± 0.36 | 0.87× | 1.04× | 0.88× | 1.04× | 1.01× |
| `ls` | -R over a 40-deep tree | 4.97 ± 1.27 | 4.51 ± 0.27 | 3.26 ± 0.15 | 5.85 ± 0.44 | 0.66× | 1.18× | 0.72× | 1.30× | 1.10× |
| `ls` | -lR over a 40-deep tree | 6.39 ± 0.38 | 6.18 ± 0.35 | 8.29 ± 2.47 | 9.85 ± 1.84 | 1.30× | 1.54× | 1.34× | 1.59× | 1.03× |
| `ls` | -b over 4000 names | 5.46 ± 0.60 | 4.90 ± 1.15 | 11.25 ± 1.92 | 10.68 ± 0.63 | 2.06× | 1.96× | 2.30× | 2.18× | 1.11× |
| `ls` | --quoting-style=shell-escape | 5.37 ± 0.27 | 4.93 ± 1.57 | 10.76 ± 1.76 | 10.73 ± 0.45 | 2.00× | 2.00× | 2.18× | 2.17× | 1.09× |
| `ls` | --time-style=full-iso -l | 18.29 ± 0.27 | 16.67 ± 2.38 | 22.43 ± 0.43 | 16.74 ± 0.82 | 1.23× | 0.92× | 1.35× | 1.00× | 1.10× |
| `md5sum` | md5sum of a 62 MiB file | 335.25 ± 2.40 | 242.62 ± 4.20 | 86.46 ± 7.60 | 100.20 ± 9.94 | 0.26× | 0.30× | 0.36× | 0.41× | 1.38× |
| `md5sum` | md5sum of a 62 MiB file from a pipe | 349.45 ± 23.16 | 252.91 ± 11.86 | 92.40 ± 6.78 | 92.21 ± 6.04 | 0.26× | 0.26× | 0.37× | 0.36× | 1.38× |
| `md5sum` | md5sum --tag of a 62 MiB file | 336.03 ± 5.67 | 239.77 ± 5.50 | 83.89 ± 2.52 | 86.48 ± 2.49 | 0.25× | 0.26× | 0.35× | 0.36× | 1.40× |
| `md5sum` | md5sum of a small file | 1.30 ± 0.07 | 1.27 ± 0.09 | 1.64 ± 0.07 | 3.28 ± 0.12 | 1.26× | 2.52× | 1.29× | 2.58× | 1.02× |
| `md5sum` | md5sum -c over 500 small files | 11.31 ± 1.32 | 12.40 ± 2.64 | 9.85 ± 0.78 | 13.27 ± 0.84 | 0.87× | 1.17× | 0.79× | 1.07× | 0.91× |
| `mkdir` | mkdir one directory | 3.28 ± 1.32 | 4.19 ± 0.59 | 3.21 ± 0.92 | 5.06 ± 0.85 | 0.98× | 1.54× | 0.77× | 1.21× | 0.78× |
| `mkdir` | mkdir 200 directories | 336.19 ± 14.45 | 344.17 ± 18.04 | 558.84 ± 142.93 | 906.44 ± 38.20 | 1.66× | 2.70× | 1.62× | 2.63× | 0.98× |
| `mkdir` | mkdir -m 755 200 directories | 384.06 ± 43.59 | 380.10 ± 30.40 | 431.68 ± 36.73 | 919.17 ± 69.40 | 1.12× | 2.39× | 1.14× | 2.42× | 1.01× |
| `mkdir` | mkdir -m 1777 200 directories | 378.05 ± 31.46 | 367.68 ± 20.18 | 447.31 ± 21.57 | 962.80 ± 119.91 | 1.18× | 2.55× | 1.22× | 2.62× | 1.03× |
| `mkdir` | mkdir -m symbolic 200 directories | 351.27 ± 14.71 | 365.71 ± 50.14 | 421.50 ± 17.84 | 913.97 ± 52.50 | 1.20× | 2.60× | 1.15× | 2.50× | 0.96× |
| `mkdir` | mkdir -p a 16-deep chain | 6.06 ± 0.87 | 5.07 ± 0.99 | 5.82 ± 1.04 | 8.36 ± 1.39 | 0.96× | 1.38× | 1.15× | 1.65× | 1.19× |
| `mkdir` | mkdir -p a 16-deep chain that exists | 3.30 ± 1.36 | 4.78 ± 3.83 | 4.85 ± 0.73 | 5.26 ± 0.92 | 1.47× | 1.59× | 1.02× | 1.10× | 0.69× |
| `mkdir` | mkdir -v 200 directories | 357.56 ± 26.95 | 393.82 ± 74.69 | 426.69 ± 14.95 | 896.90 ± 42.32 | 1.19× | 2.51× | 1.08× | 2.28× | 0.91× |
| `mkdir` | mkdir 200 names already taken | 341.03 ± 11.01 | 337.61 ± 15.24 | 411.37 ± 24.32 | 874.12 ± 42.38 | 1.21× | 2.56× | 1.22× | 2.59× | 1.01× |
| `mkfifo` | mkfifo one pipe | 5.18 ± 1.02 | 5.34 ± 1.00 | 7.32 ± 2.35 | 8.25 ± 2.14 | 1.41× | 1.59× | 1.37× | 1.54× | 0.97× |
| `mkfifo` | mkfifo 200 pipes | 345.49 ± 13.78 | 351.09 ± 21.89 | 401.31 ± 15.09 | 916.57 ± 44.77 | 1.16× | 2.65× | 1.14× | 2.61× | 0.98× |
| `mkfifo` | mkfifo 200 pipes in one run | 20.41 ± 1.54 | 21.55 ± 2.25 | 22.64 ± 0.79 | 27.53 ± 2.98 | 1.11× | 1.35× | 1.05× | 1.28× | 0.95× |
| `mkfifo` | mkfifo -m 600 200 pipes | 350.46 ± 15.53 | 350.15 ± 16.62 | 404.49 ± 14.21 | 1026.59 ± 166.70 | 1.15× | 2.93× | 1.16× | 2.93× | 1.00× |
| `mkfifo` | mkfifo -m symbolic 200 pipes | 358.91 ± 14.90 | 361.18 ± 34.61 | 498.01 ± 94.05 | 909.09 ± 51.54 | 1.39× | 2.53× | 1.38× | 2.52× | 0.99× |
| `mkfifo` | mkfifo 200 names already taken | 352.69 ± 12.39 | 352.10 ± 14.07 | 412.08 ± 13.94 | 889.45 ± 76.98 | 1.17× | 2.52× | 1.17× | 2.53× | 1.00× |
| `mknod` | mknod one fifo | 2.71 ± 5.21 | 6.34 ± 8.80 | 2.19 ± 2.01 | 2.78 ± 3.86 | 0.81× | 1.02× | 0.35× | 0.44× | 0.43× |
| `mknod` | mknod 200 fifos | 349.28 ± 14.35 | 358.74 ± 29.10 | 409.07 ± 15.49 | 930.20 ± 55.14 | 1.17× | 2.66× | 1.14× | 2.59× | 0.97× |
| `mknod` | mknod -m 600 200 fifos | 356.49 ± 25.08 | 340.41 ± 14.34 | 399.94 ± 15.70 | 1006.76 ± 81.18 | 1.12× | 2.82× | 1.17× | 2.96× | 1.05× |
| `mknod` | mknod -m symbolic 200 fifos | 377.56 ± 16.49 | 363.48 ± 21.57 | 458.45 ± 69.99 | 917.54 ± 30.95 | 1.21× | 2.43× | 1.26× | 2.52× | 1.04× |
| `mknod` | mknod 200 character devices | 371.14 ± 26.21 | 351.84 ± 13.07 | 414.21 ± 13.63 | exit 255 | 1.12× | — | 1.18× | — | 1.05× |
| `mknod` | mknod 200 hexadecimal device numbers | 349.21 ± 50.86 | 330.71 ± 12.84 | 408.71 ± 16.45 | 902.41 ± 27.86 | 1.17× | 2.58× | 1.24× | 2.73× | 1.06× |
| `mknod` | mknod 200 invalid device types | 336.48 ± 13.52 | 327.50 ± 20.62 | 400.79 ± 11.18 | 939.35 ± 20.33 | 1.19× | 2.79× | 1.22× | 2.87× | 1.03× |
| `mknod` | mknod 200 names already taken | 366.60 ± 16.62 | 368.55 ± 15.90 | 431.25 ± 18.65 | exit 255 | 1.18× | — | 1.17× | — | 0.99× |
| `mktemp` | mktemp -u default template | 1.39 ± 0.20 | 1.42 ± 0.35 | 1.78 ± 0.30 | 3.77 ± 0.20 | 1.29× | 2.72× | 1.26× | 2.66× | 0.98× |
| `mktemp` | mktemp -u fooXXXXXX | 1.46 ± 0.12 | 1.42 ± 0.25 | 1.87 ± 0.64 | 3.80 ± 0.22 | 1.28× | 2.60× | 1.32× | 2.68× | 1.03× |
| `mktemp` | mktemp -u 40 random characters | 1.45 ± 0.22 | 1.39 ± 0.13 | 1.86 ± 0.16 | 3.81 ± 0.91 | 1.28× | 2.63× | 1.33× | 2.74× | 1.04× |
| `mktemp` | mktemp create a file | 1.49 ± 0.52 | 1.51 ± 0.13 | 1.97 ± 0.18 | 3.91 ± 0.32 | 1.32× | 2.63× | 1.30× | 2.60× | 0.99× |
| `mktemp` | mktemp create a directory | 1.50 ± 0.73 | 1.53 ± 0.12 | 1.89 ± 0.34 | 3.81 ± 0.30 | 1.26× | 2.54× | 1.23× | 2.49× | 0.98× |
| `mv` | mv one file | 2.17 ± 0.71 | 2.11 ± 2.14 | 1.91 ± 0.73 | 4.73 ± 0.93 | 0.88× | 2.18× | 0.90× | 2.24× | 1.03× |
| `mv` | mv 200 files | 391.20 ± 19.08 | 390.15 ± 14.78 | 496.84 ± 19.05 | 1033.00 ± 58.69 | 1.27× | 2.64× | 1.27× | 2.65× | 1.00× |
| `mv` | mv 200 files into a directory | 44.55 ± 3.93 | 45.08 ± 1.98 | 45.53 ± 3.74 | 60.18 ± 3.00 | 1.02× | 1.35× | 1.01× | 1.33× | 0.99× |
| `mv` | mv 200 over existing files | 383.78 ± 15.01 | 384.68 ± 16.49 | 492.03 ± 14.22 | 1043.76 ± 34.33 | 1.28× | 2.72× | 1.28× | 2.71× | 1.00× |
| `mv` | mv 200 numbered backups | 454.94 ± 19.38 | 459.09 ± 10.09 | 593.80 ± 79.52 | 1152.03 ± 48.86 | 1.31× | 2.53× | 1.29× | 2.51× | 0.99× |
| `mv` | mv 200 --update comparisons | 379.86 ± 41.13 | 379.45 ± 36.97 | 495.78 ± 40.35 | 957.96 ± 19.39 | 1.31× | 2.52× | 1.31× | 2.52× | 1.00× |
| `mv` | mv a 400-file tree | 56.64 ± 15.24 | 52.73 ± 3.63 | 52.61 ± 3.29 | 58.70 ± 14.89 | 0.93× | 1.04× | 1.00× | 1.11× | 1.07× |
| `nice` | nice reads the niceness | 1.71 ± 0.88 | 2.11 ± 0.69 | 3.36 ± 2.94 | 4.00 ± 1.73 | 1.97× | 2.35× | 1.59× | 1.90× | 0.81× |
| `nice` | nice runs a command | exit 140 | exit 140 | 4.64 ± 8.74 | 2.44 ± 1.79 | — | — | — | — | — |
| `nice` | nice -n 5 a command | exit 140 | exit 140 | 1.76 ± 5.97 | 3.95 ± 4.13 | — | — | — | — | — |
| `nice` | nice -5 a command | exit 140 | exit 140 | 0.92 ± 3.30 | 1.53 ± 0.96 | — | — | — | — | — |
| `nice` | nice --adjustment=5 a command | exit 140 | exit 140 | 2.19 ± 7.33 | 3.39 ± 3.76 | — | — | — | — | — |
| `nice` | nice -n -5 a command | exit 140 | exit 140 | 4.30 ± 7.73 | 5.64 ± 2.79 | — | — | — | — | — |
| `nice` | nice a command not found | exit 140 | exit 140 | 4.90 ± 6.40 | 5.77 ± 0.52 | — | — | — | — | — |
| `nice` | nice an invalid adjustment | 2.38 ± 2.47 | 1.94 ± 1.73 | 2.21 ± 0.39 | 5.40 ± 0.63 | 0.93× | 2.27× | 1.14× | 2.78× | 1.22× |
| `nl` | nl a 62 MiB file | 212.32 ± 3.15 | 131.74 ± 9.99 | 1212.59 ± 168.17 | 421.76 ± 20.80 | 5.71× | 1.99× | 9.20× | 3.20× | 1.61× |
| `nl` | nl -ba a 62 MiB file | 224.16 ± 9.88 | 138.23 ± 9.35 | 1120.02 ± 28.81 | 424.49 ± 21.85 | 5.00× | 1.89× | 8.10× | 3.07× | 1.62× |
| `nl` | nl -bn a 62 MiB file | 85.24 ± 5.52 | 82.96 ± 6.19 | 438.56 ± 5.19 | 333.80 ± 17.53 | 5.14× | 3.92× | 5.29× | 4.02× | 1.03× |
| `nl` | nl -n rz -w12 a 62 MiB file | 220.17 ± 5.40 | 125.46 ± 5.18 | 1191.48 ± 18.59 | 645.54 ± 29.75 | 5.41× | 2.93× | 9.50× | 5.15× | 1.75× |
| `nl` | nl -bp7 a 3 MiB file | 18.23 ± 2.02 | 18.30 ± 3.50 | 83.12 ± 11.00 | 35.35 ± 1.65 | 4.56× | 1.94× | 4.54× | 1.93× | 1.00× |
| `nl` | nl from a pipe | 215.03 ± 11.54 | 129.52 ± 2.60 | 1186.50 ± 104.98 | 465.57 ± 21.43 | 5.52× | 2.17× | 9.16× | 3.59× | 1.66× |
| `nohup` | nohup a command | 0.32 ± 1.64 | 0.22 ± 0.48 | 1.17 ± 0.66 | 3.08 ± 1.24 | 3.65× | 9.62× | 5.26× | 13.87× | 1.44× |
| `nohup` | nohup a command with arguments | 3.60 ± 0.97 | 3.58 ± 0.66 | 4.21 ± 3.95 | 7.91 ± 4.80 | 1.17× | 2.20× | 1.18× | 2.21× | 1.00× |
| `nohup` | nohup a command not found | 3.44 ± 7.29 | 0.86 ± 3.05 | 1.99 ± 6.37 | 2.18 ± 8.37 | 0.58× | 0.63× | 2.32× | 2.55× | 4.02× |
| `nohup` | nohup no operand | 1.40 ± 1.15 | 1.27 ± 0.60 | 2.20 ± 1.89 | 4.55 ± 2.44 | 1.58× | 3.26× | 1.74× | 3.59× | 1.10× |
| `nohup` | nohup a status passed through | 1.16 ± 0.84 | 0.80 ± 0.53 | 1.71 ± 1.00 | 4.29 ± 0.58 | 1.47× | 3.69× | 2.14× | 5.38× | 1.46× |
| `nproc` | nproc | 1.43 ± 0.31 | 1.57 ± 1.37 | 1.90 ± 0.27 | 3.86 ± 0.38 | 1.33× | 2.70× | 1.21× | 2.45× | 0.91× |
| `nproc` | nproc --all | 1.40 ± 0.13 | 1.45 ± 1.53 | 1.79 ± 0.11 | 3.77 ± 0.33 | 1.28× | 2.69× | 1.23× | 2.60× | 0.97× |
| `nproc` | nproc --ignore=1 | 1.49 ± 0.45 | 1.49 ± 0.28 | 1.88 ± 1.84 | 3.59 ± 0.40 | 1.26× | 2.41× | 1.26× | 2.41× | 1.00× |
| `numfmt` | numfmt --to=si 1000 | 1.71 ± 0.76 | 1.50 ± 0.38 | 1.69 ± 0.18 | 3.66 ± 0.42 | 0.99× | 2.14× | 1.13× | 2.43× | 1.14× |
| `numfmt` | numfmt --from=si 1.5G | 1.43 ± 0.36 | 1.58 ± 0.73 | 2.25 ± 0.55 | 4.29 ± 0.57 | 1.57× | 2.99× | 1.42× | 2.71× | 0.91× |
| `numfmt` | numfmt --to=iec --format %.3f | 1.44 ± 0.08 | 1.55 ± 0.87 | 1.76 ± 0.32 | 3.81 ± 0.20 | 1.22× | 2.64× | 1.14× | 2.46× | 0.93× |
| `numfmt` | numfmt 5000 operands | 3.70 ± 1.46 | 3.64 ± 1.41 | 4.02 ± 1.29 | exit 0 | 1.09× | — | 1.10× | — | 1.01× |
| `numfmt` | numfmt 200k lines from stdin | 96.70 ± 1.02 | 52.25 ± 9.40 | 66.42 ± 3.62 | 125.04 ± 2.34 | 0.69× | 1.29× | 1.27× | 2.39× | 1.85× |
| `numfmt` | numfmt 200k lines in a field | 2.76 ± 0.56 | 2.87 ± 0.62 | 3.23 ± 3.52 | 6.06 ± 1.04 | 1.17× | 2.20× | 1.12× | 2.11× | 0.96× |
| `od` | od default of a 62 MiB file | 810.30 ± 28.14 | 510.01 ± 17.90 | 3374.47 ± 130.82 | 3934.47 ± 48.91 | 4.16× | 4.86× | 6.62× | 7.71× | 1.59× |
| `od` | od -t x1 of a 62 MiB file | 659.48 ± 26.96 | 415.08 ± 4.59 | 5976.17 ± 101.43 | 5323.77 ± 268.65 | 9.06× | 8.07× | 14.40× | 12.83× | 1.59× |
| `od` | od -t x1 -w64 of a 62 MiB file | 460.05 ± 13.54 | 350.62 ± 22.51 | 5807.89 ± 204.29 | 3608.77 ± 112.55 | 12.62× | 7.84× | 16.56× | 10.29× | 1.31× |
| `od` | od -t x8 of a 62 MiB file | 532.89 ± 12.79 | 303.80 ± 3.94 | 1092.45 ± 38.93 | 2555.86 ± 53.92 | 2.05× | 4.80× | 3.60× | 8.41× | 1.75× |
| `od` | od -c of a 62 MiB file | 972.78 ± 17.10 | 554.70 ± 77.45 | 6142.20 ± 81.68 | 4861.68 ± 100.12 | 6.31× | 5.00× | 11.07× | 8.76× | 1.75× |
| `od` | od -A n -t x1 of a 62 MiB file | 548.58 ± 10.39 | 383.74 ± 27.38 | 5991.14 ± 147.96 | 5179.13 ± 372.52 | 10.92× | 9.44× | 15.61× | 13.50× | 1.43× |
| `od` | od -S 4 of a 62 MiB file | 104.04 ± 7.61 | 103.36 ± 8.57 | 108.68 ± 7.22 | 22616.50 ± 339.99 | 1.04× | 217.37× | 1.05× | 218.82× | 1.01× |
| `od` | od -t f8 of a 1 MiB file | 76.34 ± 8.99 | 39.47 ± 0.69 | 138.53 ± 1.84 | 68.22 ± 1.61 | 1.81× | 0.89× | 3.51× | 1.73× | 1.93× |
| `paste` | paste two 27 MiB files | 148.28 ± 2.30 | 105.00 ± 2.15 | 262.89 ± 7.53 | 1442.93 ± 10.47 | 1.77× | 9.73× | 2.50× | 13.74× | 1.41× |
| `paste` | paste -d, two 27 MiB files | 147.61 ± 2.26 | 111.56 ± 18.32 | 261.29 ± 2.58 | 1458.89 ± 27.15 | 1.77× | 9.88× | 2.34× | 13.08× | 1.32× |
| `paste` | paste -s a 27 MiB file | 52.39 ± 1.17 | 46.65 ± 1.36 | 143.80 ± 10.28 | 55.81 ± 3.60 | 2.74× | 1.07× | 3.08× | 1.20× | 1.12× |
| `paste` | paste one 27 MiB file | 72.99 ± 4.53 | 46.21 ± 1.43 | 96.29 ± 3.56 | 1398.20 ± 9.52 | 1.32× | 19.16× | 2.08× | 30.26× | 1.58× |
| `pathchk` | pathchk one existing path | 1.47 ± 0.35 | 1.43 ± 0.14 | 1.75 ± 0.30 | 3.97 ± 1.04 | 1.19× | 2.70× | 1.23× | 2.78× | 1.03× |
| `pathchk` | pathchk 200 existing paths | 1.70 ± 0.22 | 1.64 ± 0.55 | 1.98 ± 0.13 | 4.07 ± 0.20 | 1.17× | 2.39× | 1.21× | 2.48× | 1.04× |
| `pathchk` | pathchk 200 missing paths | 1.86 ± 0.68 | 1.59 ± 0.27 | 1.95 ± 0.21 | 4.02 ± 0.39 | 1.05× | 2.16× | 1.23× | 2.52× | 1.17× |
| `pathchk` | pathchk -p 200 operands | 1.49 ± 0.19 | 1.49 ± 0.12 | 2.02 ± 0.73 | 4.18 ± 0.95 | 1.36× | 2.80× | 1.35× | 2.80× | 1.00× |
| `pathchk` | pathchk --portability one 4 KiB name | 1.52 ± 0.16 | 1.52 ± 0.08 | 1.82 ± 0.17 | 3.85 ± 0.28 | 1.19× | 2.53× | 1.19× | 2.53× | 1.00× |
| `pathchk` | pathchk the component walk | 1.44 ± 0.10 | 1.49 ± 0.64 | 1.76 ± 0.13 | 3.77 ± 0.29 | 1.22× | 2.62× | 1.18× | 2.53× | 0.96× |
| `pinky` | pinky | 1.58 ± 0.30 | 1.52 ± 0.09 | 2.81 ± 1.05 | 5.17 ± 0.58 | 1.77× | 3.27× | 1.84× | 3.40× | 1.04× |
| `pinky` | pinky -q | 1.54 ± 0.19 | 1.51 ± 0.25 | 2.47 ± 1.15 | 4.50 ± 1.26 | 1.61× | 2.93× | 1.64× | 2.99× | 1.02× |
| `pinky` | pinky filtered to one user | 1.52 ± 0.11 | 1.47 ± 0.14 | 1.90 ± 0.14 | 4.04 ± 0.20 | 1.25× | 2.66× | 1.29× | 2.74× | 1.03× |
| `pinky` | pinky -l of one user | 1.73 ± 0.80 | 1.53 ± 0.75 | 2.63 ± 0.23 | 4.84 ± 0.28 | 1.52× | 2.79× | 1.72× | 3.15× | 1.13× |
| `pinky` | pinky -l of eight users | 1.95 ± 0.30 | 2.11 ± 0.57 | 3.56 ± 0.98 | 5.59 ± 0.85 | 1.82× | 2.87× | 1.69× | 2.66× | 0.93× |
| `pr` | pr a 62 MiB file | 837.57 ± 127.61 | 543.37 ± 11.15 | 385.47 ± 30.72 | 1238.48 ± 119.69 | 0.46× | 1.48× | 0.71× | 2.28× | 1.54× |
| `pr` | pr -t a 62 MiB file | 678.72 ± 61.65 | 477.75 ± 26.85 | 311.54 ± 3.21 | 1100.71 ± 49.05 | 0.46× | 1.62× | 0.65× | 2.30× | 1.42× |
| `pr` | pr -4 a 62 MiB file | 2004.45 ± 66.65 | 1122.42 ± 6.15 | 518.92 ± 1.93 | 2861.08 ± 45.66 | 0.26× | 1.43× | 0.46× | 2.55× | 1.79× |
| `pr` | pr -4 -a a 62 MiB file | 1996.77 ± 53.73 | 1203.83 ± 61.72 | 469.38 ± 29.02 | 3076.55 ± 151.24 | 0.24× | 1.54× | 0.39× | 2.56× | 1.66× |
| `pr` | pr -n a 62 MiB file | 2458.18 ± 95.56 | 1467.60 ± 46.19 | 1000.94 ± 35.40 | 1716.74 ± 45.49 | 0.41× | 0.70× | 0.68× | 1.17× | 1.67× |
| `pr` | pr -d a 62 MiB file | 889.68 ± 49.75 | 616.17 ± 28.92 | 411.63 ± 32.71 | 1178.66 ± 63.62 | 0.46× | 1.32× | 0.67× | 1.91× | 1.44× |
| `pr` | pr -v a 62 MiB file | 1430.73 ± 33.61 | 890.77 ± 16.26 | 368.85 ± 33.44 | exit 1 | 0.26× | — | 0.41× | — | 1.61× |
| `pr` | pr -F a 62 MiB file | 745.37 ± 31.72 | 537.09 ± 15.57 | 357.97 ± 8.86 | 1131.27 ± 57.66 | 0.48× | 1.52× | 0.67× | 2.11× | 1.39× |
| `pr` | pr from a pipe | 749.05 ± 37.76 | 534.74 ± 3.30 | 358.77 ± 9.93 | 4396.27 ± 77.34 | 0.48× | 5.87× | 0.67× | 8.22× | 1.40× |
| `pr` | pr -m two 62 MiB files | 5039.70 ± 117.09 (9 runs) | 2793.84 ± 47.62 (9 runs) | 915.57 ± 35.47 (9 runs) | 1754.93 ± 80.33 (9 runs) | 0.18× | 0.35× | 0.33× | 0.63× | 1.80× |
| `pr` | pr -t -e a tabbed file | 631.56 ± 35.72 | 385.65 ± 8.75 | 258.32 ± 6.03 | exit 1 | 0.41× | — | 0.67× | — | 1.64× |
| `pr` | pr -t -2 a tabbed file | 518.69 ± 24.87 | 312.11 ± 19.05 | 251.56 ± 4.34 | 462.84 ± 4.61 | 0.48× | 0.89× | 0.81× | 1.48× | 1.66× |
| `pr` | pr a small file | 1.40 ± 0.16 | 1.49 ± 0.50 | 1.93 ± 0.58 | 4.80 ± 0.31 | 1.38× | 3.43× | 1.30× | 3.23× | 0.94× |
| `printenv` | printenv | 1.68 ± 0.97 | 1.54 ± 0.70 | 1.68 ± 0.22 | 3.43 ± 0.36 | 1.01× | 2.05× | 1.09× | 2.22× | 1.09× |
| `printenv` | printenv -0 | 1.46 ± 0.46 | 1.38 ± 0.53 | 1.66 ± 0.28 | 4.34 ± 0.95 | 1.13× | 2.97× | 1.20× | 3.16× | 1.06× |
| `printenv` | printenv PATH | 1.52 ± 0.30 | 1.61 ± 0.74 | 1.64 ± 0.09 | 3.57 ± 1.29 | 1.08× | 2.35× | 1.01× | 2.21× | 0.94× |
| `printenv` | printenv four names | 1.30 ± 0.37 | 1.30 ± 0.46 | 1.74 ± 0.19 | 3.76 ± 1.50 | 1.33× | 2.89× | 1.34× | 2.89× | 1.00× |
| `printf` | printf '%s\n' x | 1.62 ± 0.44 | 1.31 ± 0.27 | 1.74 ± 0.89 | 3.84 ± 1.45 | 1.07× | 2.38× | 1.32× | 2.93× | 1.23× |
| `printf` | printf '%d %s %5.2f\n' 1 a 2.5 | 1.49 ± 0.54 | 1.59 ± 0.69 | 1.99 ± 0.43 | 4.17 ± 1.54 | 1.33× | 2.79× | 1.25× | 2.63× | 0.94× |
| `printf` | printf '%s\n' cycling over 2000 operands | 2.33 ± 0.14 | 2.23 ± 0.50 | 2.55 ± 0.76 | 5.44 ± 0.24 | 1.09× | 2.33× | 1.15× | 2.44× | 1.05× |
| `printf` | printf '%.20f %e %g' cycling over 300 floats | 2.04 ± 0.33 | 1.86 ± 0.85 | 2.24 ± 0.89 | 4.27 ± 0.58 | 1.10× | 2.09× | 1.21× | 2.30× | 1.10× |
| `ptx` | ptx 120k words | 61.55 ± 4.35 | 37.85 ± 5.02 | 43.19 ± 6.08 | 94.34 ± 3.75 | 0.70× | 1.53× | 1.14× | 2.49× | 1.63× |
| `ptx` | ptx -G 120k words | 59.05 ± 3.03 | 40.69 ± 3.47 | 65.14 ± 2.17 | 109.79 ± 5.13 | 1.10× | 1.86× | 1.60× | 2.70× | 1.45× |
| `ptx` | ptx -O 120k words | 53.36 ± 1.50 | 39.53 ± 7.29 | 55.96 ± 0.89 | 105.58 ± 2.70 | 1.05× | 1.98× | 1.42× | 2.67× | 1.35× |
| `ptx` | ptx -T 120k words | 75.76 ± 1.08 | 47.82 ± 7.05 | 56.15 ± 9.34 | 272.80 ± 4.78 | 0.74× | 3.60× | 1.17× | 5.71× | 1.58× |
| `ptx` | ptx -W a regexp alphabet | 141.44 ± 2.00 | 84.40 ± 3.29 | 531.62 ± 20.18 | 92.72 ± 3.27 | 3.76× | 0.66× | 6.30× | 1.10× | 1.68× |
| `ptx` | ptx prose with sentences | 35.32 ± 2.65 | 20.64 ± 1.00 | 27.34 ± 1.75 | 55.13 ± 1.77 | 0.77× | 1.56× | 1.32× | 2.67× | 1.71× |
| `ptx` | ptx -A prose | 45.98 ± 1.29 | 34.23 ± 1.87 | 37.51 ± 7.67 | 85.20 ± 1.51 | 0.82× | 1.85× | 1.10× | 2.49× | 1.34× |
| `ptx` | ptx from a pipe | 60.37 ± 1.80 | 37.65 ± 2.82 | 40.54 ± 1.62 | 89.26 ± 1.09 | 0.67× | 1.48× | 1.08× | 2.37× | 1.60× |
| `pwd` | pwd | 1.57 ± 0.37 | 1.64 ± 0.78 | 1.89 ± 0.29 | 3.89 ± 0.42 | 1.20× | 2.48× | 1.15× | 2.37× | 0.96× |
| `pwd` | pwd -L | 1.40 ± 0.15 | 1.45 ± 0.20 | 1.78 ± 0.65 | 4.50 ± 1.77 | 1.27× | 3.22× | 1.23× | 3.11× | 0.96× |
| `readlink` | readlink one link | 1.38 ± 0.31 | 1.27 ± 0.09 | 1.66 ± 0.25 | 3.47 ± 0.42 | 1.20× | 2.52× | 1.31× | 2.73× | 1.09× |
| `readlink` | readlink -f one link | 1.32 ± 0.50 | 1.31 ± 0.15 | 1.69 ± 0.16 | 3.42 ± 0.32 | 1.28× | 2.60× | 1.28× | 2.60× | 1.00× |
| `readlink` | readlink -e one link | 1.29 ± 0.25 | 1.71 ± 0.78 | 1.88 ± 0.44 | 4.04 ± 0.58 | 1.46× | 3.13× | 1.10× | 2.36× | 0.75× |
| `readlink` | readlink -m a missing deep path | 1.45 ± 0.20 | 1.42 ± 0.13 | 1.63 ± 0.22 | 3.70 ± 0.90 | 1.13× | 2.56× | 1.15× | 2.61× | 1.02× |
| `readlink` | readlink 200 links | 1.84 ± 0.49 | 1.79 ± 0.16 | 2.05 ± 0.34 | 4.10 ± 1.29 | 1.11× | 2.23× | 1.15× | 2.29× | 1.03× |
| `readlink` | readlink -f 200 links | 4.19 ± 0.24 | 4.48 ± 0.52 | 4.26 ± 0.38 | 7.98 ± 0.41 | 1.02× | 1.90× | 0.95× | 1.78× | 0.94× |
| `realpath` | realpath one link | 1.60 ± 0.72 | 1.47 ± 0.68 | 1.78 ± 0.24 | 3.71 ± 0.22 | 1.12× | 2.32× | 1.21× | 2.52× | 1.09× |
| `realpath` | realpath -s one link | 1.40 ± 0.36 | 1.42 ± 0.57 | 1.78 ± 0.24 | 3.85 ± 0.51 | 1.27× | 2.74× | 1.26× | 2.72× | 0.99× |
| `realpath` | realpath -L one link | 1.44 ± 0.26 | 1.44 ± 0.28 | 1.93 ± 0.84 | 3.81 ± 1.12 | 1.34× | 2.65× | 1.33× | 2.64× | 1.00× |
| `realpath` | realpath -m a missing deep path | 1.39 ± 0.15 | 1.43 ± 0.07 | 2.02 ± 0.81 | 3.61 ± 0.86 | 1.45× | 2.60× | 1.41× | 2.53× | 0.97× |
| `realpath` | realpath --relative-to one link | 1.43 ± 0.08 | 1.41 ± 0.10 | 1.80 ± 0.56 | 3.62 ± 0.16 | 1.26× | 2.54× | 1.27× | 2.57× | 1.01× |
| `realpath` | realpath 200 links | 4.22 ± 1.25 | 4.33 ± 0.86 | 4.27 ± 0.92 | 8.15 ± 0.50 | 1.01× | 1.93× | 0.99× | 1.88× | 0.98× |
| `realpath` | realpath -s 200 links | 2.05 ± 0.48 | 1.98 ± 0.82 | 2.22 ± 0.20 | 4.48 ± 0.38 | 1.08× | 2.18× | 1.12× | 2.26× | 1.04× |
| `rm` | rm one file | 1.09 ± 0.68 | 2.18 ± 1.21 | 1.68 ± 1.67 | 3.64 ± 1.10 | 1.54× | 3.33× | 0.77× | 1.67× | 0.50× |
| `rm` | rm 200 files in one call | 27.93 ± 2.90 | 27.21 ± 2.74 | 27.90 ± 10.85 | 30.71 ± 1.72 | 1.00× | 1.10× | 1.03× | 1.13× | 1.03× |
| `rm` | rm -r a 400-file tree | 52.13 ± 2.12 | 50.87 ± 2.26 | 47.28 ± 1.68 | 58.80 ± 11.70 | 0.91× | 1.13× | 0.93× | 1.16× | 1.02× |
| `rmdir` | rmdir one directory | 3.82 ± 1.09 | 3.47 ± 0.99 | 4.89 ± 0.88 | 6.15 ± 1.33 | 1.28× | 1.61× | 1.41× | 1.77× | 1.10× |
| `rmdir` | rmdir 200 directories | 372.10 ± 12.79 | 374.66 ± 17.08 | 442.24 ± 24.03 | 954.80 ± 60.50 | 1.19× | 2.57× | 1.18× | 2.55× | 0.99× |
| `rmdir` | rmdir -p a 16-deep chain | 6.30 ± 1.02 | 6.80 ± 0.87 | 6.02 ± 1.47 | 8.74 ± 1.62 | 0.95× | 1.39× | 0.88× | 1.28× | 0.93× |
| `rmdir` | rmdir 200 missing directories | 344.19 ± 43.87 | 343.19 ± 27.06 | 421.41 ± 24.18 | 912.50 ± 82.92 | 1.22× | 2.65× | 1.23× | 2.66× | 1.00× |
| `seq` | seq 1 1000000 | 4.82 ± 0.86 | 3.05 ± 0.18 | 9.74 ± 1.00 | 9.47 ± 0.79 | 2.02× | 1.96× | 3.19× | 3.11× | 1.58× |
| `seq` | seq -s, 1 1000000 | 4.83 ± 0.44 | 3.47 ± 2.10 | 9.63 ± 0.50 | 10.20 ± 2.23 | 1.99× | 2.11× | 2.77× | 2.94× | 1.39× |
| `seq` | seq -w 1 1000000 | 4.77 ± 0.95 | 3.07 ± 0.27 | 188.82 ± 3.11 | 7.53 ± 0.62 | 39.61× | 1.58× | 61.43× | 2.45× | 1.55× |
| `seq` | seq 0 0.001 1000 | 4.90 ± 0.69 | 3.20 ± 0.68 | 212.94 ± 19.45 | 149.55 ± 23.20 | 43.49× | 30.54× | 66.46× | 46.68× | 1.53× |
| `seq` | seq -f %.3f 0 0.001 1000 | 4.95 ± 1.08 | 3.05 ± 0.76 | 206.07 ± 2.32 | 138.36 ± 2.08 | 41.60× | 27.93× | 67.54× | 45.34× | 1.62× |
| `seq` | seq 1 2 1000000 | 3.07 ± 0.23 | 2.62 ± 0.15 | 6.22 ± 0.28 | 6.59 ± 0.51 | 2.02× | 2.14× | 2.38× | 2.52× | 1.17× |
| `seq` | seq 1000000 -1 1 | 30.93 ± 0.72 | 24.68 ± 1.37 | 181.27 ± 2.97 | 89.74 ± 1.68 | 5.86× | 2.90× | 7.34× | 3.64× | 1.25× |
| `sha1sum` | sha1sum of a 62 MiB file | 356.66 ± 5.84 | 219.45 ± 1.56 | 74.40 ± 1.21 | 58.85 ± 1.89 | 0.21× | 0.16× | 0.34× | 0.27× | 1.63× |
| `sha1sum` | sha1sum of a 62 MiB file from a pipe | 360.02 ± 4.86 | 226.41 ± 1.92 | 85.18 ± 2.15 | 69.23 ± 4.91 | 0.24× | 0.19× | 0.38× | 0.31× | 1.59× |
| `sha1sum` | sha1sum --tag of a 62 MiB file | 352.96 ± 2.15 | 219.94 ± 4.66 | 75.62 ± 2.93 | 59.07 ± 2.53 | 0.21× | 0.17× | 0.34× | 0.27× | 1.60× |
| `sha1sum` | sha1sum of a small file | 1.43 ± 0.48 | 1.42 ± 0.18 | 1.77 ± 0.08 | 3.81 ± 1.27 | 1.24× | 2.66× | 1.24× | 2.68× | 1.01× |
| `sha1sum` | sha1sum -c over 500 small files | 13.07 ± 0.90 | 11.73 ± 0.85 | 11.16 ± 1.87 | 13.88 ± 0.72 | 0.85× | 1.06× | 0.95× | 1.18× | 1.11× |
| `sha224sum` | sha224sum of a 62 MiB file | 545.38 ± 4.49 | 261.37 ± 6.76 | 150.05 ± 10.28 | 143.90 ± 4.96 | 0.28× | 0.26× | 0.57× | 0.55× | 2.09× |
| `sha224sum` | sha224sum of a 62 MiB file from a pipe | 572.00 ± 32.54 | 261.93 ± 3.09 | 165.31 ± 23.50 | 156.19 ± 1.70 | 0.29× | 0.27× | 0.63× | 0.60× | 2.18× |
| `sha224sum` | sha224sum --tag of a 62 MiB file | 585.92 ± 83.78 | 258.47 ± 4.87 | 147.69 ± 1.84 | 149.25 ± 18.66 | 0.25× | 0.25× | 0.57× | 0.58× | 2.27× |
| `sha224sum` | sha224sum of a small file | 1.52 ± 0.64 | 1.36 ± 0.14 | 1.76 ± 0.13 | 3.98 ± 1.01 | 1.16× | 2.61× | 1.29× | 2.92× | 1.12× |
| `sha224sum` | sha224sum -c over 500 small files | 14.80 ± 1.42 | 11.63 ± 0.86 | 12.40 ± 4.19 | 14.68 ± 2.61 | 0.84× | 0.99× | 1.07× | 1.26× | 1.27× |
| `sha256sum` | sha256sum of a 62 MiB file | 558.30 ± 16.83 | 258.19 ± 5.99 | 147.25 ± 1.22 | 147.09 ± 13.69 | 0.26× | 0.26× | 0.57× | 0.57× | 2.16× |
| `sha256sum` | sha256sum of a 62 MiB file from a pipe | 570.23 ± 27.85 | 264.38 ± 2.42 | 156.84 ± 3.30 | 156.57 ± 14.00 | 0.28× | 0.27× | 0.59× | 0.59× | 2.16× |
| `sha256sum` | sha256sum --tag of a 62 MiB file | 562.30 ± 39.45 | 260.72 ± 6.72 | 150.32 ± 2.12 | 148.59 ± 20.15 | 0.27× | 0.26× | 0.58× | 0.57× | 2.16× |
| `sha256sum` | sha256sum of a small file | 1.44 ± 0.12 | 1.41 ± 0.16 | 1.73 ± 0.21 | 3.67 ± 0.24 | 1.21× | 2.55× | 1.23× | 2.60× | 1.02× |
| `sha256sum` | sha256sum -c over 500 small files | 16.36 ± 5.56 | 11.74 ± 1.85 | 11.30 ± 1.02 | 15.36 ± 3.25 | 0.69× | 0.94× | 0.96× | 1.31× | 1.39× |
| `sha384sum` | sha384sum of a 62 MiB file | 382.64 ± 3.97 | 126.23 ± 1.42 | 99.42 ± 10.85 | 95.03 ± 5.23 | 0.26× | 0.25× | 0.79× | 0.75× | 3.03× |
| `sha384sum` | sha384sum of a 62 MiB file from a pipe | 389.56 ± 3.33 | 132.09 ± 2.74 | 105.93 ± 2.40 | 110.22 ± 18.99 | 0.27× | 0.28× | 0.80× | 0.83× | 2.95× |
| `sha384sum` | sha384sum --tag of a 62 MiB file | 384.80 ± 5.48 | 127.20 ± 9.81 | 92.88 ± 0.54 | 91.31 ± 0.62 | 0.24× | 0.24× | 0.73× | 0.72× | 3.03× |
| `sha384sum` | sha384sum of a small file | 1.34 ± 0.14 | 1.27 ± 0.16 | 1.87 ± 0.59 | 3.67 ± 0.95 | 1.40× | 2.73× | 1.47× | 2.88× | 1.05× |
| `sha384sum` | sha384sum -c over 500 small files | 13.08 ± 1.26 | 10.25 ± 0.96 | 10.26 ± 1.08 | 14.24 ± 1.55 | 0.78× | 1.09× | 1.00× | 1.39× | 1.28× |
| `sha512sum` | sha512sum of a 62 MiB file | 382.97 ± 42.21 | 127.12 ± 3.27 | 93.07 ± 0.60 | 91.14 ± 0.74 | 0.24× | 0.24× | 0.73× | 0.72× | 3.01× |
| `sha512sum` | sha512sum of a 62 MiB file from a pipe | 385.06 ± 25.73 | 129.65 ± 2.89 | 105.80 ± 3.13 | 106.39 ± 10.93 | 0.27× | 0.28× | 0.82× | 0.82× | 2.97× |
| `sha512sum` | sha512sum --tag of a 62 MiB file | 401.40 ± 101.58 | 125.61 ± 5.64 | 93.27 ± 0.73 | 100.42 ± 13.51 | 0.23× | 0.25× | 0.74× | 0.80× | 3.20× |
| `sha512sum` | sha512sum of a small file | 1.54 ± 0.43 | 1.52 ± 0.46 | 1.98 ± 0.73 | 3.78 ± 0.72 | 1.29× | 2.46× | 1.30× | 2.48× | 1.01× |
| `sha512sum` | sha512sum -c over 500 small files | 15.67 ± 1.59 | 11.69 ± 1.13 | 10.41 ± 1.00 | 15.34 ± 2.47 | 0.66× | 0.98× | 0.89× | 1.31× | 1.34× |
| `shred` | shred one small file | 4.26 ± 2.30 | 3.15 ± 2.38 | 4.08 ± 0.94 | 23.22 ± 5.15 | 0.96× | 5.45× | 1.29× | 7.36× | 1.35× |
| `shred` | shred 200 small files | 857.97 ± 52.96 | 851.20 ± 66.08 | 878.65 ± 63.84 | 5139.28 ± 549.06 | 1.02× | 5.99× | 1.03× | 6.04× | 1.01× |
| `shred` | shred 200 small files in one run | 491.27 ± 27.07 | 525.96 ± 45.79 | 507.87 ± 29.08 | 3267.80 ± 1127.06 | 1.03× | 6.65× | 0.97× | 6.21× | 0.93× |
| `shred` | shred 4MiB default passes | 20.07 ± 2.24 | 14.75 ± 4.02 | 10.20 ± 1.08 | 31.21 ± 1.84 | 0.51× | 1.56× | 0.69× | 2.12× | 1.36× |
| `shred` | shred 4MiB -n 1 | 10.80 ± 0.97 | 8.17 ± 2.03 | 7.86 ± 1.49 | 18.03 ± 1.51 | 0.73× | 1.67× | 0.96× | 2.21× | 1.32× |
| `shred` | shred 4MiB -n 1 -z | 11.27 ± 2.18 | 9.63 ± 2.00 | 9.70 ± 2.06 | 23.94 ± 2.70 | 0.86× | 2.12× | 1.01× | 2.49× | 1.17× |
| `shred` | shred 4MiB -n 1 from a file source | 6.38 ± 2.37 | 7.30 ± 1.50 | 8.44 ± 1.77 | 13.98 ± 2.30 | 1.32× | 2.19× | 1.16× | 1.91× | 0.87× |
| `shred` | shred 4MiB -n 4 from a file source | 10.18 ± 1.27 | 12.24 ± 4.86 | 11.17 ± 2.07 | 34.42 ± 4.76 | 1.10× | 3.38× | 0.91× | 2.81× | 0.83× |
| `shred` | shred 4MiB -n 1 -x | 9.75 ± 0.97 | 9.17 ± 4.69 | 8.79 ± 1.06 | 18.10 ± 1.53 | 0.90× | 1.86× | 0.96× | 1.97× | 1.06× |
| `shred` | shred 200 sub-block files | 534.84 ± 51.04 | 525.49 ± 23.46 | 514.01 ± 19.25 | 3051.08 ± 91.00 | 0.96× | 5.70× | 0.98× | 5.81× | 1.02× |
| `shred` | shred -u 200 small files | 563.27 ± 37.16 | 557.27 ± 40.77 | 739.98 ± 371.13 | 6941.02 ± 272.58 | 1.31× | 12.32× | 1.33× | 12.46× | 1.01× |
| `shred` | shred -n 0 -u 200 small files | 494.47 ± 25.44 | 484.64 ± 11.19 | 496.29 ± 27.74 | 4467.11 ± 144.68 | 1.00× | 9.03× | 1.02× | 9.22× | 1.02× |
| `shred` | shred -s 4096 of a 4MiB file | 3.04 ± 1.77 | 2.63 ± 2.67 | 1.98 ± 3.67 | 8.56 ± 1.64 | 0.65× | 2.82× | 0.75× | 3.25× | 1.15× |
| `shuf` | shuf -i 1-1000000 | 86.06 ± 19.93 | 51.51 ± 1.90 | 39.30 ± 1.89 | 54.65 ± 7.30 | 0.46× | 0.64× | 0.76× | 1.06× | 1.67× |
| `shuf` | shuf a 62 MiB file | 1458.68 ± 72.53 | 1118.74 ± 45.40 | 1117.83 ± 15.77 | 234.85 ± 7.26 | 0.77× | 0.16× | 1.00× | 0.21× | 1.30× |
| `shuf` | shuf a 62 MiB file from a pipe | 1407.03 ± 51.91 | 1054.98 ± 42.42 | 1120.00 ± 16.41 | 244.98 ± 7.91 | 0.80× | 0.17× | 1.06× | 0.23× | 1.33× |
| `shuf` | shuf -n 10 of a 62 MiB file | 782.71 ± 8.02 | 490.04 ± 2.17 | 178.08 ± 23.32 | 54.67 ± 8.00 | 0.23× | 0.07× | 0.36× | 0.11× | 1.60× |
| `shuf` | shuf -n 1000000 of a 62 MiB file | 1255.36 ± 87.84 | 854.26 ± 44.56 | 410.99 ± 18.43 | 84.78 ± 3.98 | 0.33× | 0.07× | 0.48× | 0.10× | 1.47× |
| `shuf` | shuf -r -n 1000000 of a 62 MiB file | 361.99 ± 17.63 | 329.69 ± 8.44 | 214.49 ± 7.53 | 111.69 ± 19.64 | 0.59× | 0.31× | 0.65× | 0.34× | 1.10× |
| `shuf` | shuf -n 10 -i 1-1000000000 | 1.61 ± 0.14 | 1.53 ± 0.12 | 1.77 ± 0.47 | 3.62 ± 0.43 | 1.10× | 2.25× | 1.16× | 2.37× | 1.05× |
| `shuf` | shuf -e 200 operands | 1.77 ± 0.29 | 1.62 ± 0.22 | 1.79 ± 0.19 | 3.64 ± 0.27 | 1.01× | 2.05× | 1.11× | 2.25× | 1.10× |
| `sleep` | sleep 0 | 1.34 ± 0.52 | 1.28 ± 0.10 | 1.75 ± 0.22 | 3.60 ± 0.23 | 1.30× | 2.68× | 1.37× | 2.82× | 1.05× |
| `sleep` | sleep 0 four operands | 1.34 ± 0.20 | 1.37 ± 0.59 | 1.74 ± 0.08 | 3.53 ± 0.22 | 1.30× | 2.64× | 1.28× | 2.58× | 0.98× |
| `sort` | sort 500k lines | 51.72 ± 1.32 | 33.74 ± 1.33 | 335.31 ± 16.54 | 146.79 ± 14.56 | 6.48× | 2.84× | 9.94× | 4.35× | 1.53× |
| `sort` | sort -n 500k numbers | 78.53 ± 24.03 | 46.18 ± 3.19 | 82.70 ± 2.62 | 49.73 ± 2.53 | 1.05× | 0.63× | 1.79× | 1.08× | 1.70× |
| `sort` | sort -k2,2n 500k lines | 114.95 ± 6.62 | 80.23 ± 18.38 | 92.88 ± 1.12 | 70.95 ± 6.80 | 0.81× | 0.62× | 1.16× | 0.88× | 1.43× |
| `sort` | sort -k1,1 500k lines | 94.65 ± 2.62 | 61.42 ± 3.46 | 322.83 ± 28.31 | 166.06 ± 8.31 | 3.41× | 1.75× | 5.26× | 2.70× | 1.54× |
| `sort` | sort -u 500k lines | 58.14 ± 5.72 | 36.52 ± 0.98 | 399.88 ± 8.43 | 133.78 ± 4.65 | 6.88× | 2.30× | 10.95× | 3.66× | 1.59× |
| `sort` | sort -r 500k lines | 52.44 ± 3.95 | 35.07 ± 1.72 | 329.98 ± 3.97 | 141.20 ± 2.64 | 6.29× | 2.69× | 9.41× | 4.03× | 1.50× |
| `sort` | sort -s 500k lines | 52.84 ± 1.33 | 34.26 ± 1.24 | 330.96 ± 4.11 | 104.59 ± 23.60 | 6.26× | 1.98× | 9.66× | 3.05× | 1.54× |
| `sort` | sort 500k lines from a pipe | 54.00 ± 1.18 | 36.79 ± 2.06 | 352.80 ± 33.60 | 142.98 ± 5.11 | 6.53× | 2.65× | 9.59× | 3.89× | 1.47× |
| `sort` | sort a sorted 500k-line file | 21.69 ± 6.68 | 18.30 ± 3.47 | 175.02 ± 2.62 | 44.81 ± 0.81 | 8.07× | 2.07× | 9.56× | 2.45× | 1.18× |
| `sort` | sort -c a sorted 500k-line file | 11.37 ± 0.43 | 7.02 ± 1.89 | 71.27 ± 0.48 | 34.62 ± 0.68 | 6.27× | 3.04× | 10.16× | 4.93× | 1.62× |
| `sort` | sort -m two sorted files | 38.19 ± 1.04 | 24.04 ± 0.99 | 106.43 ± 1.64 | 61.39 ± 3.98 | 2.79× | 1.61× | 4.43× | 2.55× | 1.59× |
| `split` | split -l 100000 of a 62 MiB file | 42.08 ± 19.93 | 38.72 ± 20.18 | 58.21 ± 9.17 | 72.66 ± 8.28 | 1.38× | 1.73× | 1.50× | 1.88× | 1.09× |
| `split` | split -b 8M of a 62 MiB file | 38.14 ± 10.45 | 24.58 ± 20.65 | 20.73 ± 15.33 | 32.04 ± 5.39 | 0.54× | 0.84× | 0.84× | 1.30× | 1.55× |
| `split` | split -C 8M of a 62 MiB file | 23.32 ± 15.04 | 24.89 ± 19.31 | 38.44 ± 53.39 | 226.70 ± 10.15 | 1.65× | 9.72× | 1.54× | 9.11× | 0.94× |
| `split` | split -n 8 of a 62 MiB file | 23.98 ± 24.58 | 26.03 ± 13.81 | 25.96 ± 35.81 | 27.73 ± 4.38 | 1.08× | 1.16× | 1.00× | 1.07× | 0.92× |
| `split` | split -n l/8 of a 62 MiB file | 31.51 ± 50.89 | 33.12 ± 56.62 | 37.03 ± 74.42 | 253.42 ± 18.79 | 1.18× | 8.04× | 1.12× | 7.65× | 0.95× |
| `split` | split -n r/8 of a 62 MiB file | 170.04 ± 8.17 | 171.23 ± 5.98 | 244.53 ± 4.05 | 242.78 ± 20.68 | 1.44× | 1.43× | 1.43× | 1.42× | 0.99× |
| `split` | split -l 100000 from a pipe | 28.75 ± 7.02 | 30.96 ± 13.69 | 59.80 ± 9.10 | 76.90 ± 7.15 | 2.08× | 2.67× | 1.93× | 2.48× | 0.93× |
| `split` | split -n 8 from a pipe | 52.57 ± 32.47 | 46.80 ± 26.65 | 45.58 ± 30.50 | exit 1 | 0.87× | — | 0.97× | — | 1.12× |
| `stat` | one operand, one directive | 1.47 ± 0.10 | 1.44 ± 0.12 | 1.86 ± 0.12 | 3.93 ± 0.21 | 1.26× | 2.67× | 1.29× | 2.73× | 1.02× |
| `stat` | one operand, sixteen directives | 1.45 ± 0.30 | 1.50 ± 0.12 | 1.90 ± 0.14 | 3.72 ± 1.02 | 1.31× | 2.57× | 1.27× | 2.49× | 0.97× |
| `stat` | 4000 operands, %s | 64.65 ± 7.74 | 65.00 ± 4.36 | 63.61 ± 1.45 | 72.03 ± 17.98 | 0.98× | 1.11× | 0.98× | 1.11× | 0.99× |
| `stat` | 4000 operands, %n | 64.48 ± 2.54 | 63.48 ± 4.51 | 65.44 ± 12.17 | 68.19 ± 1.97 | 1.01× | 1.06× | 1.03× | 1.07× | 1.02× |
| `stat` | 4000 operands, the numeric alphabet | 74.96 ± 9.61 | 67.77 ± 1.36 | 71.53 ± 4.97 | 73.38 ± 2.31 | 0.95× | 0.98× | 1.06× | 1.08× | 1.11× |
| `stat` | 4000 operands, %A and %F | 63.66 ± 1.11 | 64.81 ± 1.03 | 63.98 ± 2.10 | 68.60 ± 1.49 | 1.01× | 1.08× | 0.99× | 1.06× | 0.98× |
| `stat` | 4000 operands, %N quoted | 68.49 ± 0.83 | 65.10 ± 0.89 | 63.81 ± 1.38 | 70.03 ± 1.20 | 0.93× | 1.02× | 0.98× | 1.08× | 1.05× |
| `stat` | 4000 operands, widths and precisions | 66.94 ± 1.09 | 64.62 ± 1.30 | 65.95 ± 1.58 | 75.97 ± 13.15 | 0.99× | 1.13× | 1.02× | 1.18× | 1.04× |
| `stat` | 4000 operands, user and group names | 535.94 ± 26.96 | 457.61 ± 7.03 | 69.33 ± 11.77 | 71.70 ± 5.05 | 0.13× | 0.13× | 0.15× | 0.16× | 1.17× |
| `stat` | 4000 operands, a local timestamp | 65.77 ± 1.12 | 66.03 ± 0.87 | 65.74 ± 1.44 | 69.36 ± 2.11 | 1.00× | 1.05× | 1.00× | 1.05× | 1.00× |
| `stat` | 4000 operands, epoch seconds and nanoseconds | 66.29 ± 1.67 | 64.28 ± 1.27 | 65.34 ± 3.59 | 71.36 ± 1.26 | 0.99× | 1.08× | 1.02× | 1.11× | 1.03× |
| `stat` | 4000 operands, the mount point | 113.90 ± 4.78 | 113.04 ± 2.08 | 221.33 ± 1.73 | 109.63 ± 1.84 | 1.94× | 0.96× | 1.96× | 0.97× | 1.01× |
| `stat` | 4000 operands, --printf | 65.52 ± 3.96 | 65.30 ± 1.58 | 63.63 ± 1.31 | 68.78 ± 2.05 | 0.97× | 1.05× | 0.97× | 1.05× | 1.00× |
| `stat` | 4000 operands under -L | 65.04 ± 0.84 | 63.70 ± 3.50 | 68.95 ± 18.23 | 69.14 ± 1.97 | 1.06× | 1.06× | 1.08× | 1.09× | 1.02× |
| `stty` | stty print all | 1.30 ± 0.80 | 1.16 ± 0.74 | 2.57 ± 1.77 | 15.45 ± 14.95 | 1.98× | 11.88× | 2.22× | 13.33× | 1.12× |
| `stty` | stty print stty-readable | 0.11 ± 0.67 | 0.02 ± 0.35 | 0.05 ± 0.50 | 0.01 ± 0.11 | 0.47× | 0.07× | 2.25× | 0.34× | 4.81× |
| `stty` | stty print the deviations | 2.38 ± 0.81 | 2.22 ± 0.95 | 3.01 ± 1.25 | 5.37 ± 3.34 | 1.26× | 2.26× | 1.36× | 2.42× | 1.07× |
| `stty` | stty print the size | 2.00 ± 1.38 | 2.08 ± 1.91 | 1.96 ± 0.74 | 5.76 ± 3.53 | 0.98× | 2.89× | 0.94× | 2.77× | 0.96× |
| `stty` | stty set one flag | 1.35 ± 3.52 | 0.59 ± 1.19 | 1.18 ± 1.05 | 2.58 ± 1.41 | 0.87× | 1.91× | 2.00× | 4.38× | 2.29× |
| `stty` | stty set six settings | 1.47 ± 0.86 | 2.45 ± 3.85 | 3.33 ± 1.66 | 4.88 ± 2.60 | 2.26× | 3.32× | 1.36× | 1.99× | 0.60× |
| `stty` | stty set the reference set | 1.05 ± 0.77 | 0.75 ± 0.82 | 1.07 ± 4.38 | 3.37 ± 2.89 | 1.02× | 3.22× | 1.42× | 4.48× | 1.39× |
| `stty` | stty set a combination | 2.13 ± 1.46 | 3.40 ± 2.07 | 2.83 ± 4.80 | 3.85 ± 1.10 | 1.33× | 1.80× | 0.83× | 1.13× | 0.63× |
| `stty` | stty restore a saved line | 0.98 ± 0.60 | 1.30 ± 0.93 | 2.66 ± 0.60 | 3.58 ± 4.53 | 2.72× | 3.66× | 2.05× | 2.75× | 0.75× |
| `stty` | stty reject a bad name | 1.57 ± 0.40 | 1.84 ± 0.58 | 2.13 ± 0.63 | 4.84 ± 2.45 | 1.36× | 3.08× | 1.16× | 2.64× | 0.86× |
| `sum` | sum a 62 MiB file | 92.60 ± 15.36 | 91.06 ± 2.77 | 92.71 ± 0.73 | 82.51 ± 1.89 | 1.00× | 0.89× | 1.02× | 0.91× | 1.02× |
| `sum` | sum -s a 62 MiB file | 8.76 ± 4.00 | 8.32 ± 0.26 | 8.59 ± 1.39 | 13.34 ± 0.35 | 0.98× | 1.52× | 1.03× | 1.60× | 1.05× |
| `sum` | sum a 62 MiB file from a pipe | 93.82 ± 2.92 | 100.14 ± 11.37 | 100.34 ± 8.84 | 90.57 ± 2.48 | 1.07× | 0.97× | 1.00× | 0.90× | 0.94× |
| `sum` | sum -s a 62 MiB file from a pipe | 17.11 ± 4.98 | 17.38 ± 2.08 | 18.51 ± 6.31 | 24.91 ± 8.14 | 1.08× | 1.46× | 1.07× | 1.43× | 0.98× |
| `sum` | sum a small file | 1.56 ± 0.45 | 1.45 ± 0.26 | 1.79 ± 0.20 | 3.75 ± 0.70 | 1.15× | 2.40× | 1.23× | 2.59× | 1.08× |
| `sum` | sum 500 small files | 11.96 ± 1.54 | 11.79 ± 1.51 | 13.59 ± 1.65 | 17.60 ± 1.13 | 1.14× | 1.47× | 1.15× | 1.49× | 1.01× |
| `sync` | sync | 58.69 ± 2.09 | 58.76 ± 1.37 | 59.36 ± 1.55 | 60.67 ± 1.98 | 1.01× | 1.03× | 1.01× | 1.03× | 1.00× |
| `sync` | sync one file | 2.05 ± 1.79 | 2.25 ± 0.77 | 1.53 ± 0.74 | 61.18 ± 1.52 | 0.75× | 29.90× | 0.68× | 27.20× | 0.91× |
| `sync` | sync -d one file | 2.28 ± 0.63 | 1.18 ± 1.29 | 1.77 ± 0.90 | 4.30 ± 0.56 | 0.78× | 1.89× | 1.50× | 3.66× | 1.94× |
| `sync` | sync -f one file | 58.30 ± 1.73 | 64.05 ± 8.34 | 65.65 ± 8.84 | 2.95 ± 0.64 | 1.13× | 0.05× | 1.02× | 0.05× | 0.91× |
| `sync` | sync 200 files in one run | 4.24 ± 0.98 | 5.78 ± 2.07 | 5.05 ± 4.84 | 68.42 ± 9.51 | 1.19× | 16.14× | 0.87× | 11.83× | 0.73× |
| `sync` | sync -f 200 files in one run | 11237.83 ± 164.81 (5 runs) | 11185.84 ± 121.50 (5 runs) | 60.43 ± 1.50 (5 runs) | 4.97 ± 1.01 (5 runs) | 0.01× | 0.00× | 0.01× | 0.00× | 1.00× |
| `sync` | sync 200 missing names | 3.31 ± 0.65 | 4.23 ± 0.66 | 3.86 ± 1.32 | 5.39 ± 0.91 | 1.17× | 1.63× | 0.91× | 1.27× | 0.78× |
| `tac` | tac a 62 MiB file | 114.96 ± 10.10 | 82.48 ± 2.52 | 84.61 ± 1.95 | 53.84 ± 10.02 | 0.74× | 0.47× | 1.03× | 0.65× | 1.39× |
| `tac` | tac -b a 62 MiB file | 105.36 ± 2.26 | 84.22 ± 2.29 | 84.04 ± 2.19 | 54.79 ± 2.48 | 0.80× | 0.52× | 1.00× | 0.65× | 1.25× |
| `tac` | tac -s a 62 MiB file | 98.79 ± 21.21 | 72.65 ± 4.56 | 81.83 ± 1.07 | 67.42 ± 7.01 | 0.83× | 0.68× | 1.13× | 0.93× | 1.36× |
| `tac` | tac a 62 MiB file from a pipe | 122.92 ± 2.76 | 102.01 ± 1.29 | 143.59 ± 9.64 | 105.31 ± 4.65 | 1.17× | 0.86× | 1.41× | 1.03× | 1.20× |
| `tac` | tac -r over a 588 KiB file | 59.81 ± 1.02 | 34.49 ± 2.50 | 59.54 ± 3.40 | 10.22 ± 0.63 | 1.00× | 0.17× | 1.73× | 0.30× | 1.73× |
| `tac` | tac a one-line file | 1.35 ± 0.14 | 1.39 ± 0.66 | 2.16 ± 0.89 | 4.17 ± 0.76 | 1.60× | 3.09× | 1.55× | 3.00× | 0.97× |
| `tail` | tail -n 10 of a 62 MiB file | 2.00 ± 1.14 | 1.42 ± 0.24 | 1.92 ± 0.35 | 4.21 ± 1.00 | 0.96× | 2.11× | 1.35× | 2.96× | 1.40× |
| `tail` | tail -n 4000000 of a 62 MiB file | 60.88 ± 2.68 | 38.07 ± 1.34 | 60.61 ± 4.38 | 19.06 ± 1.60 | 1.00× | 0.31× | 1.59× | 0.50× | 1.60× |
| `tail` | tail -c 32M of a 62 MiB file | 17.30 ± 1.38 | 7.20 ± 0.41 | 17.97 ± 1.09 | 9.79 ± 0.55 | 1.04× | 0.57× | 2.50× | 1.36× | 2.40× |
| `tail` | tail -n 10 from a pipe | 35.04 ± 6.42 | 22.87 ± 4.31 | 92.49 ± 3.97 | 24.00 ± 0.69 | 2.64× | 0.68× | 4.04× | 1.05× | 1.53× |
| `tail` | tail -n +4000000 of a 62 MiB file | 29.49 ± 2.15 | 12.46 ± 3.99 | 57.32 ± 1.85 | 19.93 ± 4.47 | 1.94× | 0.68× | 4.60× | 1.60× | 2.37× |
| `tee` | tee 62 MiB to stdout alone | 27.03 ± 6.30 | 6.53 ± 1.77 | 50.91 ± 8.28 | 17.05 ± 6.53 | 1.88× | 0.63× | 7.80× | 2.61× | 4.14× |
| `tee` | tee 62 MiB to one file | 40.26 ± 7.54 | 21.13 ± 12.11 | 139.47 ± 18.67 | 37.25 ± 7.78 | 3.46× | 0.93× | 6.60× | 1.76× | 1.91× |
| `tee` | tee 62 MiB to four files | 95.96 ± 27.57 | 121.43 ± 69.97 | 408.52 ± 21.94 | 113.60 ± 25.02 | 4.26× | 1.18× | 3.36× | 0.94× | 0.79× |
| `tee` | tee 62 MiB from a pipe to one file | 38.28 ± 10.02 | 22.98 ± 4.68 | 165.15 ± 7.15 | 36.87 ± 5.69 | 4.31× | 0.96× | 7.19× | 1.60× | 1.67× |
| `tee` | tee 62 MiB down a pipe | 42.29 ± 6.07 | 28.49 ± 28.12 | 162.22 ± 6.15 | 43.23 ± 5.21 | 3.84× | 1.02× | 5.69× | 1.52× | 1.48× |
| `test` | test string equality | 2.66 ± 0.49 | 1.29 ± 0.24 | 1.93 ± 0.33 | 3.18 ± 0.10 | 0.73× | 1.19× | 1.49× | 2.45× | 2.06× |
| `test` | test integer compare | 1.29 ± 0.12 | 1.29 ± 0.09 | 1.71 ± 0.42 | 3.16 ± 0.12 | 1.32× | 2.44× | 1.32× | 2.44× | 1.00× |
| `test` | test -f on a file | 1.34 ± 0.55 | 1.34 ± 0.09 | 1.72 ± 0.12 | 3.07 ± 0.17 | 1.28× | 2.29× | 1.28× | 2.29× | 1.00× |
| `test` | test -nt on two files | 1.33 ± 0.36 | 1.47 ± 0.43 | 1.70 ± 0.27 | 3.20 ± 0.64 | 1.28× | 2.41× | 1.15× | 2.17× | 0.90× |
| `test` | test 40-term and/or chain | 1.37 ± 0.20 | 1.42 ± 0.72 | 1.66 ± 0.52 | 3.12 ± 0.16 | 1.21× | 2.28× | 1.17× | 2.20× | 0.96× |
| `timeout` | timeout a command | 1.18 ± 1.59 | 1.13 ± 1.97 | 0.42 ± 1.64 | 3.57 ± 2.17 | 0.35× | 3.03× | 0.37× | 3.15× | 1.04× |
| `timeout` | timeout zero disables | 1.38 ± 0.92 | 0.98 ± 0.69 | 3.02 ± 2.77 | 4.05 ± 1.52 | 2.18× | 2.92× | 3.09× | 4.13× | 1.41× |
| `timeout` | timeout --foreground | 1.91 ± 3.07 | 3.56 ± 1.74 | 3.82 ± 1.62 | 6.55 ± 3.68 | 2.00× | 3.42× | 1.07× | 1.84× | 0.54× |
| `timeout` | timeout -s KILL | 2.56 ± 0.96 | 1.61 ± 1.11 | 2.60 ± 1.11 | 5.97 ± 8.24 | 1.02× | 2.33× | 1.61× | 3.70× | 1.59× |
| `timeout` | timeout -k with a grace period | 2.83 ± 2.02 | 2.91 ± 0.88 | 3.07 ± 0.95 | 4.89 ± 1.18 | 1.08× | 1.73× | 1.06× | 1.68× | 0.97× |
| `timeout` | timeout -v | 2.74 ± 4.20 | 3.25 ± 2.39 | 5.03 ± 1.44 | 7.10 ± 2.02 | 1.84× | 2.60× | 1.55× | 2.19× | 0.84× |
| `timeout` | timeout --preserve-status | 1.72 ± 2.09 | 1.38 ± 4.33 | 1.51 ± 1.97 | 3.17 ± 1.10 | 0.88× | 1.84× | 1.09× | 2.29× | 1.24× |
| `timeout` | timeout a fractional duration | 2.56 ± 0.57 | 2.46 ± 0.75 | 3.24 ± 4.50 | 4.75 ± 0.56 | 1.27× | 1.86× | 1.32× | 1.93× | 1.04× |
| `timeout` | timeout a suffixed duration | 1.68 ± 0.90 | 2.68 ± 0.63 | 3.01 ± 0.55 | 4.43 ± 3.00 | 1.79× | 2.63× | 1.12× | 1.65× | 0.63× |
| `timeout` | timeout a command with arguments | 3.96 ± 1.71 | 4.17 ± 0.89 | 4.61 ± 0.91 | 152.45 ± 21.61 | 1.16× | 38.46× | 1.11× | 36.60× | 0.95× |
| `timeout` | timeout a command not found | 1.42 ± 0.48 | 1.66 ± 1.65 | 2.28 ± 0.59 | 5.14 ± 0.60 | 1.61× | 3.63× | 1.38× | 3.11× | 0.86× |
| `timeout` | timeout an invalid duration | 1.62 ± 3.83 | 1.19 ± 1.10 | 1.52 ± 0.27 | 4.46 ± 0.77 | 0.94× | 2.76× | 1.28× | 3.75× | 1.36× |
| `touch` | touch one file | 0.65 ± 3.07 | 0.72 ± 0.80 | 2.15 ± 0.58 | 2.99 ± 0.88 | 3.33× | 4.62× | 2.99× | 4.15× | 0.90× |
| `touch` | touch 200 files in one run | 6.54 ± 1.56 | 7.83 ± 5.59 | 6.59 ± 1.17 | 6.36 ± 1.18 | 1.01× | 0.97× | 0.84× | 0.81× | 0.83× |
| `touch` | touch -d relative 200 files | 7.13 ± 0.96 | 6.83 ± 0.91 | 6.82 ± 4.65 | 7.78 ± 1.03 | 0.96× | 1.09× | 1.00× | 1.14× | 1.04× |
| `touch` | touch -t 200 files | 6.60 ± 1.48 | 5.58 ± 0.84 | 6.02 ± 0.96 | 8.17 ± 4.85 | 0.91× | 1.24× | 1.08× | 1.46× | 1.18× |
| `touch` | touch -r -d 200 files | 5.90 ± 1.62 | 5.88 ± 0.97 | 7.25 ± 0.57 | 7.18 ± 1.45 | 1.23× | 1.22× | 1.23× | 1.22× | 1.00× |
| `touch` | touch -a -m -h 200 files | 3.48 ± 2.87 | 4.42 ± 0.66 | 4.10 ± 1.43 | 6.40 ± 0.98 | 1.18× | 1.84× | 0.93× | 1.45× | 0.79× |
| `touch` | touch -c 200 missing names | 3.03 ± 0.80 | 2.27 ± 2.68 | 2.57 ± 1.30 | 4.93 ± 0.89 | 0.85× | 1.63× | 1.13× | 2.18× | 1.34× |
| `touch` | touch create and remove 10 files | 6.13 ± 0.84 | 6.21 ± 3.60 | 6.42 ± 0.85 | 8.30 ± 0.92 | 1.05× | 1.35× | 1.03× | 1.34× | 0.99× |
| `tr` | tr 0-9 a-j over a 62 MiB file | 24.73 ± 0.84 | 27.14 ± 1.68 | 49.43 ± 11.11 | 23.49 ± 1.31 | 2.00× | 0.95× | 1.82× | 0.87× | 0.91× |
| `tr` | tr -d 0-4 over a 62 MiB file | 25.08 ± 0.85 | 26.57 ± 0.75 | 73.72 ± 12.06 | 51.74 ± 1.60 | 2.94× | 2.06× | 2.77× | 1.95× | 0.94× |
| `tr` | tr -s 0-9 over a 62 MiB file | 225.26 ± 3.85 | 135.43 ± 10.25 | 952.89 ± 15.80 | 42.97 ± 0.89 | 4.23× | 0.19× | 7.04× | 0.32× | 1.66× |
| `tr` | tr -cd digits over a 62 MiB file | 24.95 ± 1.02 | 27.17 ± 0.75 | 54.65 ± 2.08 | 38.67 ± 2.86 | 2.19× | 1.55× | 2.01× | 1.42× | 0.92× |
| `tr` | tr from a pipe | 27.76 ± 1.05 | 31.47 ± 1.01 | 60.54 ± 1.82 | 32.58 ± 10.89 | 2.18× | 1.17× | 1.92× | 1.04× | 0.88× |
| `true` | true | 1.45 ± 0.28 | 1.29 ± 0.24 | 1.44 ± 0.45 | 3.22 ± 0.12 | 1.00× | 2.23× | 1.12× | 2.50× | 1.12× |
| `truncate` | truncate one file | 3.13 ± 2.36 | 2.85 ± 0.85 | 4.09 ± 0.94 | 6.26 ± 2.65 | 1.30× | 2.00× | 1.43× | 2.20× | 1.10× |
| `truncate` | truncate 200 existing files | 380.28 ± 115.22 | 356.06 ± 44.85 | 420.82 ± 47.55 | 897.84 ± 47.25 | 1.11× | 2.36× | 1.18× | 2.52× | 1.07× |
| `truncate` | truncate 200 files in one run | 26.86 ± 9.85 | 29.87 ± 5.58 | 30.20 ± 1.98 | 36.85 ± 2.74 | 1.12× | 1.37× | 1.01× | 1.23× | 0.90× |
| `truncate` | truncate -s with a suffix | 394.01 ± 50.85 | 362.14 ± 28.33 | 436.29 ± 14.03 | 1011.85 ± 68.61 | 1.11× | 2.57× | 1.20× | 2.79× | 1.09× |
| `truncate` | truncate -s relative | 383.10 ± 40.17 | 388.17 ± 62.89 | 461.16 ± 42.27 | 1053.50 ± 77.12 | 1.20× | 2.75× | 1.19× | 2.71× | 0.99× |
| `truncate` | truncate -s rounding up | 380.30 ± 29.42 | 383.75 ± 45.56 | 444.84 ± 52.33 | 913.99 ± 35.58 | 1.17× | 2.40× | 1.16× | 2.38× | 0.99× |
| `truncate` | truncate -o 200 files | 388.14 ± 45.52 | 398.40 ± 47.44 | 450.47 ± 17.66 | 1052.18 ± 82.07 | 1.16× | 2.71× | 1.13× | 2.64× | 0.97× |
| `truncate` | truncate -r 200 files in one run | 30.85 ± 4.58 | 30.65 ± 7.26 | 28.68 ± 1.62 | 38.40 ± 2.50 | 0.93× | 1.24× | 0.94× | 1.25× | 1.01× |
| `truncate` | truncate -c 200 missing files | 347.00 ± 28.52 | 350.85 ± 42.15 | 415.49 ± 22.64 | 949.59 ± 47.02 | 1.20× | 2.74× | 1.18× | 2.71× | 0.99× |
| `truncate` | truncate 200 missing files under -s | 370.75 ± 14.40 | 378.66 ± 33.90 | 432.16 ± 15.27 | 979.01 ± 24.20 | 1.17× | 2.64× | 1.14× | 2.59× | 0.98× |
| `tsort` | tsort 100k-edge DAG from a file | 45.55 ± 16.76 | 28.44 ± 1.62 | 61.76 ± 1.41 | 42.75 ± 2.40 | 1.36× | 0.94× | 2.17× | 1.50× | 1.60× |
| `tty` | tty from a pipe | 2.51 ± 0.89 | 2.90 ± 3.29 | 2.06 ± 0.91 | 4.44 ± 0.74 | 0.82× | 1.77× | 0.71× | 1.53× | 0.86× |
| `tty` | tty -s from a pipe | 2.76 ± 2.57 | 1.98 ± 1.32 | 3.87 ± 2.10 | 4.82 ± 0.66 | 1.40× | 1.75× | 1.96× | 2.44× | 1.40× |
| `tty` | tty on a terminal | exit 1 | exit 1 | 3.52 ± 1.59 | exit 1 | — | — | — | — | — |
| `uname` | uname | 1.49 ± 0.27 | 1.46 ± 0.15 | 1.77 ± 0.14 | 3.82 ± 2.60 | 1.19× | 2.57× | 1.21× | 2.62× | 1.02× |
| `uname` | uname -a | 1.48 ± 0.09 | 1.64 ± 0.86 | 1.78 ± 0.19 | 3.83 ± 0.24 | 1.20× | 2.59× | 1.09× | 2.34× | 0.90× |
| `unexpand` | unexpand a 42 MiB indented file | 61.16 ± 15.82 | 53.63 ± 2.15 | 866.87 ± 18.29 | 280.35 ± 6.51 | 14.17× | 4.58× | 16.16× | 5.23× | 1.14× |
| `unexpand` | unexpand -a a 42 MiB indented file | 153.86 ± 2.10 | 131.33 ± 12.82 | 906.63 ± 77.97 | 283.58 ± 9.10 | 5.89× | 1.84× | 6.90× | 2.16× | 1.17× |
| `unexpand` | unexpand -t4 a 42 MiB indented file | 158.10 ± 1.75 | 129.27 ± 2.39 | 905.35 ± 47.12 | 287.98 ± 13.78 | 5.73× | 1.82× | 7.00× | 2.23× | 1.22× |
| `unexpand` | unexpand a 32 MiB file with no blanks | 20.34 ± 1.83 | 19.40 ± 1.39 | 882.41 ± 30.63 | 29.86 ± 1.17 | 43.38× | 1.47× | 45.48× | 1.54× | 1.05× |
| `uniq` | uniq over 4M lines in groups of 4 | 80.21 ± 23.59 | 48.25 ± 0.61 | 115.61 ± 5.31 | 93.56 ± 2.31 | 1.44× | 1.17× | 2.40× | 1.94× | 1.66× |
| `uniq` | uniq -c over 4M lines in groups of 4 | 84.97 ± 2.69 | 55.24 ± 3.06 | 140.87 ± 6.31 | 105.08 ± 4.62 | 1.66× | 1.24× | 2.55× | 1.90× | 1.54× |
| `uniq` | uniq -d over 4M lines in groups of 4 | 72.26 ± 1.29 | 48.03 ± 1.08 | 120.78 ± 6.40 | 93.97 ± 1.30 | 1.67× | 1.30× | 2.51× | 1.96× | 1.50× |
| `uniq` | uniq over 4M distinct lines | 78.50 ± 1.36 | 52.24 ± 2.01 | 155.61 ± 4.42 | 115.09 ± 2.07 | 1.98× | 1.47× | 2.98× | 2.20× | 1.50× |
| `uniq` | uniq -u over 4M distinct lines | 79.33 ± 1.62 | 51.12 ± 1.73 | 163.48 ± 5.92 | 122.52 ± 13.88 | 2.06× | 1.54× | 3.20× | 2.40× | 1.55× |
| `uniq` | uniq over 2M 44-byte distinct lines | 60.59 ± 1.73 | 62.50 ± 16.12 | 186.63 ± 23.39 | 74.27 ± 2.73 | 3.08× | 1.23× | 2.99× | 1.19× | 0.97× |
| `uniq` | uniq -f1 -c over 4M lines | 209.31 ± 2.84 | 136.34 ± 51.79 | 226.94 ± 14.08 | 122.74 ± 13.43 | 1.08× | 0.59× | 1.66× | 0.90× | 1.54× |
| `uniq` | uniq from a pipe | 76.33 ± 3.37 | 50.53 ± 1.97 | 119.95 ± 7.95 | 110.73 ± 13.44 | 1.57× | 1.45× | 2.37× | 2.19× | 1.51× |
| `unlink` | unlink one file | 1.18 ± 1.17 | 1.15 ± 1.19 | 2.34 ± 1.02 | 5.68 ± 5.18 | 1.98× | 4.82× | 2.03× | 4.93× | 1.02× |
| `unlink` | unlink 200 files | 405.97 ± 69.01 | 369.53 ± 28.93 | 421.60 ± 15.20 | 998.76 ± 109.84 | 1.04× | 2.46× | 1.14× | 2.70× | 1.10× |
| `users` | users, no database | 1.40 ± 0.33 | 1.50 ± 0.12 | 1.90 ± 0.62 | 3.89 ± 0.65 | 1.36× | 2.77× | 1.27× | 2.59× | 0.94× |
| `users` | users of one login | 1.69 ± 0.78 | 1.56 ± 0.59 | 1.85 ± 0.43 | 5.33 ± 2.23 | 1.09× | 3.14× | 1.19× | 3.42× | 1.09× |
| `users` | users of 4000 logins | 5.92 ± 2.71 | 4.39 ± 1.15 | 3.48 ± 2.74 | 4.79 ± 0.73 | 0.59× | 0.81× | 0.79× | 1.09× | 1.35× |
| `vdir` | 4000 names, no stat | 17.58 ± 0.78 | 15.28 ± 2.36 | 21.82 ± 1.88 | 17.93 ± 0.49 | 1.24× | 1.02× | 1.43× | 1.17× | 1.15× |
| `vdir` | -l over 4000 names | 18.43 ± 3.99 | 15.62 ± 0.86 | 22.40 ± 3.41 | 22.73 ± 2.32 | 1.22× | 1.23× | 1.43× | 1.46× | 1.18× |
| `vdir` | -U (unsorted) over 4000 names | 18.55 ± 6.35 | 15.45 ± 0.90 | 15.89 ± 0.66 | 20.29 ± 4.78 | 0.86× | 1.09× | 1.03× | 1.31× | 1.20× |
| `vdir` | -v (filevercmp) over 4000 names | 22.02 ± 2.47 | 16.30 ± 0.91 | 15.17 ± 0.92 | 45.21 ± 2.34 | 0.69× | 2.05× | 0.93× | 2.77× | 1.35× |
| `vdir` | -t over 4000 names | 18.37 ± 0.63 | 16.06 ± 0.44 | 16.30 ± 3.13 | 19.07 ± 0.55 | 0.89× | 1.04× | 1.02× | 1.19× | 1.14× |
| `vdir` | -S over 4000 names | 18.37 ± 0.64 | 16.59 ± 1.72 | 22.41 ± 0.39 | 18.80 ± 0.60 | 1.22× | 1.02× | 1.35× | 1.13× | 1.11× |
| `vdir` | -C -w 200 over 4000 names | 7.36 ± 1.68 | 6.16 ± 1.29 | 11.19 ± 0.30 | 10.97 ± 1.29 | 1.52× | 1.49× | 1.82× | 1.78× | 1.19× |
| `vdir` | -x -w 200 over 4000 names | 7.36 ± 2.35 | 5.87 ± 0.71 | 11.15 ± 0.41 | 10.98 ± 1.36 | 1.52× | 1.49× | 1.90× | 1.87× | 1.25× |
| `vdir` | -m -w 200 over 4000 names | 6.87 ± 0.25 | 5.37 ± 0.23 | 10.90 ± 0.24 | 10.43 ± 0.42 | 1.59× | 1.52× | 2.03× | 1.94× | 1.28× |
| `vdir` | -i -s over 4000 names | 19.50 ± 0.51 | 18.17 ± 3.83 | 23.72 ± 0.56 | 21.34 ± 2.05 | 1.22× | 1.09× | 1.31× | 1.17× | 1.07× |
| `vdir` | -F over 1500 mixed entries | 10.58 ± 3.33 | 8.80 ± 0.32 | 10.44 ± 0.36 | 11.52 ± 2.19 | 0.99× | 1.09× | 1.19× | 1.31× | 1.20× |
| `vdir` | -l over 1500 mixed entries | 9.48 ± 1.80 | 7.97 ± 0.88 | 9.53 ± 0.32 | 11.08 ± 1.26 | 1.01× | 1.17× | 1.19× | 1.39× | 1.19× |
| `vdir` | --color=always over 1500 mixed | 9.11 ± 2.14 | 8.07 ± 0.58 | 9.55 ± 1.29 | 18.01 ± 0.77 | 1.05× | 1.98× | 1.18× | 2.23× | 1.13× |
| `vdir` | -R over a 40-deep tree | 6.44 ± 0.18 | 5.98 ± 0.33 | 6.48 ± 0.47 | 8.07 ± 0.36 | 1.01× | 1.25× | 1.08× | 1.35× | 1.08× |
| `vdir` | -lR over a 40-deep tree | 6.86 ± 1.66 | 5.46 ± 0.71 | 8.10 ± 1.18 | 8.61 ± 1.01 | 1.18× | 1.25× | 1.48× | 1.58× | 1.26× |
| `vdir` | -b over 4000 names | 15.74 ± 1.96 | 13.95 ± 2.94 | 21.65 ± 3.16 | 17.13 ± 0.56 | 1.38× | 1.09× | 1.55× | 1.23× | 1.13× |
| `vdir` | --quoting-style=shell-escape | 16.31 ± 1.17 | 15.62 ± 4.52 | 22.74 ± 1.16 | 17.33 ± 0.71 | 1.39× | 1.06× | 1.46× | 1.11× | 1.04× |
| `vdir` | --time-style=full-iso -l | 17.20 ± 3.75 | 14.27 ± 1.21 | 19.97 ± 1.94 | 17.41 ± 0.55 | 1.16× | 1.01× | 1.40× | 1.22× | 1.21× |
| `wc` | wc -L of a 62 MiB file | 44.91 ± 7.43 | 34.20 ± 4.60 | 107.15 ± 6.57 | 73.43 ± 8.81 | 2.39× | 1.63× | 3.13× | 2.15× | 1.31× |
| `wc` | wc -c of a 62 MiB file | 1.36 ± 0.05 | 1.31 ± 0.08 | 2.15 ± 0.95 | 4.25 ± 0.58 | 1.57× | 3.11× | 1.64× | 3.24× | 1.04× |
| `wc` | wc of a 62 MiB file | 42.99 ± 3.75 | 47.30 ± 8.93 | 107.39 ± 9.38 | 109.85 ± 3.96 | 2.50× | 2.56× | 2.27× | 2.32× | 0.91× |
| `wc` | wc -w of a 62 MiB file | 41.41 ± 4.88 | 43.78 ± 5.11 | 106.52 ± 6.38 | 75.09 ± 5.26 | 2.57× | 1.81× | 2.43× | 1.72× | 0.95× |
| `wc` | wc -L of a 62 MiB file | 44.91 ± 7.43 | 34.20 ± 4.60 | 107.15 ± 6.57 | 73.43 ± 8.81 | 2.39× | 1.63× | 3.13× | 2.15× | 1.31× |
| `wc` | wc -l from a pipe | 17.27 ± 2.29 | 16.30 ± 1.16 | 26.15 ± 6.86 | 18.62 ± 1.42 | 1.51× | 1.08× | 1.60× | 1.14× | 1.06× |
| `who` | who, no database | 1.42 ± 0.40 | 1.35 ± 0.10 | 2.10 ± 0.27 | 4.01 ± 0.27 | 1.47× | 2.81× | 1.55× | 2.97× | 1.05× |
| `who` | who of one login | 1.52 ± 0.50 | 1.49 ± 0.25 | 2.20 ± 0.29 | 4.22 ± 0.88 | 1.44× | 2.77× | 1.48× | 2.84× | 1.02× |
| `who` | who of 4000 logins | 11.11 ± 0.66 | 9.78 ± 0.97 | 2.36 ± 0.51 | 4.89 ± 1.14 | 0.21× | 0.44× | 0.24× | 0.50× | 1.14× |
| `who` | who -a of 4000 logins | 12.10 ± 2.21 | 10.24 ± 1.63 | 2.78 ± 0.69 | 6.01 ± 2.39 | 0.23× | 0.50× | 0.27× | 0.59× | 1.18× |
| `who` | who -q of 4000 logins | 4.02 ± 1.05 | 2.31 ± 1.46 | 1.36 ± 1.11 | 3.81 ± 0.55 | 0.34× | 0.95× | 0.59× | 1.64× | 1.74× |
| `whoami` | whoami | 2.14 ± 0.53 | 2.08 ± 0.40 | 2.38 ± 0.15 | 5.12 ± 2.59 | 1.11× | 2.40× | 1.14× | 2.46× | 1.03× |
| `yes` | yes | head -c 1G | 468.17 ± 11.50 | 491.61 ± 20.35 | 464.90 ± 17.42 | 604.41 ± 8.89 | 0.99× | 1.29× | 0.95× | 1.23× | 0.95× |
| `yes` | yes 70000-byte line | head -c 1G | 571.20 ± 20.53 | 560.77 ± 25.98 | 562.26 ± 52.40 | 578.79 ± 19.07 | 0.98× | 1.01× | 1.00× | 1.03× | 1.02× |
