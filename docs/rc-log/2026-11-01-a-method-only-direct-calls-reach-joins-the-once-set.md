# 2026-11-01 — a method that only direct calls reach joins the once set

`seminline` splices a body into its one caller when the body is unnamed:
nothing but direct calls reaches it. `semsource.outside_unnamed` refused
every name with a `.`, so a method was never in the once set, and never a
shared or an unpacker splice. The rule `outside_uncalled` already admitted a
method that no export, interface, dyn arm, derive or Drop names.
The pair and scalar passes used that rule, the splice plan did not.

## The change

- The splice plan and the body drop use `outside_uncalled`. A method that
  only direct calls reach is spliced as a private function is, and its body
  is dropped when every caller took it.
- A method joins the once set only when its body is one block
  (`semsource.spliceable_body`), for the reason below (#13990).
- The eq and hash methods of a keyed map key stay, because the key closures
  of `ssarc` call them by name after the plan is decided.
- The pair and scalar passes keep their own flag, `uncalled`, which admits
  every method as before.

A branch in a spliced body puts the caller's store-back after a join. The
plan takes an element out of its slot only within the block that reads it.
With every method admitted, `TestSelfHostServeAllocsPerRequestH3Mux`
measured 42.3 allocations a request against 28.8 on main. The splice of
`h3q.Conn.respond` alone accounts for 8 of them.

## Measurement

x86-64, `-O`, instructions from callgrind. `seq.txt` is `seq 1 1000000`.

| Workload | Main | One block | Every method |
|---|--:|--:|--:|
| `cat -n` of `seq.txt` | 212,035,827 | 212,035,428 | 210,054,571 (-0.93%) |
| `nl` of `seq.txt` | 242,785,318 | 242,785,298 | 241,744,787 (-0.43%) |
| `uniq` of `seq.txt` | 201,865,359 | 201,865,346 | 201,865,052 |
| `fold -w 40` of `seq.txt` | 223,184,730 | 223,184,736 | 223,184,198 |
| `shuf -i 1-1000000` | 391,364,788 | 391,377,021 | 391,361,840 |

The gain on `cat -n` and `nl` comes from methods with a branch, which the
one-block rule keeps as calls. With the rule, every binary keeps its size.
The compiler built by the stage-1 compiler reads 22,303,712 bytes against
22,307,776 on main. With every method admitted, `cat` grew from 98,384 to
102,464 bytes and `uniq` from 86,496 to 90,592. The compiler grew to
22,336,368 bytes.

The perf lanes measure `ordmap_insert.text` at -3.4% on x86-64 and -3.6% on
arm64 under the one-block rule. `pmap_insert.text` reads +2.4% and +1.5%.
Compiling `checker.fern`, the compiler allocates 1.39% more boxes and its
appends cross the shared cliff 2.96% more often. Those two rows are the
cost of the rule as it stands, and #13990 is where they are owed a cause.

The rc-log entry of 2026-10-30 predicted 1.9% on `cat -n` and 1.3% on `nl`
with a tiny limit of 8. The tiny limit stays at 4.

## Next lead

#13990: a take whose store-back sits past the join a spliced body adds.
When the plan takes across that join, or the reads of the slot merge where
the parent is not spent between them, the one-block rule can go, and the
`cat -n` and `nl` rows above return.

The scan in `semsource.unnamed` reads every instruction string once per
module to find a name reference. The only reference left to a method is a
dyn arm. `dispatched_arms` already lists those, so the scan can skip methods.
