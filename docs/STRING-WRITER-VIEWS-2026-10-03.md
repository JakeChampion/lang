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
and release it afterward. The checked expression and escape analysis are
unchanged. Named sources keep their existing owner.

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

## Validation status

The Linux Go interpreter, native and SSA, WASM and primary target groups pass,
as do the buffered-I/O and byte-view regression groups. The full unit suite
and all lint gates pass. The native Go fixture and census group was rerun
after making its emitter selection explicit; it passes in 1.669 seconds.
Darwin, fresh bootstrap reproduction, actual-compiler probes, cache isolation
and controlled measurements are pending at initial publication.

## Scope

This removes the owned-array copy for whole string byte views. Primary
byte-view slicing still copies. Avoiding that cost for partial writes remains
separate work; this change does not claim that every range is zero-copy or
that #5714 is complete.
