# HTTP/3 allocation repair

PR #14022 increased allocations at three call sites per request.
The matched parent passes the existing serial gate with 4,668,887 allocations over 100,000 requests.
The issue head produces 4,968,855 allocations and fails the unchanged ceiling of 48 per request.
The caller census attributes two added allocations to `Conn.__readable` and one to `Streams.write`.

The record guard kept the first projection after an array snapshot, even when the update block read the record again.
The repair restricts that extra guard to consumed record and variant donors.
Array snapshots follow the existing rule for reads in the update block.
The independent token slots, donor identities and region certificates remain intact.

The final source includes actual main `87b8892564`.
Compiler stages 2, 3 and 4 match `f5ba48fe3a8c0ee025f38d57aa310401aae41ee2184899765c5988782367605c`.
Stage 1 differs.
The compiler binding records every compiler and standard-library input, embedded asset, producer and command.

The prepared runner uses the existing Go harness and its original limits, slack and concurrency checks.
Both 1,000-request pilots pass before the 100,000-request runs.
QEMU supplies correctness and allocation evidence. These durations do not measure native performance.

| Final probe | Allocations | Requests | Limit per request | Peak concurrency |
| --- | ---: | ---: | ---: | ---: |
| Serial | 4,668,930 | 100,000 | 48 | Not reported |
| Multiplexed | 2,874,735 | 100,000 | 29 | 64 |

The seven wide-update controls retain their original gains against the security parent.
The native ARM instruction counts match the earlier measurements at 10,000 steps.
P/D allocations remain 3, compared with 10,002 on the parent.
The 8-step pilots and full runs retain exact commands, program hashes and raw Callgrind records.

All 39 benchmark outputs match the prior directory-token producer.
The unchanged strict gates pass all 78 ARM metrics and 39 x86 static metrics.
The three-target advisory gate passes all 117 metrics.
No baseline increases or native x86 instruction claims follow from this local campaign.

The ownership suite passes all 15 roots in 290.981 seconds.
The three nested-token roots also pass on native Darwin and Wasm in 48.592 seconds.
The x86 census helper uses the target runner on this ARM host.
Direct execution faults for a byte-identical control from the public issue and diagnostic compilers.
QEMU returns the expected result with balanced counters.
The helper change preserves native x86 execution on x86 hosts.

The manifest binds the raw records and lossless archives.
Earlier evidence directories retain their original source and producer identities.
