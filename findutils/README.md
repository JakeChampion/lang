# findutils

GNU find and xargs, written again in Fern. The ports give the same output,
the same error messages and the same exit codes as GNU findutils 4.9.0.

The ports use the shared library of the coreutils ports:
`coreutils/lib/gnu.fern` for the options and the messages,
`coreutils/lib/bre.fern` for `-regex`, and `coreutils/lib/fnmatch.fern`,
`pwdb.fern`, `mode.fern`, `timefmt.fern`, `datetime.fern` and `selinux.fern`
for the tests of find. `findutils/lib/cmdline.fern` holds what find and
xargs share. The parity gate is the coreutils corpus in
`internal/testing/coreutils/`, in `find_test.go` and `xargs_test.go`.
`docs/COREUTILS.md` has the rules of parity and the method.

## Build

```
$ fern -O -target x86-64-linux -o find findutils/find.fern     # or arm64-linux, arm64-darwin
$ fern -O -target x86-64-linux -o xargs findutils/xargs.fern
$ ./find . -name '*.fern' -print0 | ./xargs -0 -n 50 wc -l
```

## What find does

- The options before the paths: `-H`, `-L`, `-P` and `-O`.
- The options: `-depth` and `-d`, `-maxdepth`, `-mindepth`, `-mount` and
  `-xdev`, `-noleaf`, `-ignore_readdir_race`, `-noignore_readdir_race`,
  `-files0-from`, `-daystart`, `-follow`, `-regextype`, `-warn` and
  `-nowarn`.

The tests and the actions:

- The tests of a name: `-name`, `-iname`, `-path`, `-ipath`, `-wholename`,
  `-iwholename`, `-lname`, `-ilname`, `-regex` and `-iregex`, with each type
  of `-regextype`.
- The tests of the type, the links and the owner: `-type` and `-xtype` with
  a list of types, `-links`, `-inum`, `-samefile`, `-user`, `-group`,
  `-uid`, `-gid`, `-nouser` and `-nogroup`.
- The tests of the size and the permissions: `-size` with each unit,
  `-empty`, `-perm` with the `-` and `/` forms and a symbolic mode,
  `-readable`, `-writable` and `-executable`.
- The tests of the times: `-newer`, `-anewer`, `-cnewer`, `-newerXY`,
  `-atime`, `-ctime`, `-mtime`, `-amin`, `-cmin`, `-mmin` and `-used`.
- The other tests: `-fstype`, `-context`, `-true` and `-false`.
- The operators `!`, `-not`, `-a`, `-and`, `-o`, `-or`, `,` and the
  parentheses, with the precedence and the error messages of GNU find.
- The actions: `-print`, `-print0`, `-printf` with each directive and
  escape, `-fprint`, `-fprint0`, `-fprintf`, `-ls`, `-fls`, `-prune`,
  `-quit`, `-delete`, `-exec` and `-execdir` with `;` and `+`, `-ok` and
  `-okdir`.

The walk goes in the order of readdir. In a directory of more than 10000
entries, it goes in the order of the inode numbers, as fts does. It opens each directory by its name
in the directory above it, and it reads the type of each entry from
readdir. A test of the name or of the type thus needs no stat.

## What xargs does

The options are `-0`, `-a`, `-d`, `-E`, `-e`, `-I`, `-i`, `-L`, `-l`,
`-n`, `-s`, `-x`, `-P`, `-p`, `-r`, `-t`, `-o`, `--show-limits` and
`--process-slot-var`. SIGUSR1 adds one command to `-P`, and SIGUSR2 takes
one away, as in GNU xargs. With `-P`, xargs waits for the first command
that ends, and then it starts the next command. `-I` replaces the string
in each initial argument after the command name. The exit status is one of
these:

- 0 when each command exits with 0.
- 123 when a command exits with a status from 1 to 125.
- 124 when a command exits with 255.
- 125 when a signal stops a command.
- 126 when a command cannot run.
- 127 when there is no command of that name.
- 1 for an error in the options or the input.

