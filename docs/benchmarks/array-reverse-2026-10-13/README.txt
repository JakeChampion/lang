Reproduction commands (run from the repository root)

See docs/rc-log/2026-10-13-borrowed-with-chains.md for revisions, producer
hashes, versions, limitations and the distinction between timing and profiling.
For the recorded measurements, use TWO checkouts at 324340038. Apply the
archived production.patch.gz to the candidate checkout only. Later main updates
used for validation are NOT part of that controlled A/B experiment:

  gzip -cd /ABS/production.patch.gz | git -C CANDIDATE_TREE apply -

The uncompressed patch SHA-256 is
6ed0f6f558abd5604bcc2f4a09bd5835490ae83cbba68bc865081cd6dc01ede7.
Build each compiler with the same verified stage0-20261009-823f7b1 executable:

  STAGE0 -target x86-64-linux -o DRIVER TREE/compiler/fern.fern TREE/internal/stdlib

STAGE0, DRIVER and TREE stand for explicit absolute paths. For each driver:

  DRIVER -O -target x86-64-linux -o reverse-release \
    docs/benchmarks/array-reverse-2026-10-13/reverse.fern TREE/internal/stdlib

Check exit status zero and empty stdout/stderr for each binary under each
argument list: small, small shared, large, large shared. Stop other heavy
work before timing. With Hyperfine 2.0.0, run each matching pair separately:

  hyperfine -N --warmup 5 --runs 100 --export-json case.json \
    'BASE_BINARY large' 'CANDIDATE_BINARY large'

Repeat for the other three argument lists. The JSON gzip files retain all
100 raw samples; mean/stddev are summary.time_wall_clock.mean/stddev.

For separate allocation profiles, repeat each build with FERN_RC_TRACE=1
and FERN_LEAKCHECK=1 set in the compiler's environment. Check fixture exit
zero and empty stdout, retain stderr, then summarize it:

  python3 docs/benchmarks/array-reverse-2026-10-13/summarize-trace.py TRACE.stderr

For Callgrind 3.27.1, emit the same optimized fixture as assembly, then link
with GNU as through gcc. This gives Callgrind a conventional ELF layout:

  DRIVER -O -target x86-64-linux -emit asm -o reverse.s \
    docs/benchmarks/array-reverse-2026-10-13/reverse.fern TREE/internal/stdlib
  gcc -nostdlib -static -no-pie -Wl,--build-id=none -o reverse-profile reverse.s
  valgrind --tool=callgrind --dump-instr=yes --callgrind-out-file=profile.out \
    ./reverse-profile large
  python3 docs/benchmarks/array-reverse-2026-10-13/summarize-profile.py \
    reverse-profile profile.out

Repeat for large shared and both drivers. Only use each binary with its own
profile: addresses differ. The saved instruction dumps accept gzip directly.
Header stores are a layout-specific attribution, not a logical RC event census.
Copied-byte figures are derived from allocation growth capacities and element
slot width, not a directly instrumented byte-copy counter.

Compiler timing separately emits the frozen baseline checker to /dev/null,
using its matching frozen stdlib, default optimization, x86-64-linux, and
FERN_RC_CERTIFY_MEMO=0. Use 10 runs, 2 warmups and both command orders; no
compiler speedup is claimed because the result changes with command order.

Local validation environments (not performance inputs)

The full current-source suite uses GNU 9.12, Go 1.27.1 and the pinned
Wasmtime/WASI adapter. Source is copied into a disposable writable container;
the incoming checkout is mounted read-only. A private PID namespace with
SYS_PTRACE and seccomp=unconfined lets sandbox tests apply their own filters.
Both mise installs and their HTTP-tarball symlink targets must be mounted.
The image includes jq and libc6-dev-arm64-cross.

