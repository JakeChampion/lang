# Targets and backends

The Go compiler generates no code: its emitters, the stack-machine
`internal/codegen/{arm64,x86_64,wasmbin}` and the register-allocating
`arm64ssa` / `x86_64ssa`, were deleted in step 5 of
`docs/NATIVE-RETIREMENT.md`. Every `-target` compile runs the self-host
compiler: its register path (`-backend ssa`, `docs/SELFHOST-SSA-BACKEND.md`)
on the native ISAs, its stack machine (`-backend flat`) on wasm. Background:
`docs/SSA-DECISION.md`, #4112, #8822.

`internal/testing/e2e/arm64_default_string_reclaim_test.go` holds the default arm64
build to retention that does not grow with the input: a string passed to a
user function is reclaimed.

Targets are `<isa>-<environment>` (#6529): the ISA half picks the backend, the
environment half says what the host provides. Neither is implied — there is no
bare `arm64` meaning arm64-Linux. There is no x86-64 Darwin target.

| target | ISA | environment | object format | ABI | status |
| ------ | --- | ----------- | ------------- | --- | ------ |
| arm64-linux | arm64 | linux | ELF | AAPCS64 | primary target |
| arm64-darwin | arm64 | darwin | Mach-O | AAPCS64 + Apple's syscall vector | the arm64-linux emitter, its text rewritten for XNU (`darwinize` in `asm_arm64_ir.fern`) |
| arm64-android | arm64 | android | ELF (ET_DYN, PIE) | AAPCS64 | same syscalls as arm64-linux |
| x86-64-linux | x86-64 | linux | ELF | System V AMD64 | supported |
| wasm32-wasi | wasm32 | wasi | wasm32 module | wasm CC + WASI | a wasi:cli/run component by default |
| wasm32-wasi-http | wasm32 | wasi-http | component | wasi:http/incoming-handler | proxy world; std/fetch sends through wasi:http/outgoing-handler (docs/WASI-PREVIEW2.md) |
| arm64-freestanding, x86-64-freestanding | | freestanding | — | — | declared + type-checkable; no emitter yet (#6510) |

Two axes are deliberately NOT in the name: `-backend` names the target's
emitter, and `-emit` an alternate output form (#6536). Each target has one
emitter, and `-backend` may only name it: `ssa`, the register path, on both
native ISAs; `flat`, the stack machine, on wasm, which has no register path
and is not getting one — wasm is a stack machine with locals, so the register
path's wins do not exist there (`docs/SELFHOST-SSA-BACKEND.md`, "The other
backends").

`wasm32-wasi` has two output forms: the default composes a wasi:cli/run
component, and `-emit core-module` and `-emit command-module` both write the
core module, a WASI preview-1 command with a `_start` that runs main and exits
with its value, which also exports `main` (#10768, #11408). The exit code is
what separates them: a `wasi:cli/run` component reports ok or err and nothing
wider, so `return 42` reaches the host as 1. A `main` that returns nothing
exits 0 (#9233, `internal/testing/e2e/void_main_exit_test.go`).

`cmd/fern` passes `-target`, `-emit` and `-backend` through to the self-host
driver unchanged (`cmd/fern/selfhost.go`), and `fern -targets` lists the
targets. `-emit asm` stops at the emitter's text (GAS on the natives, WAT on
wasm); it is what a native compile with neither `-o` nor `-emit` prints, and
how the emitters are observed in isolation
(docs/TOOLCHAIN-SELF-HOSTING.md). For `wasm32-wasi-http` the
self-host appends `std/wasi_http`, the entry written in Fern over `@import`
externs, and composes the core against the embedded proxy world
(`compiler/wit_compose.fern`).

## Internal networking syscall floor

`__syscall3` through `__syscall6` (#9853, #4451) pass a syscall number and
three to six machine-word operands and return the kernel's word, negative
errno on failure on every target. `__store_u8(addr, v)` writes the low byte
of `v`. Every native target lowers the same names, so a socket primitive is
one Fern body rather than assembly per target. They are runtime
intrinsics with a `__` prefix, not public builtins: `internal/pkg/platforms`
gates them under the `syscall` capability only the hosted-native profile
grants, they carry no package capability, and networking APIs built on them
still need their own classifications.

| Target | lowering |
| --- | --- |
| x86-64-linux | `syscall`, number in `rax`, sixth argument in `r9` |
| arm64-linux | `svc #0`, number in `x8`, sixth argument in `x5` |
| arm64-darwin | `svc #0x80`, number in `x16`, carry-flagged errno negated |
| wasm32-wasi | E066: the target has no `syscall` capability |
| interp | never reached: refused with the target, not at run time |

`FERN_SANDBOX=1` on x86-64 records a floor call whose number is a literal
like any other syscall, and refuses a program whose number is a run-time
operand, which the seccomp allowlist cannot cover.

The syscall-floor probe (`internal/testing/e2e/native_syscall_floor_test.go`) maps a
file at a nonzero offset, reads distinct bytes back, unmaps and closes, pins
the errno of a bad descriptor, and round-trips bytes through the byte store.
The Darwin legs run in the Apple Silicon lane.

`tcp_listen`, `tcp_listen_with`, `tcp_connect`, `tcp_accept`,
`tcp_local_port`, `tcp_close`, `tcp_socket_ctl`, `tcp_recv`, `tcp_send`,
`udp_send`, `udp_bind`, `udp_connect`, `udp_sendto` and `udp_recvfrom` are
one Fern body each in `asmcore.fern` over this floor, on x86-64-linux,
arm64-linux and arm64-darwin (the sockaddr's leading `sin_len` byte, the
option numbers and `MSG_NOSIGNAL` are the Darwin differences); wasm has
wasi:sockets bodies in `wasm_ir.fern`, and the interpreter Go ones.

Every socket primitive that takes an address takes it as a `u8[]` of its
network-order bytes, four for IPv4 and sixteen for IPv6, and opens the
socket for the family the length names; any other length answers
`-EAFNOSUPPORT` before a socket exists (97 on Linux, 47 on Darwin, 5 in
the WASI numbering). The unspecified address of either family takes
every interface, and `::` both families: `IPV6_V6ONLY` is cleared on an
IPv6 listener whatever the host's default, except on wasi:sockets 0.2,
which keeps an IPv6 socket IPv6-only. A host without IPv6 refuses the family:
`-EAFNOSUPPORT` from `socket(2)` natively and in the interpreter, and on
wasm the `-ENOTSUP` wasmtime answers for create-socket. `tcp_connect` and
`udp_send` keep their packed IPv4 forms and box them for the same bodies.
The IPv6 leg (`TestSocketV6*` and the self-host twin) runs where Go can
listen on `::1` and skips, with the probe's own "nov6" answer required,
where it cannot.

`tcp_listen_with(addr, port, backlog, reuse_port)` and `tcp_socket_ctl(fd,
op, arg)` (op 1 `TCP_NODELAY`, 2 `SO_KEEPALIVE`, 3 `O_NONBLOCK`, 4
`shutdown(2)` with `arg` its how, 5 the result of a connect under way, 6
the bytes queued to send that the peer has not acknowledged, `SIOCOUTQ`
on Linux and `SO_NWRITE` on Darwin, 7 the peer's address as one 32-bit
key: an IPv4 address packed as `tcp_connect` takes it, a v4-mapped IPv6
address its IPv4 one, any other IPv6 address the two words of its first
eight bytes XORed and never 0, and 0 for a socket with no peer, so a key
is never mistaken for an error, 9 the socket's own address and 10 its
peer's as 16-bit groups: arg 0..7 the group in network order, an IPv4
address filling 0 and 1 and the rest 0, 8 the family as 4 or 6, 9 the
port, -EINVAL past that) are the socket controls of #9853;
std/net wraps them. On wasm, ops 1, 3 and 6 answer `-ENOTSUP` (58 in the
WASI numbering) because wasi:sockets 0.2 has neither a Nagle switch nor a
blocking mode to turn off nor a reading of the send queue (op 7 reads
`remote-address`, whose 16-bit IPv6 groups the u16 lowering byte-swaps
into the same words), and
`reuse_port` is ignored: a port is one socket's there. Every other op and the backlog behave the same on every
target. A wasi:sockets `result<_, error-code>` puts the error-code at byte
1 (a handle, tuple or address payload puts it at 4, a u64 count at 8);
the wasm socket bodies read the byte the result's shape names,
so a refused bind or dial reports its errno rather than whatever the area
held.

The datagram sockets (#9853) are `udp_bind(addr, port)` (a socket bound
to addr:port, the unspecified address for every interface and port 0 for
one the host picks, or -errno), `udp_connect(fd, addr, port)` (fix the
peer: 0 or -errno), `udp_sendto(fd, addr, port, data)` (one datagram to
addr:port, or to the peer when `addr` is empty: the bytes accepted, or
-errno) and `udp_recvfrom(fd, buf, from)` (one datagram into the `u8[]`
`buf`, up to its length, and the sender into a `from` of nineteen bytes
or more: the family at 0, 4 or 6, the address's network-order bytes from
1, four or sixteen with the rest zero, and the port at 17, high byte
first: the byte count, or -errno). `tcp_close` and `tcp_local_port` take
a datagram socket too, and
std/net wraps the four as `udp_socket`, `set_peer`, `send_to`, `send`,
`recv_from`, `recv`, `local_port` and `close`. The same on every target,
with these divergences: on wasm a datagram "fd" is the tcp record with a
kind word of 2 or 3 at offset 12, `tcp_socket_ctl` on one answers
`-ENOTSUP` for every op (wasi:sockets 0.2 has no keep-alive, shutdown or
blocking mode on a udp socket), a `udp_connect` that the host refuses
leaves the socket without streams until the next one succeeds, and
`udp_recvfrom` blocks on the incoming stream's pollable where the natives
block in recvfrom(2), so `set_nonblocking` has no udp arm there; on Darwin
a `udp_sendto` naming an address on a connected socket is refused with
`EISCONN` where Linux sends it, so a connected socket sends with an empty
address on every target; and the interpreter keeps a raw descriptor per
datagram socket, so its errnos are the host's like the natives'.

`tcp_connect_with(addr, port, nonblocking)` is a connect to addr:port, or
with `nonblocking` a non-blocking socket whose connect is only started: the
descriptor comes back while the connect is under way (-errno only when it
could not start), and control op 5 says how it ended: 0 once a peer is
attached, `-EINPROGRESS` while none is and no error is pending, else the
`-errno` it failed with, read once through `SO_ERROR`. On wasm the started
connect is the record with kind 4 at offset 12 (start-connect done,
finish-connect not yet), op 5 runs finish-connect (a `would-block` is
`-EINPROGRESS`, 26 in the WASI numbering) and on success gives the record
its streams; `tcp_close` on such a record drops the socket alone. The
interpreter dials through the net package before answering, so its op 5
is 0 at once and a refused dial is reported by `tcp_connect_with` itself.
Waiting for a started connect is a loop over op 5 today: the readiness
builtins watch readability only; the reactor floor below watches writability too.
std/net wraps the three as `connect`, `connect_start` and `connect_result`.

`unix_listen(path, backlog)` and `unix_connect(path)` are the Unix-domain
sockets: a stream listener at a filesystem path and a connection to one,
each a descriptor or -errno (-ENAMETOOLONG for a path longer than the
address holds, 107 bytes on Linux and 103 on Darwin, before any socket
exists; a held path is -EADDRINUSE, and the socket file is not unlinked
first), which `tcp_accept`, `tcp_recv`, `tcp_send` and `tcp_close` take.
Fern bodies in `asmcore.fern` (Darwin leads the address with sun_len) and
the net package in the interpreter. Native only: the `unix` capability is
in no wasi profile, so E066 refuses them on both wasm worlds at check time,
and std/net's `listen_unix`, `connect_unix` and `accept` wrap them.

The **reactor floor** is a readiness set that outlives one wait:
`reactor_new()` (epoll on Linux, kqueue on Darwin, a table of wasi:io
pollables on wasm, an epoll or kqueue set over the handles' descriptors in
the interpreter), `reactor_ctl(r, op, fd, arg)` (op 1 watches `fd` for the
interest in `arg`, 1 readable and 2 writable, op 2 stops watching it, op 3
closes the set) and `reactor_wait(r, events, timeout_ms)`, which fills
`events` with (fd, readiness) pairs, readiness 1 readable, 2 writable and 4
an error or hang-up, and answers the pair count, 0 on the timeout (-1 waits
without one), or -errno. Fern bodies natively; on wasm a
watch subscribes the pollables a record's kind names (a listener or a
connect under way through tcp-socket.subscribe, a connection through its
input and output streams, a datagram socket through its datagram streams)
and a wait polls them with a timer for a finite timeout. Two rules hold on
every target: a host may report readiness spuriously (wasmtime does after a
read that did not drain the socket), so a reader reads until -EAGAIN; and a
socket is unwatched before it is closed, since on wasm the pollables a watch
holds are children of its streams. Behind it, `tcp_recv_into(fd, buf)` is
the owned-buffer read: the bytes read into the caller's `u8[]`, 0 at EOF,
-EAGAIN (wasi's 6) when nothing is queued and the socket is non-blocking,
which on wasm is every socket, else -errno. A signal is a readiness event
too: `reactor_ctl` op 4 watches signal `fd` and answers a descriptor op 5
wants back, and the set reports a delivery as the pair (-signal, 1). On
Linux the signal is blocked and read through a signalfd the set holds
beside -signal in its event's data word; on Darwin it is ignored, so its
default action cannot end the process, and kqueue's EVFILT_SIGNAL records
each delivery; the interpreter turns os/signal deliveries into bytes on a
pipe the set watches; wasm, which has no signals, answers -ENOTSUP. Op 6
reports the parent's exit as that SIGTERM pair: PR_SET_PDEATHSIG on
Linux, EVFILT_PROC's NOTE_EXIT on Darwin (the interpreter waits on it in
a kqueue of its own and raises SIGTERM), -ENOTSUP on wasm. The
Driver seam wraps the floor as `watch`, `unwatch`, `wait`, `watch_signal`,
`unwatch_signal`, `watch_parent` and `close` (std/async), std/sim scripts readiness for
its leg with `ready_at`, and the serve loops run on it.

Per target, every socket primitive is provided as follows. "Fern body" is
the one body in `asmcore.fern` over the syscall floor, the same on
x86-64-linux and arm64-linux and on arm64-darwin with its sockaddr length
byte, option numbers and `MSG_NOSIGNAL` value. wasi-http is the proxy world,
whose profile grants none of `tcp`, `unix` or `reactor`, so E066 refuses each
of these at check time there.

| Primitive | Linux natives | arm64-darwin | wasm32-wasi | wasi-http | interp |
| --- | --- | --- | --- | --- | --- |
| `tcp_listen`, `tcp_accept`, `tcp_local_port`, `tcp_close`, `tcp_pollable`, `tcp_recv`, `tcp_send` | Fern body; `tcp_pollable` answers the descriptor itself | Fern body; `tcp_pollable` answers the descriptor itself | wasi:sockets/tcp bodies (`wasm_ir.fern`); a "fd" is the 16-byte record | E066 | net package |
| `tcp_connect` (packed IPv4) | Fern body | Fern body | boxes the address for `tcp_connect_with` | E066 | net package |
| `tcp_listen_with`, `tcp_connect_with` (byte address) | Fern body | Fern body | `__fern_ip_flat` then start-bind or start-connect; a started connect is kind 4 | E066 | net package, `JoinHostPort`; a started connect is finished before answering |
| `tcp_socket_ctl` | Fern body; op 7 answers 0 on a datagram socket | Fern body; op 7 answers 0 on a datagram socket | ops 2, 4, 5, 7, 9 and 10 through wasi:sockets; 1, 3 and 6 `-ENOTSUP`; every op but 9 and 10 `-ENOTSUP` on a datagram record, op 7 0 | E066 | net package controls; op 3 makes `tcp_recv` and `tcp_send` one read(2) or write(2) on the descriptor, so a send answers what the kernel took or -EAGAIN; op 5 answers 0 at once; op 6 reads the host's send queue; op 7 keys `RemoteAddr`, 0 on a datagram socket; ops 9 and 10 read the descriptor's own sockaddr |
| `tcp_recv_into` | Fern body, `read(2)` | Fern body | non-blocking read on the input stream, `-EAGAIN` when empty | E066 | a read through the descriptor |
| `tcp_sendfile` | Fern body, `sendfile(2)`, the file advanced by what the socket took | Fern body, the position read and moved around Darwin's offset-and-length form | `-ENOTSUP`: the serve loop reads the file and sends the piece | E066 | a read of the open file then one socket write, the file moved back over what the socket did not take |
| `tcp_send_buf` | Fern body, one `sendto(2)` of the builder's bytes from `from` | Fern body | the builder's bytes written to the output stream in place, as `tcp_send_bytes` writes an array | E066 | one socket write of the builder's bytes |
| `udp_send` | Fern body, dotted-quad parse | Fern body | `udp_bind` then `udp_sendto` then close | E066 | net package |
| `udp_bind`, `udp_connect`, `udp_sendto`, `udp_recvfrom` (byte address) | Fern body | Fern body; `EISCONN` for a named address on a connected socket | wasi:sockets/udp bodies (`wasm_ir.fern`); `recvfrom` blocks on the incoming pollable | E066 | raw descriptors, the kernel's errnos |
| `unix_listen`, `unix_connect` | Fern body | Fern body, `sun_len` head | E066: no `unix` | E066 | net package |
| `reactor_new`, `reactor_ctl`, `reactor_wait` | Fern body, epoll | Fern body, kqueue | a table of wasi:io pollables; signals `-ENOTSUP` | E066: no `reactor` | an epoll or kqueue set over the handles; signals through a pipe |

On arm64-darwin `poll(fds, timeout_ms)` reaches `kqueue`/`kevent`
(`asmcore.fern`'s `rt_src_poll_kqueue`). It ignores negative fds and failed
registrations, returns the lowest ready caller index (including duplicate
fds), and supports zero, positive and negative timeouts. The temporary kqueue
and event storage are released on each call. Linux uses `poll` on x86-64 and
`ppoll` on arm64; wasm polls WASI pollables. The persistent reactor is the
`reactor_*` floor above; `poll` remains the one-shot wait the async
combinators use.

On wasm `poll` implements its timeout by adding an owned monotonic-clock
timer to the borrowed pollable list. A timer-only
result returns `-1`; a negative timeout creates no timer. Milliseconds are
widened before conversion to nanoseconds, and each call releases the temporary
list, returned indices, return area and timer. Direct `wasm_poll` remains an
indefinite wait.

## CPU baseline

Fern emits **static binaries with no runtime CPU dispatch**, so every
instruction a backend selects is a hard requirement of the produced binary —
an unavailable one is a SIGILL at first execution, not a slow path. The
baselines are therefore a project-level decision, recorded here rather than
inside a codegen switch:

| backend | baseline | what it buys |
| ------- | -------- | ------------ |
| arm64 / arm64-darwin | ARMv8.2-A with the cryptographic extensions | `clz`, `rbit`, the SIMD-side popcount (`cnt` + `addv`), `crc32`, LSE atomics, the carry-less multiply `pmull` / `pmull2` in its `.1q` form, and the AES rounds `aese` / `aesmc`. This one IS a raise: FEAT_AES (which carries FEAT_PMULL) stays optional in every ARMv8-A and ARMv9-A profile, and taking it is what drops the Raspberry Pi. |
| x86-64 | **x86-64-v3 plus AES-NI** — Haswell-class 2013 (AMD: Excavator 2015, Zen 2017) | `popcnt` (SSE4.2), `lzcnt` / `tzcnt` (BMI1), `pshufb` (SSSE3), the SSE4.1 `roundsd`, SSE2 floating point, `pclmulqdq`, `aesenc` / `aesenclast` / `aeskeygenassist`, and the **AVX2** 32-byte loops the byte kernels already emit. |
| wasm | core wasm 2.0, fixed-width SIMD included | the `v128` family — `v128.load`, `i8x16.splat`, `i8x16.eq`, `i8x16.bitmask` and siblings. SIMD is part of the 2.0 standard rather than an option, and every engine Fern targets (wasmtime, and browsers since 2021) enables it unconditionally, so this is the same kind of floor as arm64's Advanced SIMD. |

**LZCNT / TZCNT have a failure mode POPCNT does not, and it is the reason to
state the baseline rather than assume it.** Below the baseline, POPCNT is an
invalid opcode and faults. LZCNT / TZCNT are the *same opcodes* as the 386-era
BSR / BSF distinguished only by a mandatory `F3` prefix — an older CPU ignores
the prefix and executes BSR / BSF, which answer a different question and are
undefined at a zero input. So a sub-baseline CPU miscomputes **silently** there
instead of crashing.

**The x86-64 level is what the backend already emits, not a raise.** The
self-host's byte kernels (`__fern_count_byte`, `__fern_memchr`,
`__fern_rmemchr`, `__fern_ascii_run`) run 32-byte AVX2 main loops —
`vmovdqu` / `vpcmpeqb` / `vpbroadcastb` / `vpmovmskb` on `ymm`, with no cpuid
check anywhere — ahead of their 16-byte SSE2 ones, through the five VEX forms
its in-process assembler encodes (`x86_native.fern`), and the `clz` / `ctz` /
`popcount` ops lower to `lzcnt` / `tzcnt` / `popcnt`, so its output sits at v3.

x86-64-v3 is the standard name for the class that has it, and no real
part carries AVX2 without v3's other bits. The AMD floor moves with it: Jaguar
and Piledriver are AVX1 parts and never ran this output.

**AES-NI is a raise, and it drops no hardware.** The psABI levels leave AES-NI
out of every one of them, v4 included, so std/crypto/aes_gcm's kernels
(`__aes_expand_key`, `__aes_ctr32`) take it as a separate bit (#9858). Every
AVX2 part from Intel and AMD has it — the Pentium and Celeron lines that
shipped without AES-NI also shipped without AVX — so the parts the v3 floor
admits are the parts this one admits. The reason not to do without it is
constant time: a table-driven software AES leaks its key through the cache,
and the constant-time software wasm runs instead seals about 25 MB/s under
wasmtime where the instructions seal about 1.1 GB/s (x86-64, 64 KiB messages,
2026-10-07).

wasm has no AES or carry-less multiply instruction, so its AES-GCM kernels are
software that never branches on or indexes by a secret: a bitsliced AES over
`i64`, four blocks a pass, and a GHASH that multiplies a bit at a time under
masks (`compiler/wasm_aes.fern`).

Anything above this level (AVX-512, …) needs runtime dispatch first, and none
of it is used. AVX-512 is deliberately not taken: Intel removed it from
consumer parts at Alder Lake, so a v4 baseline would drop hardware a v3 one
keeps.

**The assemblers encode more than the baselines cover, on purpose.** An
assembler that cannot spell an instruction cannot be told to gate it, so
`x86_native.fern` and `arm64_native.fern` accept every mnemonic in the tables
`cmd/x86tblgen` / `cmd/arm64tblgen` generate from `internal/tables/x86tbl` /
`internal/tables/arm64tbl`, unconditionally; what a *code generator* may reach for is
the baseline's question, not theirs.

**What the arm64 raise cost, and what it did not buy.** The crypto extensions
are what `pmull.1q` needs (#9128), and the only declared hardware without them
was the Raspberry Pi — Broadcom omits them on the Pi 4 (BCM2711) and Pi 5
(BCM2712), which is a licensee choice rather than a core limitation, since
ARM's own A72 and A76 have them. Graviton 2 and later, Apple Silicon, and
mainstream Android SoCs all carry them. x86-64 paid nothing at all for the
matching `pclmulqdq`: it is Westmere-and-later, older than the Haswell line,
and the cut-down Haswell Pentium/Celeron parts that lack it also lack BMI1, so
the existing baseline already excluded them.

**SVE is NOT in the arm64 baseline and cannot be, whatever else is raised.**
Apple Silicon has no SVE through M4, so the common denominator across Graviton,
Apple and Android tops out below it. "Modern" on this architecture means
ARMv8.2-A plus the optional feature bits every one of those parts implements —
it does not mean the newest instruction set ARM has published.

**qemu-aarch64 cannot check any of this.** Every named core model it offers —
`cortex-a53`, `a55`, `a72`, `a76`, `max` — executes `pmull v0.1q` including
`cortex-a72`, the Pi 4's exact core, and the crypto bits are not a settable
property. So the emulator runs whatever we emit and can neither confirm nor
refute a baseline claim; the qemu lane is a correctness gate, not a portability
one. Under the old baseline that was a hazard (green in CI, SIGILL on a Pi);
under this one there is nothing left below us for it to miss.

---

## Supported OS / runtime versions

We support only the **latest** version of each target's host OS or
runtime. The CI runner labels reflect that policy:

- Linux: `ubuntu-latest` (tracks the current Ubuntu LTS image).
- macOS: pinned to a specific recent label (currently
  `macos-15` = Sequoia, on Apple Silicon arm64). We deliberately
  do NOT use `macos-latest` because that floating label has
  shipped beta / regressed images in the past. The pin gets
  bumped in a dedicated PR after verifying the new version
  works (build + test + examples). Pinning to anything OLDER
  than what's currently in `.github/workflows/macos.yml` is
  explicitly not supported.
- wasm: `wasmtime` pinned to a specific version in `mise.toml`. Bumps
  land as part of the dep refresh cycle.

If a future macOS release breaks something:
1. **Preferred**: fix the codegen for the new version.
2. **Acceptable for short-lived breakages**: document the regression
   in this file's "Known limitations" section with a tracking PR
   reference.
3. **Not supported**: pinning CI to an older `macos-N` label to dodge
   the breakage.

---

## Known limitations

Items that are known-broken in some configuration but considered too
costly (or too speculative) to fix right now. Each entry should have a
concrete fix plan and a rough scope estimate.

### Line coverage (`-cover`) is x86-64-linux and arm64-linux only

`-cover` (#5548, `docs/COVERAGE.md`) instruments every executable source line
and every source-level conditional with counters and dumps the table at exit.
The self-host instruments the source (`cover.fern`), and the counter table
and its report are written on the raw-memory floor (`__raw_cover`,
`__raw_alloc`, `__raw_load_ptr`, …), which the wasm emitter does not lower.

The driver refuses `-cover` for every target but x86-64-linux and
arm64-linux rather than producing an uninstrumented binary. A coverage run
that silently measures zero is the failure mode that refusal exists to
prevent.

Fix plan for wasm: the counter table and report as hand-written WAT helpers,
the way wasm serves the other raw-floor runtime helpers (`wasm_ir.fern`).
Scope: the report loop is the work — the counter bump itself is `i64.load` /
`add` / `store`.

### Heap exhaustion exits 125 on the natives, traps on wasm

The native targets exit 125 when the arena runs out (`.Lalloc_oom` in
`asm_ir.fern` and `asm_arm64_ir.fern`). On wasm the equivalent event is
`memory.grow` returning -1, and `$__fern_alloc` raises `unreachable` there —
so the failure is attributable to the allocator, with its caller chain, but
the process dies as a trap rather than carrying a status
(`compiler/wasm_ir.fern`'s `$__fern_alloc`).

Fix plan: call `$__fern_proc_exit` with 125 instead of trapping. The cost is the
reason it has not been done — the import-free component core (mode 1 of
`wasm_ir.fern`'s module emitter) has no `$__fern_proc_exit`, so wiring the allocator
to it puts a WASI import into every allocating module. Scope: small in the
emitter, wide in what it perturbs.

### Tail-call optimisation — every target

The typed lowering turns a self-recursive call in tail position, and a tail
call modulo cons, into a loop before any emitter sees the body
(`ssasem.tail_recursion`, #9692, #10462), so self-tail recursion runs in
constant stack depth on x86-64, arm64 and wasm
(`internal/testing/e2ecompiler/self_host_sem_tail_recursion_test.go`).

### Pointer-width handling on arm64-darwin's high heap — how it works

**The regime is testable without a Mac.** Linux honours the arena's mmap
address hint, so `__fern_alloc` puts the heap at 0x1000_0000 (256 MiB) and
every Linux lane runs with heap pointers that fit in 32 bits; macOS ignores
the hint and relocates the mapping above 4 GiB. A pointer handled 32 bits
wide is therefore correct on every cheap lane and wrong only on Apple
hardware. `FERN_HIGH_HEAP=1` in the compiler's environment raises the hint to
0x2_0000_0000 (8 GiB) at emit time (`asm_arm64_ir.fern`), and qemu-aarch64
honours the raised hint. The gates are `internal/testing/e2e/arm64_high_heap_test.go`
(`TestArm64HighHeap*`, picked up by the ordinary `-run TestArm64` selection)
and `internal/testing/e2ecompiler/self_host_arm64_high_heap_test.go`
(`TestSelfHostArm64HighHeap*`). What neither reproduces: only the arena moves,
so a truncation of a `.rodata`, image or stack address still needs the
`macos-15` lane, where the `map_heap_string_values` case in
`internal/testing/e2e/arm64_darwin_native_test.go` round-trips concat-built keys and
values through a `Map[string, string]`.

---

## Performance / memory wins

Tracked here because the language targets lightweight CLI tools
and edge-handler-style HTTP servers — every byte allocated per
request and every cycle on the hot path matters. Each entry has
a rough impact estimate (mem / speed) and a sketch of the
design. Mostly **breaking** changes — sequence
them with care.

### 1. Inline small strings (SSO)

**Impact:** zero-alloc for strings ≤ N bytes; significant for
short keys, status codes, header names. Every runtime-built
string is a heap allocation today: a string is a two-word box,
`[data, len]`, on the natives and one `[len][bytes]` block on
wasm, with no inline form on any target.

### 2. Per-instantiation Map entry sizing

**Impact:** `Map[i32, i32]` entries drop from 16 B → 8 B on the
natives (50% memory). `Map[i32, u8]` would drop further (4 B +
1 B = 8 B with alignment).

Every `core/map` entry is `2 * __ptr_width()` bytes whatever K
and V are (`internal/stdlib/core/map.fern`). Sizing it per
instantiation is the per-(K, V) monomorphisation in
`docs/MAP-SPECIALIZATION.md`.

### 3. Reduce the 4-byte length prefix to varint for short strings

**Impact:** small (saves 0–3 bytes per string), but cumulative
on JSON / HTTP payloads with many short field names. Probably
not worth the complexity. The 4-byte prefix is wasm's string
block; a native string box holds its length in a full word.

**Status:** mentioned for completeness; would interact with SSO
(Item 1 in this list).
