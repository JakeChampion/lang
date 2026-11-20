# Fern and Go compiler measurements on x86-64 Linux

At the October 5 revision before the native Go CLI retired, Fern took 26.53 seconds to compile itself. Go took 26.93 seconds to compile the same source. Fern finished first in all four pairs. [Tracking issue #8171](https://github.com/JakeChampion/lang/issues/8171)

The November 20 compiler built its own CLI in a median of 67.79 seconds across three builds. The retired Go compiler cannot compile that source. The shared assembler provides a smaller workload that both toolchains can compile.

| Workload | Go median wall time | Fern median wall time | Fern / Go | Fern revision |
| --- | ---: | ---: | ---: | --- |
| October compiler | 26.93 s | 26.53 s | 0.985 | October |
| November compiler | — | 67.79 s | — | November |
| Shared assembler | 0.859 s | 0.618 s | 0.719 | November |

November Fern finished first in all six assembler pairs. The table reports ratios of medians on this host. The file lists every timed build in run order, with wall time, process CPU time, peak memory and the executable's SHA-256. [Build samples](samples.tsv)

## Workloads

The October revision is `071a99feacc3`. Its Go CLI still calls the native x86-64 code generator. The next change sends target builds to the Fern compiler. The November revision is `0dca14c50705`. [CLI handoff](https://github.com/JakeChampion/lang/commit/5ef7ab235787b9ca9ff573db5e5585167a846e0a)

- **October compiler.** Both October compilers read `examples/self_host/fern.fern` and used the October standard library. Each wrote a linked x86-64 Linux executable.
- **November compiler.** The November Fern compiler built `compiler/fern_main.fern` with its standard library and the assets from `scripts/fern-embed-stage`. This measures the shipped CLI, including its embedded library.
- **Shared assembler.** The October Go compiler and the November Fern compiler each built the same driver and the unmodified October `examples/self_host/x86_native.fern`. The driver has no standard-library imports. Each executable assembled an addition and a return, then checked all four bytes. The measured time covers the compiler process. The output check ran separately.

The October Go compiler rejected the November compiler source at the compound assignment in `compiler/fern.fern`. November Fern rejected the October compiler source. With `-stdlib` set to the October library it reported a missing `core/primitive_text`. With the November library it rejected non-ASCII byte escapes in the historical source. These results leave the whole-compiler comparison across revisions open.

## Method

The host ran Linux 6.18.57 on x86-64 AMD EPYC 9V74 with a four-CPU quota. The Go compiler used Go 1.27.4, `GOMAXPROCS=4` and `FERN_LOWER_JOBS=4`. All timed builds used `-target x86-64-linux -o OUTPUT` with default optimization settings. The compiler processes ran one at a time. A monotonic clock measured process wall time. Each build overwrote the same output path.

Build the October Go CLI with `go build -o go-native ./cmd/fern` at the October revision. For October Fern, start with the x86-64 Linux executable from that revision's `bootstrap/stage0.lock`. Its SHA-256 starts with `a106f735135b`. Use each successive compiler to build the next one. Run this command from the October checkout.

```sh
"$compiler" -target x86-64-linux -o "$output" examples/self_host/fern.fern internal/stdlib
```

The second build produced the measured October Fern compiler. For the November compiler, stage the standard library and use the following arguments twice, first with a working Fern compiler and then with its output. Run this command from the November checkout.

```sh
"$compiler" -target x86-64-linux -embed "$embed_directory" -o "$output" compiler/fern_main.fern internal/stdlib
```

The measured November compiler had SHA-256 `4a7f75c3fb99215f9f1e4ab0c9d07b56db4d8fe396f64ed5ffa409c0caf5eb8f`. Its embedded VERSION was `0dca14c507` followed by a newline.

The whole-compiler comparison used four pairs, with Go first in odd pairs and Fern first in even pairs. The shared assembler used six pairs, with Fern first in odd pairs. Each output compiler built a program that summed 3, 5 and 7. Each resulting program exited with 15. Within each series, the output SHA-256 stayed the same across repetitions.

Place this driver beside the October `x86_native.fern` to repeat the assembler comparison.

```fern
import "./x86_native";

function main(): i32 {
  let a = x86_native.x86_gas_assemble(".text\naddl $3, %eax\nret\n");
  if (a.unknown.len() > 0 || a.text.len() != 4) { return 1; }
  if (a.text[0] != 131 || a.text[1] != 192 || a.text[2] != 3 || a.text[3] != 195) { return 2; }
  return 0;
}
```
