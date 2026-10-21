# 2026-10-21 — the owned append grows in one frame

`__fern_arr_push_owned`, the runtime's sole-owner append, on x86-64 and
arm64. Refs #8171.

## The shape

An `a = a.append(v)` with a free slot appends in place at the call site;
the kernel is called only when the receiver is full, shared or a static
literal. In the whole-compiler self-compile the owned kernel is called
24.8 M times: 13.0 M first appends to an empty static array and 11.8 M
grows of a full sole-owned buffer. A grow went through the wrapper: a
frame to save the old pointer, a call into the shared slow path, whose
own frame and re-checks led to the box shim and the copy, then a free
that re-read the rc and the capacity the fast path had already proved,
about 97 instructions besides the copy. The three kernels and the box
shim were 4.5% of the compile.

## What changed

On the plain build the owned kernel grows in one frame. The rc read at
entry sends a static literal or a shared receiver to the shared slow
path, which copies and frees nothing, as before. A full sole-owned
receiver pops its new block from the freelist class as the box shim
would, copies the elements, and pushes the old block onto its class,
with nothing re-read: about 59 instructions besides the copy. The copy
loop both grow paths run is one helper, so the shared kernel's text is
unchanged. The debug, census and counter builds keep the wrapper, whose
hooks count and check every allocation and free.

## Measured and not landed

Two growth policies, measured on the same subject against the same base:

| policy | Ir | peak RSS of the compile |
|---|--:|--:|
| the first append allocates 8 slots, not 4 | 73.626 G (−0.73%) | 2000 MB (+2.6%) |
| the first grow goes from 4 slots to 16, not 8 | 73.968 G (−0.27%) | 1957 MB (+0.4%) |

Either trades memory in every Fern program for instructions; the 8-slot
start costs 32 bytes on every array of one to four elements. Neither is
this slice's to decide, so both are recorded here and on #8171. The
grows themselves say where the arrays are: 8.3 M of the 11.8 M copy
fewer than eight elements, so most are the one grow from 4 to 8.

A one-pass dead-code prune in `ssa.register_form`, marking each root as
it is met instead of listing the roots and marking from the list
(#12919, withdrawn): 74,256,937,527 Ir without it against 74,256,771,737
with it, on the same base, −166 K. The -g profile had put the old prune
at 930 M of self time, which the -O build does not bear out.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.167 G | 73.741 G (−0.57%) |

The base is main at c18a970 with the one-pass prune above applied, which
measures within 0.2 M Ir of the tree without it. The emitted assembly of
the subject differs from the base compiler's only inside
`__fern_arr_push_owned`; the program's own code is identical. Peak RSS
and wall time of the compile are unchanged.

## What is left

The first append to an empty static array still goes to the shared slow
path's four-slot pop through two checks the owned kernel already made,
13.0 M times. The box shim itself is 81 M calls at 17 instructions,
60 M of them from the compiler's own struct constructions.
