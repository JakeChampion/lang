# 2026-11-01 — a method that only direct calls reach joins the once set

`seminline` splices a body into its one caller when the body is unnamed:
nothing but direct calls reaches it. `semsource.outside_unnamed` refused
every name with a `.`, so a method was never in the once set, and never a
shared or an unpacker splice. The rule `outside_uncalled` already admitted a
method that no export, interface, dyn arm, derive or Drop names.
The pair and scalar passes used that rule, the splice plan did not.

## The change

- The splice plan and the body drop in `semsource.unnamed` use
  `outside_uncalled`. A method that only direct calls reach is spliced as a
  private function is, and its body is dropped when every caller took it.
- `seminline.inline_leaves` takes one flag for both uses. The two arrays were
  the same.

## Measurement

x86-64, `-O`, instructions from callgrind. `seq.txt` is `seq 1 1000000`.

| Workload | Before | After |
|---|--:|--:|
| `cat -n` of `seq.txt` | 212,035,479 | 210,054,548 (-0.93%) |
| `nl` of `seq.txt` | 242,785,308 | 241,744,777 (-0.43%) |
| `uniq` of `seq.txt` | 201,865,366 | 201,865,080 |
| `fold -w 40` of `seq.txt` | 223,184,722 | 223,184,211 |
| `shuf -i 1-1000000` | 391,393,120 | 391,342,688 (-0.01%) |

No workload is slower. `cat` grows from 98,384 to 102,464 bytes and `nl`
from 178,144 to 182,240. The other four binaries keep their size. The
compiler built by the stage-1 compiler grows from 22,480,144 to 22,512,800
bytes (+0.15%).

The rc-log entry of 2026-10-30 predicted 1.9% on `cat -n` and 1.3% on `nl`
with a tiny limit of 8. The tiny limit stays at 4, so the gain is smaller.

## Next lead

The scan in `semsource.unnamed` reads every instruction string once per
module to find a name reference. The only reference left to a method is a
dyn arm. `dispatched_arms` already lists those, so the scan can skip methods.
