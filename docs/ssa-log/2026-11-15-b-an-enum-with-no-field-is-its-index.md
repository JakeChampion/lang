# An enum with no field is its index

2026-11-15. x86-64, arm64 and wasm. Follows
`2026-11-15-a-a-compare-reads-its-operand-from-memory.md`.

## The shape

A value of an enum whose variants hold no field was the address of a constant
box. A `match` on the value loaded the address from its field, and then
loaded the tag of that box. The red-black `Node` keeps its colour in such a
field. On rbtree 420,000 the insert function tested a colour 24 million
times.

## The change

A value of such an enum is the index of its variant. The enum must have no
`drop`. `ssasem.immediate_enum` is the rule, and `ssasem.counted` already
gave such a value no count.

The lowering (`compiler/ssarc.fern`):

- A constructor is `const_i32` of the index (`immediate_site`).
- A variant test is a compare of the value with the index.
- A constant record holds the index in the word of the field
  (`ssaunits.enum_word`). The enum has no constant box
  (`ssaunits.placeable`).

A dyn value is a box that holds a tag, and an index has no tag:

- `ssasem.boxed_enum_dyns` changes the widening of such a value to a
  `dyn_box`. The box has one word, which holds the index. Its tag is the tag
  of the first variant (`ssarc.dyn_box_shape`).
- The dispatch of each backend finds the enum from that tag. For an immediate
  enum it passes the word of the box to the method
  (`irtables.enum_immediate`).
- `parser.drop_provider` is the one rule for a `drop`. The front end and the
  backends use it, so the two sides agree which enums are immediate.

The other users:

- The wasm import bridge of a plain enum passes the value through. The value
  is the discriminant of the interface type.
- The debug information describes the variable as the enumeration, with the
  indexes as its values. It was a pointer to an enumeration of tags.
  `asmcore.dbg_shape_type` and `dbg_enum_members` wrote the tags, and they
  are deleted.

The reference count guards do not change. They skip each value below 65536.

## Measured

Cachegrind instruction counts, x86-64, with the small inputs:

| workload | before | after | change |
| --- | ---: | ---: | ---: |
| rbtree 420,000 | 660,042,425 | 635,643,778 | -3.7% |
| rbtree-ck 420,000 | 1,026,349,290 | 1,003,441,765 | -2.2% |
| deriv 8 | 112,697,949 | 112,697,949 | 0 |
| cfold 16 | 38,884,681 | 38,884,681 | 0 |
| nqueens 10 | 27,816,528 | 27,816,528 | 0 |

Koka runs 593,463,215 instructions on rbtree 420,000. The D1 misses do not
change, because a `Node` keeps its 48 bytes.

Each of the 626 programs of `tests/stdlib` gives the same output and the same
exit status before and after. Stage 2 and stage 3 of the compiler are the
same file.

## Traps

- A dyn value of a fieldless enum crashed the first version: the dispatch
  read a tag at the address 0 or 1.
- The backends do not see the finalizer rows of the front end. They find a
  `drop` from the function declarations with the same rule.
- The assembler of the wasm backend does not accept `offset=` on a load.

What stays open:

- The index takes a word of 8 bytes in a box. A `Node` can have 32 bytes
  when the key of 32 bits, the colour and the boolean share one word. Koka
  has that layout. This is the next step.
