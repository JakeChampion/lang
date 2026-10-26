# Native worker failure qualification

Issue #13332 is a child of aggregate memory issue #13222.
This slice tests disposable native Fern workers under local Docker cgroup v2 controls.
The Python collector is an offline test tool.
All image operations execute in Fern.
The fixture does not implement a production worker pool or an HTTP service.

## Controls and workload

Each container starts one static x86-64 Fern executable from a scratch image.
The image requires no external library, package, or network download.
The worker runs as UID and GID 1000 with no capabilities or extra mounts.
Its root filesystem is read-only, and its network mode is `none`.
The container also sets `no-new-privileges` and a private cgroup namespace.

The memory limit is 64 MiB, with zero swap allowance.
The CPU quota is 50000 microseconds per 100000 microseconds.
The PID limit is 16.
The collector verifies these controls through Docker inspection.
Completed and busy workers also read their own cgroup control files.
Every worker rejects a different memory or swap limit before its workload starts.

Each valid worker renders a cached 64 by 16 tile from a virtual bilinear graph.
The graph maps 1024 by 10000 pixels to 512 by 20000 pixels.
The worker checks all four channels at a tile corner.
It also encodes and decodes a 16 by 16 RGBA PNG with one colored pixel.
The worker compares every decoded byte with the original image.
This small restricted PNG does not qualify broad PNG compatibility.

The retained-image fixture keeps distinct 512 by 512 RGBA frames through copy-on-write updates.
Each frame contains 1 MiB of pixel data.
The OOM mode attempts to retain 128 frames under the 64 MiB cgroup limit.
The pressure modes retain 48 frames, then render tiles or test cancellation.
The worker accesses retained images after cancellation to preserve their ownership during that test.
Cancellation stops at callback polls 1, 8, 24, and 64.

## CI cgroup driver

CI selects Docker's cgroupfs driver before worker qualification.
Systemd can remove an empty container scope before the runtime reads its OOM event.
The runtime issue is [runc #5071](https://github.com/opencontainers/runc/issues/5071).
The cgroupfs driver keeps cgroup removal under the container runtime.

CI run 40337273239 recorded exit 137 with `OOMKilled=false` for an OOM worker under the systemd driver.
The collector rejected that observation.
The driver selection preserves every resource limit, deadline, leak, recovery, and cleanup assertion.
An exit code of 137 alone remains insufficient evidence of a kernel OOM kill.

The local cgroupfs run with Docker 28.4.0 passed all 27 fresh worker cases.
All three OOM workers reported `OOMKilled=true`.
The record reports unchanged source hashes and successful cleanup.
This result does not qualify a production worker pool or aggregate service memory.

## Failure and recovery contract

The collector runs three sequences of nine fresh containers, with no concurrent workers.
Each sequence contains five valid workers, one OOM worker, two deadline workers, and one cancellation worker.
A valid worker runs before and after each forced failure.
The collector requires exit code 137 and Docker `OOMKilled=true` for the OOM worker.
The deadline workers must report `OOMKilled=false` after an explicit `SIGKILL`.
Every completed valid or cancellation worker must report balanced allocations and zero final live bytes.

The deadline starts when the collector observes the worker's ready message.
The collector polls Docker, waits at least one second, then kills the worker.
Startup has a separate fifteen-second observation limit.
Docker commands also have timeouts.
The collector waits for termination and verifies a zero process ID before container removal.
It verifies removal before the next worker starts.

The raw record includes source, library, stdlib, compiler, binary, collector, and Docker CLI hashes.
The collector checks source and tool hashes before and after the sequence.
It records the scratch image ID, safe daemon fields, controls, worker output, and final process state.
It does not record Docker environment values or registry credentials.

## Local observations

The committed record uses compiler `8c22550998a5` and worker source commit `8eb44c989971351361d0a3f3cc5d6855aecb5e8b`.
All 27 containers pass their expected outcome checks and container removal checks.
The three OOM workers exit with code 137 and `OOMKilled=true`.
The six deadline workers exit with code 137 and `OOMKilled=false`.
The fifteen valid workers and three cancellation workers exit successfully with zero final live bytes.

The busy workers stop 1.071 to 1.129 seconds after the ready observation.
The workers with retained images stop 1.083 to 1.166 seconds after that observation.
Their pre-work cgroup peak observations range from 51793920 to 52068352 bytes.
The cancellation workers report cgroup peaks from 51781632 to 52092928 bytes at their observation phases.
These values describe this collection on this host.
They are not thresholds for service latency or exact memory use.

The valid and cancellation modes also pass separate native heap-sanitizer runs under the same container controls.
The worker source compiles for ARM64 and Wasm.
Those compile checks do not qualify cgroup execution on those targets.
The current image library also passes its separate 76 TAP cases on native x86-64, Wasm, and ARM64/QEMU.
GitHub CI remains separate from this local evidence.

## Evidence limits

The cgroup memory limit controls kernel memory charge for one container.
It is not an exact RSS limit, an allocator limit, or a mathematical process memory bound.
The worker reads `memory.peak` at explicit phases.
Those observations concern cgroup charge, with kernel accounting limits and observation costs.
They do not replace the separate [RSS observations](rss-observations.md).

The CPU quota controls bandwidth.
It does not promise an exact elapsed deadline or qualify representative image throughput.
The deadline observations include collector polls, Docker commands, scheduling, and process termination.
The record does not qualify hard real-time deadlines or service latency.

A forced kill does not execute Fern destructors or the final leak census.
A later valid worker proves recovery in a fresh process.
It does not prove recovery inside the killed worker or recoverable allocation failure.
The fixture does not test collector crashes, daemon restarts, simultaneous workers, or malicious input isolation.
The collector and Docker daemon remain outside each worker's memory limit.
Aggregate service admission, worker pools, physical scratch accounting, and production gates remain incomplete.

## Reproduce

Use native x86-64 Linux with Docker and cgroup v2 memory, swap, CPU, and PID controls.
Use the repository's supported compiler and CPU baseline.
Run this command from the repository root:

```sh
FERN="$PWD/bin/fern" ./fern-image/scripts/qualify-workers.sh
```

The script enables the leak census without sanitizer quarantine.
It writes a fresh record to `fern-image/build/worker-qualification.json`.
The native image CI job runs the same script and uploads that record.
Unavailable cgroup controls cause failure, not a skipped qualification.
The [committed record](../benchmarks/results/worker-isolation-m4.json) preserves one local observation set.
Kernel charge and elapsed observations can vary between runs.

## Failed qualification evidence

Issue #13486 preserves worker evidence when a qualification assertion fails.
The record includes the selected exit state, controls, worker output, completed samples, and the failed sample.
The collector records cleanup outcomes before it writes the file.
It also records a later state observation for diagnosis.
That later observation cannot waive the original assertion failure.

A record with `status: failed` is incomplete evidence, not a passing qualification.
The script still exits with failure.
CI uploads an available record after failure, without changes to the test result or subsequent validation steps.
The OOM, leak, deadline, pressure, source-immutability, and cleanup checks retain their existing conditions.
Records contain selected state fields rather than Docker environment or credential configuration.

Eight local fault-injection cases check strict OOM signals, missing state, leak rejection, failed-record output, and cleanup.
A delayed mock OOM flag still produces a failure and preserves both observations.
These unit cases do not identify the cause of the intermittent CI OOM assertion.
Actual Docker qualification remains a separate test with the original controls.
Historical committed records retain their original schema, source hashes, and scope.