A command that exits with 255, a command that a signal stops, a command
that cannot run and an error each stop xargs. xargs then waits for the
commands that still run, as GNU xargs does at exit. If one of them exits
with 255 or a signal stops it, xargs stops at once with 124 or 125.
Otherwise, when a command exited with a status from 1 to 125, the status
is 123. At the end of the input xargs waits in the same way. Thus a command
that exits with 255 there gives 124, also after a command that failed.

## What is not in the ports

- `find -D` stops with an error.
- `find -O` does not change the order of the tests. GNU find can test a
  name before a size at `-O1` and up. The output is the same. But when a
  stat of a file fails, one program can report it and the other not.
- On wasm32-wasi there are no processes. `-exec` and its kin stop find with
  an error, and xargs stops at once. `-fstype` names each file system
  `unknown`, `-readable` and its kin are false, and `%Z` has no context.

## Speed

The tree has 501000 files in 5511 directories on ext4, on Linux x86-64
with 4 cores. Each time is the best of 11 runs, with the runs of the two
programs in turn, 2026-11-15. The cache was warm. A ratio above 1 means
that the port is faster. The load of the machine was 4 to 7, so a
difference of less than 5% is noise. The user time is the time of the
program outside the kernel.

| Workload | GNU | Fern | Ratio | GNU user | Fern user |
|---|---|---|---|---|---|
| `find . -name '*.go'` | 0.263 s | 0.200 s | 1.32 | 0.117 s | 0.051 s |
| `find . -type f -size +10k` | 0.861 s | 0.887 s | 0.97 | 0.143 s | 0.127 s |
| `find . -printf '%p %s\n'` | 0.901 s | 0.919 s | 0.98 | 0.200 s | 0.147 s |
| `find . -newer ref` | 0.855 s | 0.870 s | 0.98 | 0.093 s | 0.116 s |
| `find . -print0 \| xargs -0 -n 100 true` | 5.124 s | 5.178 s | 0.99 | | |

A test of the name needs no stat, and there the port is faster. A test of
the size or the time needs a stat of each file, and then the kernel uses
80% of the time. The walk no longer copies its entry for each stat. Thus
the user time of the port is at most that of GNU find on two of the three
rows that stat each file.

The rest of the difference is in the kernel. The port reads each stat with
statx, and GNU find reads it with newfstatat. A test of the two calls on
the files of one directory gives 426 ns for statx and 412 ns for
newfstatat. Over 501000 files that is about 0.02 s. The runtime uses statx
because the birth time of `ls` and of `%B` comes from it.

The table below compares xargs `-P` with GNU xargs 4.9.0 on the same
machine. Each value is the median of 11 runs. The total CPU time includes
the commands. The xargs CPU time is the time of xargs alone. Other programs
used the machine during the runs, so the wall times change by 10% or more
from run to run.

| Workload | GNU wall | Fern wall | GNU CPU | Fern CPU | GNU xargs CPU | Fern xargs CPU |
|---|---|---|---|---|---|---|
| `seq 2000 \| xargs -P 4 -n 1 true` | 3.42 s | 3.01 s | 2.25 s | 2.14 s | 157 ms | 144 ms |
| 400 commands that sleep 0 to 40 ms, `-P 4` | 2.34 s | 2.25 s | 0.56 s | 0.54 s | 51 ms | 50 ms |
| one command that sleeps 1 s, then 150 of 10 ms, `-P 2` | 1.53 s | 1.54 s | 0.21 s | 0.20 s | 21 ms | 19 ms |
| `seq 2000 \| xargs -n 1 true` | 1.88 s | 1.89 s | 1.86 s | 1.77 s | | |

The row of `-n 1` is from 2026-11-15: the best of 9 runs in turn, with no
`-P`. The two wall times are equal within the noise, and their medians are
2.42 s and 2.39 s. callgrind counts 658000 instructions in GNU xargs and
648000 in the port for 200 commands. The port has 188 kB of resident
memory and GNU xargs 1904 kB, so a fork of the port is no slower.

xargs waits for the first command that ends, so a slow command does not
stop the start of the next ones. xargs reads the environment one time and
not again for each command.
