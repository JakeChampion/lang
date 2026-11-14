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
with 4 cores. Each time is the best of 7 runs. A cold run drops the page
cache, the dentries and the inodes first. A ratio above 1 means that the
port is faster.

| Workload | GNU warm | Fern warm | Ratio | GNU cold | Fern cold | Ratio |
|---|---|---|---|---|---|---|
| `find . -name '*.go'` | 0.249 s | 0.185 s | 1.34 | 0.693 s | 0.602 s | 1.15 |
| `find . -type f -size +10k` | 0.777 s | 0.801 s | 0.97 | 2.655 s | 2.643 s | 1.00 |
| `find . -printf '%p %s\n'` | 0.832 s | 0.854 s | 0.97 | 2.694 s | 2.751 s | 0.98 |
| `find . -newer ref` | 0.798 s | 0.807 s | 0.99 | 2.789 s | 2.644 s | 1.06 |
| `find . -print0 \| xargs -0 -n 100 true` | 4.821 s | 4.985 s | 0.97 | 4.902 s | 5.252 s | 0.93 |

Other programs used the machine during the runs, so a difference of less
than 5% is noise. A test of the name needs no stat, and there the port is
faster. A test of the size or the time needs a stat of each file.

In that case the kernel uses 80% of the time. The two programs make the
same system calls, so the time is the same. The port uses more user time
than GNU find: 0.12 s to 0.15 s against 0.09 s to 0.11 s. Most of it is
the copy of the entry of the walk for each file. The pipeline with xargs
spends its time in the 5000 runs of `true`.
