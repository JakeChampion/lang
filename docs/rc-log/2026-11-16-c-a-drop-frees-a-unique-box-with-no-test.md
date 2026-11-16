# A drop frees a unique box with no test

2026-11-16. All native targets. The change is in `ssarc`.

## The shape

The drop helper of deriv made 340,715 of the calls of
`__fn___fern_box_dec.u` at deriv 8. Before each call, the inline release
made three tests of the box:

- It is not an immediate value.
- Its count is not more than 1.
- It is not immortal.

The helper had already proved each of those boxes unique with
`__fern_rc_is_unique`.

## The change

The release of a box that a test of the drop helper proved unique has the
mark `ir.sole`. The emitter then calls the `.u` entry of the release with no
inline test, as it does for `classified_death`. These releases take the mark:

- a child with no counted field in `pushed_field`
- the cell that `popped` frees
- the node that the loops of `drop_variant_chain` and `drop_variant_stack`
  free

Each of these boxes is unique at its test, and nothing increments its count
after the test. The node of the call is not freed there, because its caller
frees it. The certifier does not read the mark.

## Measured

Cachegrind on x86-64, against the build with the routine of each box size:

| workload | before | after | change |
|---|---:|---:|---:|
| nqueens 10 | 22,659,528 | 22,545,714 | -0.5% |
| deriv 8 | 103,477,888 | 101,506,675 | -1.9% |
| rbtree 420,000 | 575,618,860 | 575,618,866 | 0.0% |
| rbtree-ck 420,000 | 899,385,423 | 885,488,621 | -1.5% |
| cfold 16 | 31,189,815 | 30,995,351 | -0.6% |

Best processor time: deriv 10 goes from 969 ms to 903 ms in 15 runs.
rbtree-ck 4,200,000 goes from 1748 ms to 1637 ms in 7 runs. The answers on
x86-64, and on arm64 under qemu, are the same.

## Not done

`drop_value_inline` can free a unique box in its own arm, and give back a
count in the other arm. The release helper uses that function, so each
outlined release can take the change. cfold 16 then ran 1.7% fewer
instructions. But the four other workloads ran 0.7% to 2.4% more, because
the release is in the code twice.
