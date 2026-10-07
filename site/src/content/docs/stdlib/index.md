---
title: Standard library
description: Reference for every module in the standard library.
sidebar:
  order: 0
---

One page per module, listing its public functions, structs, enums,
traits and constants with their doc comments. Every page is generated
from the Fern source it documents and rebuilt with the site, so it can't
drift from the code — to fix a description, edit the doc comment above
the declaration ([how](../contributing/)).

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
| Text | [`string`](string/), [`regex`](regex/), [`format`](format/), [`unicode`](unicode/), [`utf8`](utf8/), [`textwrap`](textwrap/), [`table`](table/) |
| Data and encoding | [`json`](json/), [`csv`](csv/), [`base64`](base64/), [`hex`](hex/), [`url`](url/), [`uuid`](uuid/), [`deflate`](deflate/) (gzip and zlib decoding), [`crypto`](crypto/) (digests, HMAC, key derivation) and its `crypto/*` submodules |
| Collections | [`array`](array/), [`map`](map/), [`set`](set/), [`iter`](iter/), [`sort`](sort/), and the persistent [`pvec`](pvec/), [`pmap`](pmap/), [`ordmap`](ordmap/) |
| Errors | [`option`](option/), [`result`](result/), [`error`](error/) |
| Numbers | [`math`](math/), [`rand`](rand/), [`bigint`](bigint/), [`i32`](i32/), [`i64`](i64/), [`float`](float/) |
| Files, I/O and time | [`io`](io/), [`path`](path/), [`cli`](cli/), [`log`](log/), [`time`](time/), [`tz`](tz/), [`signal`](signal/), [`async`](async/) |
| Networking | [`http`](http/), [`serve`](serve/), [`fetch`](fetch/), [`tcp`](tcp/), [`dns`](dns/), [`net`](net/) |
| Testing | [`test`](test/), [`bench`](bench/), [`fuzz`](fuzz/), [`sim`](sim/) (deterministic simulation of async code) |

Some modules need a capability a target does not provide — sockets on
`wasm32-wasi-http`, for example — and using one there is a build error.
`fern -targets` lists what each target provides.

The source is under [`internal/stdlib/std/`][std] and
[`internal/stdlib/core/`][core] on GitHub.

[std]: https://github.com/JakeChampion/lang/tree/main/internal/stdlib/std
[core]: https://github.com/JakeChampion/lang/tree/main/internal/stdlib/core
