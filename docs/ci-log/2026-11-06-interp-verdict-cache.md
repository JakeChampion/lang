# Interpreter verdicts for the stdlib suites

Status: [record] measurements of 2026-11-06 and the change they drove.

The five `selfhost-stdtest` legs ran 3 to 27 minutes in run
[42267280766](https://github.com/JakeChampion/lang/actions/runs/42267280766),
69 runner minutes in all. Each case runs the suite under the Go interpreter,
then compiles it with the self-host compiler and runs the binary. The
interpreter is most of the cost:

| Suite | Go interpreter | Self-host compile | Binary run |
| --- | ---: | ---: | ---: |
| `opus_packet` | 32.6 s | 3.0 s | 0.03 s |
| `opus_pitch_doubling` | 65.4 s | 2.6 s | 0.36 s |
| `mitigations` | 73.3 s | 2.6 s | 0.00 s |
| `quic_conn` | 114.7 s | 8.4 s | 0.05 s |

Measured on the 4-core dev container. The x86-64 and arm64 legs interpret the
same suites, and the wasm leg interprets the crypto and TLS suites again.

`e2eharness.InterpVerdict` now keeps each verdict, the stdout and the exit
status, in `FERN_SELFHOST_BUILD_CACHE`. The key hashes the files of the Go
packages that `cmd/fern` links. It also hashes the suite's relative imports
and the `FERN_*` environment. It leaves out the `compiler`
package, whose embedded self-host sources `-interp` does not read. A
compiler-only change therefore keeps every verdict. Of the last 76 merges on
main, 23 changed Go code that the interpreter links, and 16 changed the stdlib.

When `FERN_BIN` is set, the harness bypasses the verdict cache. The binary
suite executes that external program, which can change at the same path.
The environment value alone cannot identify its behavior.

Each `selfhost-stdtest` leg restores and saves the verdict files through the
Actions cache. With a warm cache, four cases took 10.8 seconds in place of
128 seconds on the dev container.
