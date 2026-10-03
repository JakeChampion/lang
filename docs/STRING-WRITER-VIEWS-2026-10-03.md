# Writer byte views

`Writer.write_bytes` and `Writer.write_some_bytes` now borrow `[u8]`. Owned
arrays lend automatically, and callers can pass a string's `as_bytes()` view
without copying it to an owned array. The host consumes the bytes before the
call returns; neither method keeps a reference to the input.

`write_bytes` completes short writes or returns an error. `write_some_bytes`
returns the count from one host write, including zero. Empty writes still
reach the host, so a closed descriptor reports an error even for empty input.
The Preview 2 partial-write limit remains 4096 bytes.

## Representation and ownership

The Go bootstrap reads its existing data/length view header. The primary
compiler decodes its packed-array or tagged-string representation once before
the write loop. Native addresses keep their pointer width. The diagnostic
word-array path retains its scratch packing. A runtime cache-key change keeps
units compiled against the old argument contract out of the new runtime.

Automatic lending exposed a bootstrap cleanup gap: a temporary array became
a view, whose header was released, while the source array was left behind.
The lowering now uses the existing owned-argument staging and copying-builtin
contract to keep an immediate view's temporary source alive through the call
and release it afterward. The checked expression is unchanged. Named sources
keep their existing owner.

The bootstrap deliberately uses immortal stream handles and error boxes. Its
inline-string spill also has the existing #8408 leak. Bootstrap census tests
therefore compare a no-write control with repeated writes of owned arrays,
temporary arrays, subviews and heap-string views. They require identical live
bytes and outstanding block counts. Before the cleanup fix, twenty writes of
an array literal left 672 bytes live on Go WebAssembly, compared with 32 bytes
for the handle alone; after the fix they leave the same 32 bytes.

The primary compiler requires a fully balanced census for the complete Writer
fixture, including short strings, fresh string sources, partial UTF-8 byte
sequences and closed-handle errors. The output fixture also covers every byte
value, nonzero-offset array views, `str` views, empty views and held aliases.

## Validation

At `ff029586b`, the Linux Go interpreter, native and SSA, WASM and primary
target groups pass,
as do the buffered-I/O and byte-view regression groups. The full unit suite
and all lint gates pass. The native Go fixture and census group was rerun
after making its emitter selection explicit; it passes in 1.669 seconds.
Darwin Go and primary groups pass in 2.490 and 43.525 seconds. The fresh
bootstrap takes 37, 29 and 14 seconds for stages 1, 2 and 3. Stages 2 and 3
are byte-identical at 12,877,921 bytes, SHA-256
`edb6ef84389dad801d421b58c4b4280439fced776e9ba248fe0670ff872ac094`.

That reproduced compiler passes the 16,657-byte output fixture on Darwin,
core WASM, components and the interpreter. Checked Darwin reports 46
allocations and 46 frees; core WASM reports 55 and 55. Both finish with zero
live bytes. Native and WASM per-module cache tests reject the old units,
reuse newly cached units and produce the same output as clean compilation.

## Upstream integration

After integrating `main` at `09ffd368f`, native bootstrap tests explicitly
invoke the Go CLI with `-backend flat`. This avoids depending on the retired
`emitLeakCheck` helper or accidentally testing the primary compiler twice.
The heap-string census source concatenates function parameters so constant
folding cannot replace it with a literal.

The integrated Go target/census group passes in 28.741 seconds, the primary
Writer group in 36.987 seconds and related buffered-I/O/view tests in 87.933
seconds. All lint gates pass. Darwin Go and primary groups pass in 2.376 and
43.316 seconds. A fresh bootstrap takes 37, 28 and 14 seconds; stages 2 and 3
match at 12,877,889 bytes, SHA-256
`fc15892a54e9f5d5017cd0b3ad748b31b6eb30b012237d136d6e6ac2b6e7ec1a`.
Actual Darwin, core WASM, component and interpreter probes pass again, with
the same balanced native/core census counts above. Native and WASM cache
isolation checks pass again. The earlier full unit
pass belongs to `ff029586b`; full integrated CI remains a merge gate.

CI on `68623a892` found two Go WASM buffered-writer census failures on both
runner architectures. After 200 flushes, the first reports 605 allocations
and 404 frees; the fresh-byte argument fixture reports 461 allocations,
203 frees and 7,232 live bytes. These tests were absent from the targeted
bootstrap selection above. The PR cannot merge until the regression is
repaired and the full current-head checks pass.

The cause was the same automatic array-to-view wrapper at a different
boundary. A helper borrowing an array for Writer previously received the
copying-builtin parameter credit. After the signature change, that credit
stopped at the view wrapper. Callers such as `BufWriter.flush` then left their
temporary arrays behind. The bootstrap now preserves the existing credit
through an immediate array view at those synchronous-copy positions only.
Returned views and views passed to retaining callees keep their refusal.
Four focused positive cases fail before the repair and pass afterward;
escaping-view controls pass in both versions. Both failing WASM census tests
also pass locally after the repair. The frozen Linux regression pair passes
in 0.255 seconds, the expanded Writer/BufWriter target group in 31.646 seconds,
and the primary regression group in 95.372 seconds. Full unit tests and all
lint gates pass. Darwin Go Writer/BufWriter tests pass in 2.658 seconds.
All 5,920 Go/Fern files match the validated snapshot. The 211 primary compiler
and standard-library files are unchanged from the reproduced `68623a892`
snapshot, so its fixed-point and artifact evidence above still applies.
Full current-head CI and required approval remain merge gates.

## Controlled measurements before upstream integration

Both benchmark variants use the reproduced `ff029586b` compiler above. They
write the same 4096-byte string to `/dev/null`, using either `data.bytes()` or
`data.as_bytes()`, and check the retained alias after the loop. The pilot
uses 256 calls; the full run changes only the count to 32,768. Each variant
has two warmups and seven alternating timed samples, including process
startup. Census runs are separate from timing. Task-owned heavy jobs were
idle; the rest of the desktop was not isolated.

| Input | Median | Sample range | Allocations |
| --- | ---: | ---: | ---: |
| Owned copy | 18.391 ms | 18.129-19.859 ms | 65,542 |
| Borrowed view | 12.931 ms | 12.837-13.891 ms | 32,774 |

The ranges do not overlap. The view removes one array allocation per call;
both runs free everything. Both native files are 49,985 bytes. The view
program's code is 22,608 bytes versus 23,572 for the copy; unwind data is
3,916 versus 4,060 bytes, and ordinary data is 3,864 bytes in both.

A separate ABI comparison compiles the same owned-array program with both
Writer methods using the parent and new compilers. The parent compiler is
SHA-256 `44f78472a119fc30239f5d6babf96fc54cbfe2d83cb1d3836e7045dfc510fb89`.
Both versions produce identical output on Darwin and core WASM. Native file
size stays 33,473 bytes; code grows from 11,864 to 12,576 bytes and unwind
data from 1,228 to 1,444 bytes for view decoding and its call lifetimes.
Data stays 3,632 bytes. Core WASM grows from 6,802 to 7,354 bytes.

The compiler itself grows from 12,861,377 to 12,877,921 bytes. Its code grows
by 1,312 bytes, unwind data by 216 and data by 768. Mach-O load commands
attribute the file growth to a 16,384-byte text-segment alignment step and
160 bytes of link-edit data. No size baseline changes.

## Scope

This removes the owned-array copy for whole string byte views. Primary
byte-view slicing still copies. Avoiding that cost for partial writes remains
separate work; this change does not claim that every range is zero-copy or
that #5714 is complete.