Stock QEMU 10.0.2 zeros unnamed termios c_cc slots 17 and 18 during ioctl
translation. Baseline and candidate reproduce the same failing round trip.
For local ARM64 correctness tests, decompress and apply qemu-cc.patch.gz to the official release
tarball https://download.qemu.org/qemu-10.0.2.tar.xz, SHA-256:
ef786f2398cb5184600f69aef4d5d691efd44576a3cff4126d38d4c6fec87759.
The patch preserves raw bytes only for AArch64 guests on x86-64 hosts; existing
named-control-character mappings still run. Build with glib development
headers, pkg-config, ninja and python3-venv:

  ./configure --target-list=aarch64-linux-user --disable-system --disable-docs \
    --disable-guest-agent --disable-tools --disable-capstone --disable-download
  make -j2

The local resulting qemu-aarch64 SHA-256 is
fb3553cab2e79639424bffc1c9246e8f9419a44bd7ed8a94a2d9acdbb7d1e51a.
TestArm64Termios passes unchanged (25.546 s). This is not a claim that the
stock emulator passes, and no emulator timing is used for performance claims.

This worker kernel lacks CONFIG_ELF_CORE and /proc/self/coredump_filter.
TestRunWithStackLimit is therefore run unchanged using the statically linked
e2e test executable in a Linux 6.8 software VM. Its minimal initramfs contains
that binary, go.mod for package-root discovery, BusyBox, Bash and Bash's shared
libraries. Mount proc, devtmpfs and devpts, then run:

  /test -test.v -test.run '^TestRunWithStackLimit$' -test.timeout=2m

The wrapper requires the explicit test PASS plus an exit-code-zero marker,
not merely a successful VM shutdown. The current b9c5573db plus candidate test
binary SHA-256 is
f86103b01ce7a7dc5c54b862892d8b3dad04a5144ef74715ecac8be0fcec9f35.
The subsequent 34360bd01 plus candidate/harness binary also passes this
unchanged VM test; its SHA-256 is
068ff746636c60ed6c0cd76aab8aa723316c2aa39fc2d9922c9302435cda6f8b.
The host shard coverage ledger assigns exactly this test to the VM and checks
that host shards plus the VM cover the discovered tests exactly once,
including fuzz seeds. Test lists, source hashes, logs and exit codes are
preserved in a named Docker volume. No missing-platform execution is reported
as a supported-platform pass.

The worker also lacks CONFIG_CHECKPOINT_RESTORE's task/children proc file.
The supervised-server test harness uses strict PPid enumeration when that
optional interface is absent, retaining all count/shutdown assertions. This
is a test-portability prerequisite (#12363), not a production server change.
The complete validation coordinators execute their four shards inside the
containers, persisting a result for each shard even if a host client stops.

The corrected frozen 34360bd01 candidate's complete local validation passes:
all four e2e shards, all four compiler shards, the unchanged VM kernel gate,
all 15 relevant opt-in gates and generation, plus a subsequent fresh -count=1
run of all 71 remaining packages (GNU 9.12 coreutils: 630.500 seconds).
The initial remaining-package phase was cached, not fresh execution.
Final vet/gofmt/Fern formatting gates pass. Unsupported platform/debugger/
kernel-feature and mutation-only skips are recorded, not claimed executed.
The coverage-guided smoke budget is 30 seconds; feedback compares five streams
of 300 guided and 300 blind iterations, not another 30-second campaign.
The complete/fresh/source-hash audit and exact pre-documentation-update source
are preserved in local tooling archives; see the rc-log for artifact names.
Previous failed validation remains failed and preserved.

Upstream subsequently carries independently merged borrowed-with, dynamic-call
bracketing, formatter, proxy and proc-children fixes. Integration must preserve
those guards and tests and be validated separately. These A/B exports belong
ONLY to frozen 324340038 plus production.patch, not the integrated compiler.

Integration starts from main c78ff2894 with the compiler, formatter, simulated
proxy, proc-children harness and pipeline controls exactly as upstream shipped
them. Only reversal, supplemental array regressions and the historical records
are carried forward. The raw patch bytes remain unchanged in gzip archives.
The older snapshot's static sole-box fast path is not transplanted; integrated
correctness/resource gates and release timings must be established afresh.
