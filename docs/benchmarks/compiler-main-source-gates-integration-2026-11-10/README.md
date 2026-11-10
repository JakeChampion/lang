# Source gate evidence after main integration

Status: [record] integration from 2026-11-10.

The owner merged both source gate repairs through PRs #15115 and #15121.
Main is `e31c27c971145607f0a4071795b601d985b82b32`.
This PR now retains qualification evidence without a compiler, standard library, test, or reference change against main.

Both compressed references have exactly the same decompressed bytes.
The only compressed-byte difference is the gzip header at offset 9.
The earlier archive has byte 3, and main has byte 255.
The live file retains main's container.

[reference-main.json.gz](reference-main.json.gz) preserves the exact main file.
[reference-resolution.json](reference-resolution.json) records both hashes, the shared raw hash, and the conflict decision.
The [original evidence](../compiler-main-source-gates-2026-11-10/README.md) retains the earlier exact container and all 4,698 records.
Its verifier still requires exact product bytes at its original publication commit.
No proof, negative control, or comparison rule changes.

The earlier qualification uses source `93942727d19bcad77d7aef70da15b36c58056bf8` and its recorded compiler.
It does not qualify later compiler changes on main.
The source generator check and CI self-tests must verify the integrated source separately.
