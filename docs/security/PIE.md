# Position-independent executables

Status: implementation for [SEC-027](https://github.com/JakeChampion/lang/issues/13529), with physical arm64 verification open.
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The image layout and the permissions it keeps are in [ELF-INPUTS.md](ELF-INPUTS.md) and `docs/HARDENING.md`.

## Contract

`-pie` builds a position-independent executable for `x86-64-linux` and `arm64-linux`.
The image is `ET_DYN` at base 0 with no interpreter and no dynamic section.
The kernel therefore loads it at a random base under its address-space randomization.
The two load segments keep the permissions of the static image, the stack stays non-executable, and `-harden` and `-g` compose with the flag.
`-shared`, `arm64-darwin`, `arm64-android`, which is position-independent already, and the Wasm targets refuse the flag with exit 2.

Code on both targets is position-independent as emitted: x86-64 code addresses data through `%rip`, and arm64 code through `adrp`.
The absolute words are in the data segment: string boxes, constant aggregates, function values, trait object tables, and the words a `match` compares an enum shape against.
A startup loop moves them before the first function runs.

| Target | How the image relocates itself |
|---|---|
| arm64 | The data section carries four tables between labels, and the loop at `_start` adds the load delta to each table entry. The delta is the difference between the runtime and the link-time address of an anchor word. Android used the same loop before this change, and a static arm64 image runs it with a delta of zero. |
| x86-64 | The linker already lists every absolute data word for the shared-object writer. The writer appends that list to the data section, fills two slots with its address and its length, and the loop at `_start` adds the delta to each listed word. |

One x86-64 instruction form was position-dependent.
A `match` on an enum compared the value's shape word against the shape's address as a 32-bit absolute immediate.
Under `-pie` the backend emits the compare against a data word that holds the address, and the loop relocates that word with the rest.
The linker refuses a `-pie` image that still holds a 32-bit absolute code address.
A new position-dependent form is therefore a build error, not a crash.

## Verification

`TestSelfHostPIE` compiles one program for each target with `-pie`, with `-pie -harden`, and with `-pie -g`.
The program exercises string boxes, a constant aggregate, a function value, a trait object, an enum `match`, and a runtime-module error path.
It also prints the base of its own text mapping from `/proc/self/maps`.

- The headers must say `ET_DYN` with the first load at virtual address 0. They must carry no interpreter and no dynamic segment, a non-executable stack, and no segment that is both writable and executable.
- The program must print the same lines as the static build and a text base that is not the static `0x400000`.
- On x86-64 a second run must print another base when `randomize_va_space` is not 0, which pins the kernel's randomization.
- The `-g` image must carry the function symbols.
- The refusals for `-shared`, `wasm32-wasi` and `arm64-darwin` must exit 2 with their reason.

On the Linux 6.18.44 host the x86-64 text mapped at `0x7f6ab9553000`.
Under QEMU 8.2.2 user mode the arm64 text mapped at `0x7fb6d0ad0000`, a base that QEMU chooses itself.
Before the x86-64 compare change, a `match` on `IoError` took the wrong arm, because the compare read the link-time shape address.
The same program then crashed in the next string concatenation.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostPIE' -count=1 -timeout 0
```

## Remaining limits

- Randomization on physical arm64 hardware is unverified. QEMU user mode loads the image at a base it chooses, so the arm64 evidence shows relocation at a non-link base, not the kernel's randomization.
- `-pie` is opt-in. The static image at `0x400000` stays the default, because the perf and size lanes pin it.
- The relocated words stay in the writable data segment. A read-only segment after relocation, the `RELRO` shape, needs a third load segment and is open under SEC-028.
- The unwind tables of the `-pie` image are written for base 0. The runtime's own backtrace walks frame pointers and needs no table, and an external unwinder must add the load base.
- `-shared` keeps its own writer with dynamic relocations, and Android keeps its writer.
