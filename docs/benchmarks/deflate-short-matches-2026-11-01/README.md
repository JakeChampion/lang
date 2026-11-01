These measurements qualify the short-match repair for #13347.

The matcher keeps bounded chains for both hashes. A packed ring stores both predecessor links in one 64-bit slot per history position. A short-chain search runs when the four-byte search finds no match. Every search stays inside the requested window.

| Tile | Before the four-byte change | Main | Candidate |
| --- | ---: | ---: | ---: |
| z0 | 23,765 | 24,123 | 23,640 |
| z2 | 30,744 | 31,684 | 30,679 |
| z14 | 77,465 | 82,706 | 77,300 |
| z38 | 90,609 | 95,792 | 90,509 |

All four filtered image streams are byte-identical across main and candidate. PNG chunk CRCs and Python zlib decoding are checked. The historical matcher reproduces the original sizes on the same inputs. Its compatibility edits only rename the error and CRC32 APIs.

On the 12-file Silesia corpus, output totals 68,224,127 bytes on main and 68,201,907 for the candidate. 7 files grow. The sum of the per-file encode medians is 3.225 seconds on main and 3.397 seconds for the candidate. The historical matcher takes 6.082 seconds. Each median covers five native calls on the same ARM64 Darwin host.

The libdeflate 1.24 level-6 reference produces 67,509,984 bytes. Its compressor and output buffer are allocated before the timed calls. Fern allocates its encode buffers inside the calls. Both implementations decode the candidate/reference streams, and Python independently decodes every stream.

The mixed-block fixture shrinks from 17,096 to 17,095 bytes. The gzip fixture grows from 7,818 to 7,819 bytes and decodes to the same 70,890 bytes. The exact size assertions record these streams. No performance baseline or tolerance changes.

Both instrumented probes record 13 fewer allocation events. Arena-bump totals stay flat within each pair. The probes require balanced process counts and zero live bytes.

The fixed native compiler is the #13995 fixed point, SHA256 `ed765f71669c60a41766716f12a815bb018b64b99547ac0a988444e78e4df39d`. The formatted encoder produces the same benchmark binary as the pre-format source. Full inputs, hashes, timings and compatibility edits are in [measurements.json](measurements.json).

Use the existing `scripts/deflate-bench` setup for the pinned corpus and C reference. The pilot uses one corpus file; the full run changes only `--corpus-limit` to 12:

```sh
uv run docs/benchmarks/deflate-short-matches-2026-11-01/measure.py \
  --compiler /tmp/fern-13994-update-only-fixed-point \
  --baseline-stdlib /Users/jakechampion/codex/lang-pr-13988-one-block-validation/internal/stdlib \
  --work /tmp/fern-13347-repro --runs 5 \
  --corpus /Users/jakechampion/.cache/fern-deflate-bench/silesia \
  --corpus-limit 1 --cbench /tmp/fern-13347-cbench
```

Separate-ring, flat-pair and relative-link layouts were slower in the tile trials. A fixed-code cost filter preserved the old gzip size but failed to recover three historical tile sizes. The selected search keeps the original 35/17 candidate budgets and adds a bounded 35-candidate fallback.
