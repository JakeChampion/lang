---
title: Standard library
description: Reference for every module in the standard library.
sidebar:
  order: 0
---

One page per module lists its public functions, structs, enums, traits, and constants.
The generator reads declarations and documentation from the Fern source.
Edit the source comment to correct a description ([how](../contributing/)).
The [library guide](https://github.com/JakeChampion/lang/blob/main/docs/STDLIB.md)
covers conventions across modules.

## Importing a module

A program sees only what it imports. Modules live under two prefixes:
`std/` for the library proper and `core/` for the foundations it is
built on (iterators, comparison traits, maps, big integers). Calls into
a module are qualified by its last path segment, or by the name given
with `as`; methods a module adds to a type, such as the string methods
in `std/string`, are called on the value.

```fern
import "std/string";
import "std/regex";
import "core/iter" as it;

function main(): i32 {
    let words: string[] = "alpha beta gamma".split(" ");
    print(words.len().to_string());                         // 3
    print(regex.regex_match("^b", words[1]).to_string());   // true
    print(it.sum(it.of([1, 2, 3])).to_string());            // 6
    return 0;
}
```

## By area

The sidebar groups every module the same way.

| Area | Start with |
| ---- | ---------- |
| Text | [`string`](string/), [`regex`](regex/), [`format`](format/), [`unicode`](unicode/), [`utf8`](utf8/), [`textwrap`](textwrap/), [`table`](table/), [`glob`](glob/), [`peg`](peg/) (parsing expression grammars), [`strdist`](strdist/) (edit distances), [`ansi`](ansi/) |
| Data and encoding | [`json`](json/), [`csv`](csv/), [`protobuf`](protobuf/), [`base64`](base64/), [`base32`](base32/), [`hex`](hex/), [`url`](url/), [`uuid`](uuid/), [`semver`](semver/), [`hash`](hash/), [`deflate`](deflate/) (gzip and zlib), [`zstd`](zstd/), [`brotli`](brotli/) (decoding), [`crypto`](crypto/) (digests, HMAC, key derivation) and its `crypto/*` submodules |
| Collections | [`array`](array/), [`map`](map/), [`set`](set/), [`iter`](iter/), [`sort`](sort/), [`ndarray`](ndarray/) (n-dimensional arrays), and the persistent [`pvec`](pvec/), [`pmap`](pmap/), [`pset`](pset/), [`ordmap`](ordmap/), [`ordset`](ordset/) |
| Errors | [`option`](option/), [`result`](result/), [`error`](error/) |
| Numbers | [`math`](math/), [`rand`](rand/), [`bigint`](bigint/), [`i32`](i32/), [`i64`](i64/), [`f64`](f64/), [`simd`](simd/) (one kernel over scalars, `Dual` autodiff and `DoubleDouble`) |
| Files, I/O and time | [`io`](io/), [`io_buffered`](io_buffered/), [`stream`](stream/), [`path`](path/), [`cli`](cli/), [`dotenv`](dotenv/), [`log`](log/), [`time`](time/), [`tz`](tz/), [`signal`](signal/), [`errno`](errno/), [`async`](async/) |
| Networking | [`http`](http/) and its `http/*` submodules, [`serve`](serve/), [`fetch`](fetch/), [`metrics`](metrics/), [`tcp`](tcp/), [`dns`](dns/), [`net`](net/), and TLS 1.3 in the `tls/*` submodules — the [HTTP guide](../http/) walks through them |
| Testing | [`test`](test/), [`bench`](bench/), [`fuzz`](fuzz/), [`sim`](sim/) (deterministic simulation of async code), [`sim_fetch`](sim_fetch/) (a scripted network), [`sim_platform`](sim_platform/), [`mock_platform`](mock_platform/) |
| Interop | [`jni`](jni/) (Java Native Interface) |

Some modules need a capability a target does not provide — sockets on
`wasm32-wasi-http`, for example — and using one there is a build error.
`fern -targets` lists what each target provides.

The source is under [`internal/stdlib/std/`][std] and
[`internal/stdlib/core/`][core] on GitHub.

[std]: https://github.com/JakeChampion/lang/tree/main/internal/stdlib/std
[core]: https://github.com/JakeChampion/lang/tree/main/internal/stdlib/core
