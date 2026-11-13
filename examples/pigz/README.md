# pigz

A Fern port of [pigz](https://zlib.net/pigz/), which compresses and
decompresses gzip files. It compresses on all CPUs.

```
$ pigz file              # writes file.gz and removes file
$ pigz -d file.gz        # writes file and removes file.gz
$ pigz -9 -k file        # the smallest output, and file stays
$ pigz -c file > out.gz  # writes to the standard output
$ pigz -t file.gz        # tests file.gz
$ pigz < in > in.gz      # with no file, the standard input
```

## Build and run

```
$ fern -O -target x86-64-linux -o pigz examples/pigz/pigz.fern   # or arm64-linux
```

## Options

| Option | Long form | Effect |
| --- | --- | --- |
| `-c` | `--stdout`, `--to-stdout` | Write to the standard output and keep the input. |
| `-d` | `--decompress`, `--uncompress` | Decompress. |
| `-f` | `--force` | Replace an output file that exists. |
| `-k` | `--keep` | Keep the input file. |
| `-t` | `--test` | Test the integrity of each file and write nothing. |
| `-1` to `-9` | `--fast`, `--best` | The compression level. The default is 6. |
| `-p N` | `--processes N` | Compress with N processes. The default is one for each CPU. |

Options can come before and after the file names. `--` ends the options, and
`-` is the standard input. Letters can share one dash, for example `-dc`.

## Parallel compression

The input goes in blocks of 128 KiB, as in pigz. Each block has the 32 KiB
before it as its dictionary. Each block but the last ends on a byte boundary
with an empty stored block. So the blocks join into one deflate stream.

Each `std/worker` worker compresses one run of consecutive blocks,
and the parent compresses the last run. The output does not depend on the
count of processes, except that one process gives one block. An input of
128 KiB or less is one block.

On macOS and on wasm32-wasi, `std/worker` runs the workers inline, so the
compression uses one CPU there (#15584).

## Compatibility

The output decompresses with `gzip -d` and `pigz -d`. The port decompresses
what both write, with each member of a multi-member file. As pigz does, the
port ignores bytes after the last member.

The header keeps the name of the input file and its modification time, as
pigz writes them. The output file gets the mode of the input file. The
messages and the exit codes are those of pigz 2.8:

- `pigz: abort: ...` and exit 22 for an invalid option or level.
- `pigz: skipping: ...` and exit 1 for a file that does not exist, a
  directory, an output file that exists, or a stream that does not decode.

The port has no level 0 and no level 11. `-0` and `-11` abort with exit 22.

## Tests

- `gz_test.fern`: the options, the output names, the header, the messages
  for corrupt streams, and the parallel compression.
  `TestSelfHostExamplePigzUnit` runs it on x86-64, arm64 and wasm32-wasi
  with the leak census.
- `TestSelfHostExamplePigzCLI` runs the CLI on x86-64 and arm64. `gzip` is
  the oracle: it decompresses the output of the port at levels 1, 6 and 9,
  and the port decompresses the output of `gzip`. With `-p 1`, `-p 2` and
  `-p 4`, gzip decompresses each output, and `-p 2` and `-p 4` give the same
  bytes. It also checks the files that each option writes and removes, and
  each message and exit code.
