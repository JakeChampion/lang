These measurements qualify the short-match repair for #13347.

The matcher keeps bounded chains for both hashes. A packed ring stores both predecessor links in one 64-bit slot per history position. A short-chain search runs when the four-byte search finds no match. Every search stays inside the requested window.

| Tile | Before the four-byte change | Main | Candidate |
| --- | ---: | ---: | ---: |
| z0 | 23,765 | 24,123 | 23,640 |
| z2 | 30,744 | 31,684 | 30,679 |
| z14 | 77,465 | 82,706 | 77,300 |
| z38 | 90,609 | 95,792 | 90,509 |

All four filtered image streams are byte-identical across main and candidate. PNG chunk CRCs and Python zlib decoding are checked. The historical matcher reproduces the original sizes on the same inputs. Its compatibility edits only rename the error and CRC32 APIs.

On the 12-file Silesia corpus, output totals 68,224,127 bytes on main and 68,201,907 for the candidate. Seven files grow. After the table-fill correction, the sum of the per-file encode medians is 3.508 seconds on main and 3.690 seconds for the candidate. The historical matcher takes 6.514 seconds. Each median covers five native calls on the same ARM64 Darwin host. These are individual paired runs, not a claim about a stable timing distribution.

The libdeflate 1.24 level-6 reference produces 67,509,984 bytes. Its compressor and output buffer are allocated before the timed calls. Fern allocates its encode buffers inside the calls. Both implementations decode the candidate/reference streams, and Python independently decodes every stream.

The mixed-block fixture shrinks from 17,096 to 17,095 bytes. The gzip fixture grows from 7,818 to 7,819 bytes and decodes to the same 70,890 bytes. The exact size assertions record these streams. No performance ceiling increases or tolerance changes.

After the table-fill correction, the z0 probe uses 196 allocation events against 290 on main. Its arena bump falls from 1,668,432 to 1,660,776 bytes. The gzip probe uses 145 events against 239; its bump falls from 1,596,920 to 1,425,336 bytes. The probes require balanced process counts and zero live bytes. These counters do not establish peak RSS.

The fixed native compiler is the #13995 fixed point, SHA256 `ed765f71669c60a41766716f12a815bb018b64b99547ac0a988444e78e4df39d`. Full inputs, hashes, timings and compatibility edits are in [measurements-after-fill.json](measurements-after-fill.json). The [initial measurements](measurements.json) and [initial qualification](qualification.json) preserve the source before the table-fill correction.

## Correction after CI

The first head failed both instruction-count gates: 44,732,361 ARM64 instructions against 44,105,319, and 43,808,348 x86 instructions against 43,182,612. CI run `41315869975` remains a failed and cancelled run.

The correction allocates each filled integer table once and writes every element by index. It preserves empty results for nonpositive lengths. All 64 benchmark streams and all PNG outputs remain byte-identical to the initial candidate. ARM64 Callgrind reproduces the original CI count exactly, then measures 43,923,960 encode instructions and 35,961,899 decode instructions after the correction. The decode baseline decreases from 36,429,262 to the measured count so that its improvement becomes the new ceiling. The encode baseline stays unchanged.

The local x86 Callgrind attempt rejects a BMI2 instruction under emulation, so its partial counts are not qualification evidence. Native x86 CI run `41317429107` measures 42,207,169 encode instructions and 33,837,238 decode instructions. Both improve beyond the existing one-percent tolerance, so their baselines decrease to retain those gains. ARM64 CI passes and reproduces both local counts exactly. Production emitters and benchmark inputs remain unchanged.

Use the existing `scripts/deflate-bench` setup for the pinned corpus and C reference. The pilot uses one corpus file; the full run changes only `--corpus-limit` to 12:

```sh
uv run docs/benchmarks/deflate-short-matches-2026-11-01/measure.py \
  --compiler /tmp/fern-13994-update-only-fixed-point \
  --baseline-stdlib /Users/jakechampion/codex/lang-pr-13988-one-block-validation/internal/stdlib \
  --work /tmp/fern-13347-fill-repro --runs 5 \
  --corpus /Users/jakechampion/.cache/fern-deflate-bench/silesia \
  --corpus-limit 1 --cbench /tmp/fern-13347-cbench
```

Separate-ring, flat-pair and relative-link layouts were slower in the tile trials. A fixed-code cost filter preserved the old gzip size but failed to recover three historical tile sizes. The selected search keeps the original 35/17 candidate budgets and adds a bounded 35-candidate fallback.
