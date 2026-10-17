# Native register pools and fixed clobbers

The register allocator assigns SSA values to homes. A home is distinct from a
parameter's incoming ABI location and from a temporary the emitter uses while
implementing one instruction. Expanding a home pool must account for all three.

## ARM64

| Role | Registers |
| --- | --- |
| Caller-saved homes | x0, x9–x15, x1–x3, x8, x30 |
| Callee-saved integer homes | x19–x28 |
| Emitter temporaries | x4–x7 |
| Address/frame expansion temporaries | x16–x17 |
| Fern integer argument locations | x0, x9–x15; later arguments stay on the stack |
| Fern floating argument locations | d0–d7, according to the parameter mask |

`asm_arm64_ir.ssa_nregs` counts caller-saved homes; `ssa_nargs` independently
limits argument locations. Increasing the former must not create d8+ argument
locations: d8–d15 hold callee-saved floating values.

The additional x1–x3/x8 uses in the register emitter occur in host calls,
runtime calls, string views and allocation/append paths. The allocator already
treats those instruction kinds as calls, including their inlined fast paths,
so a value live across one takes a saved home or a spill. Release entry guards
run before parameter homes are initialized. x4–x7 and x16–x17 remain reserved
because ordinary memory and arithmetic emission uses them between SSA values.

A function using x30 as a value home saves its incoming return address in a
frame record first, even when it makes no calls. Profiling and hardened
frames preserve the same saved control address. A thirteen-parameter leaf
regression executes such a function and checks its save and home.

The fused carry uses only reserved x4–x6 temporaries. Paired-return
transport operations remain kind 59 caller-clobber sites: values held across
a word transfer take saved homes or spills, protecting the auxiliary x1
result. A thirteen-argument paired call exercises that boundary.

Runtime calls pass operands in x0–x2. These now overlap multiple homes, so
`ssa_load_abi_args` uses the same parallel-move resolver as phi and call
moves. Loading just the x0 argument first is insufficient: swapping a pointer
in x1 with a length in x0 overwrites the pointer. The extra-caller regression
executes that swap, a ninth stack argument, and an indirect call with nine
floating arguments and a live saved floating value.

## x86-64 audit

The current caller pool is rax/rsi/rdi/r8/r9/r10; rbx/r12–r15 are saved homes.
The remaining three general registers have uses throughout the register emitter:

| Reserved register | Uses that constrain expansion |
| --- | --- |
| rcx | Variable shifts/rotations, binary fallback's right operand, inline memory/string helpers |
| rdx | Division and high multiplication, memory addressing, raw operations and inline RC guards |
| r11 | Cached fallback loads, large immediates, memory operations, indirect dispatch and parallel-move parking |

`ssa.regalloc_linear` already excludes rax over the binary operations that fix
it. It does not yet describe all the rcx/rdx/r11 clobbers above. Merely appending
those registers to the caller pool would leave values live in emitter temporaries.

The x86 follow-up (#12210) needs instruction and edge constraints, including
operand/result restrictions where a temporary is written before an input is
consumed. Runtime argument moves need cycle handling when rcx/rdx become homes,
and parallel-copy parking must preserve an allocated r11. These constraints
must cover fast paths as well as actual calls. Native compiler timings and
register-pressure workloads determine whether an expanded allocation policy
pays for its extra analysis and moves.

`bench/crypto/x25519.fern` and `scripts/native-pair-bench` provide a native
same-host comparison; the Perf workflow's optional `native_baseline` mode runs
it on Apple Silicon. Every timing is retained rather than used as a flaky CI
threshold. The existing instruction, allocation and driver-size gates remain.
