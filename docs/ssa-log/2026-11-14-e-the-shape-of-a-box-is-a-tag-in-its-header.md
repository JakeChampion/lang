# The shape of a box is a tag in its header

2026-11-14. x86-64 and arm64. Follows
`2026-11-14-d-a-box-has-a-header-of-one-word.md`.

## The shape

A record and an enum variant kept the address of their shape in their first
word. The header word of a box held the count in its low half and the number
of words in its high half. An `Add` of two fields took 32 bytes and a
red-black `Node` took 56. Koka uses 24 and 48.

On deriv 10, Fern ran 6.6 billion instructions and Koka ran 7.5 billion. Fern
took 33.0 million last-level cache misses and Koka took 26.5 million.

## The change

The high half of the header word holds a tag, and a box has no shape word.

- The low 16 bits of the tag are the number of words of the box.
- The bits above them are zero for a tuple. For a record or a variant they
  are one more than the index of the shape in the declarations of the
  program (`asmcore.shape_tag`). Each unit of a program has the same
  declarations, so each unit gives one shape the same tag.
- Field i is at offset 8 * i. A record with no field has one word.

The allocation and the tests:

- `__fern_rec_box` writes the number of words in the high half, as before. A
  constructor of a record then stores the tag over it with a store of 32
  bits (`lift_block_make`, `lift_struct_set_shape_prod`).
- A `match` loads 32 bits at offset -4 and compares them with a constant
  (`lift_variant_is_prod`). It loaded a word and an address before.
- `__fn___fern_box_dec` and the reuse test read the low 16 bits of the tag.
- A box of more than 65,535 words keeps the header of an array, because its
  number of words does not fit the tag.

The other users of a shape:

- A `dyn` box of a primitive is a box of one word with the tag of its type.
  The dispatch compares the tag.
- A constant record has `.long -1` and `.long` of its tag in its header.
- The debug information of an enum has the tags as its values. A record with
  no field keeps its tag in its one word, where the debugger reads it.
- The reuse constant of the IR is `ir.op_record_slots`. The wasm backend
  keeps the shape in the first slot of a record and takes the constant. The
  native lift takes one less.

The data of a shape, `.S<n>` or `__fern_shp_<name>`, is gone, and so are
`shape_ref`, `shape_label`, `intern_shape` and the shape literals of a unit.

A program can have 32,766 shapes and a record can have 65,535 fields. The
compiler stops with a message for a program over a limit.

## The passes and the models

- `ssatokenkeep`, `ssa.share_shape_loads` and `ssadeps.boxed_values` read the
  tag at `ssa.tag_offset()`. A load of the tag proves that its base is a box,
  as a load of a field does.
- The credit proofs (`ssacreditorigin`, `ssacreditreuse`,
  `ssacreditreplace`, `ssacredithelper`, `ssacreditselected`,
  `ssacreditabi`) take the tag store at -4 and fields from offset 0. A
  record of two fields has two words.
The text models:

- `ssaconstructorbound` reads the tag from the one 32-bit store at -4 of the
  body. Its low 16 bits must be the number of fields.
  `ssaconstructormachine` requires the tag in the header and each field in
  its word.
- `ssaboxroutines` has the new text of the two box routines.
- `ssadecrementtext.readonly_ascii` and the driver `ssastaticshapes` read
  the bytes of a shape. They are deleted.

## Measured

x86-64, this container. Best CPU time of seven runs, in milliseconds, each
program with no stack limit, and peak resident memory:

| workload | before | after | Koka | memory before | memory after | Koka |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rbtree 4,200,000 | 783 | 754 | 605 | 226 MB | 194 MB | 132 MB |
| rbtree-ck 4,200,000 | 2423 | 2348 | 1736 | 1608 MB | 1376 MB | 1151 MB |
| deriv 10 | 1270 | 1099 | 1036 | 566 MB | 438 MB | 459 MB |
| cfold 20 | 190 | 167 | 192 | 184 MB | 176 MB | 156 MB |
| nqueens 13 | 875 | 838 | 809 | 126 MB | 94 MB | 99 MB |

Cachegrind, with the small inputs:

| workload | Ir before | Ir after | D1 misses before | D1 misses after |
| --- | ---: | ---: | ---: | ---: |
| rbtree 420,000 | 712,829,992 | 712,829,473 | 1,106,834 | 949,198 |
| rbtree-ck 420,000 | 1,083,094,850 | 1,083,091,639 | 5,531,033 | 4,807,975 |
| deriv 8 | 114,073,720 | 114,636,098 | 644,392 | 490,972 |
| cfold 16 | 39,562,978 | 39,671,103 | 681,067 | 604,452 |
| nqueens 10 | 28,907,702 | 28,907,651 | 82,041 | 61,131 |

The compiler compiles itself with a peak of 2409 MB, down from 2541 MB. Stage
2 and stage 3 are the same file.

## Traps

- The times on this container moved by 30% between two sets of runs on one
  afternoon. Compare the two programs in one set, and trust the counts of
  Cachegrind more than the times.
- A pass that matches a load or a store at offset 0 of a box now matches a
  field. The tag is at `ssa.tag_offset()` with the mark `u32`.
- `ssadeps.boxed_values` did not count the load of the tag as a load from
  the box. Each uniqueness test after a `match` then kept its two pointer
  guards, and cfold ran 6% more instructions.

What stays open:

- A red-black `Node` has 48 bytes and Koka has 40, because Koka packs the
  colour and the boolean in one word. That is the next gap in size.

A failure of main:

- `ssaretained_run` stops on main at `ssauniqueselectedunit` line 102 for
  arm64, with `unproved uniqueness final unit`, before this change and after
  it. The checks after that line did not run here for arm64.
