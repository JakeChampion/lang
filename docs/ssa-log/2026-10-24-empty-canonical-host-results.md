# Empty canonical host results

The default WASI component receives environment and argument buffers through
the canonical ABI. A zero-length result can point at the aligned heap position
without a heap allocation. A raw free for that pointer therefore adds an unmatched
free to the allocation census.

Environment cleanup checks the key length, value length and list count.
It frees each host buffer only when that buffer has a positive length or count.
Argument cleanup checks the string length and list count. The return areas retain their ordinary cleanup. The
raw allocator and preview 1 cleanup keep their contracts.

The regression executes default components with empty environment lists, empty
values and empty arguments. Each case repeats the operation three times under
strict IR, sanitizer and allocation checks. Preview 1 scratch tests
continue to pass. See the [full evidence](../benchmarks/system-api-names-2026-10-24/README.md).
