# 2026-09-21 — an ascription names a destination

`examples/tests/ndarray_test` produced **0 of its 254 declarations** because of
one line:

```fern
var none: ndarray.NdArray[i32] = ndarray.from_flat([] as i32[], [0, 3]).map_rank(1, first_one);
```

Nine lines are the whole of it:

```fern
function main(): i32 {
    return total([] as i32[]);
}
```

```
FERN_SEM_IR: main: unresolved array literal type
```

## The operand got no destination

`e as T` reaches `semsource.cast` as a unary whose operator names T, and the
operand was evaluated with nothing:

```fern
var operand: Value = expr(u.operand, s, typeinfo.unchecked());
```

So an empty literal was asked to name its own type, which it cannot. T was in
hand two lines below — `checked(e, s)` answers `i32[]` — but read after the
operand rather than handed to it. `destination_type` exists for exactly this
and a binding's annotation reaches it; a cast's did not.

## And the identity was not admitted

Supplying the destination is not enough on its own. With the operand typed the
cast then refuses on its own contract:

```
FERN_SEM_IR: main: cast contract: i32[] as i32[]
```

`cast_admits` covers the usize/reference reinterpretation, the integer
conversions and the float ones, and nothing else. `T as T` over a reference was
refused — although the parser writes that cast as a zero-cost identity and says
so in its own comment.

## One rule answers both

A cast to a type the conversion vocabulary does not convert TO — anything that
is not an integer, a char or a float — is an ASCRIPTION: T is the operand's own
type written down. So T is the operand's destination, and a value already at T
passes through unchanged, no instruction emitted. A numeric target stays a
conversion, where the operand keeps its own width and the cast changes it.

`ssasem.cast_ascribes` is that test, kept beside `cast_admits` so the two read
off the same vocabulary rather than a second copy of it.

## Why one line cost a whole file

The refusal does not stay in its own function. A refused declaration's hoisted
bodies are built by the AST lowering, and `semlower.ast_value_call` refuses any
PRODUCED body that calls a function value of matching arity, because the two
lowerings would disagree about who releases what. The matching value here is
the callback `test.TestRunner.it` takes — the method every test in the file
goes through. So `it` fell to the AST lowering, and every declaration it
reaches followed.

That is why the census reported the file under `calls a function value of 0
arguments, a type the AST lowering builds a value of` rather than under this.
The mixing rule is an amplifier, not a root: it fires because a sibling
refusal left a value behind, and it disappears when the sibling is fixed. Worth
recording, because ranking census leaves by the reason string alone points at
the amplifier and away from the cause.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`.

| program | before | after |
|---|---|---|
| `examples/tests/ndarray_test` | 0 of 254 | **254 of 254** |

It passes 17 of 17, matching native.

Corpus census, 864 seeds, both columns against frozen binaries:

| | before | after |
|---|---|---|
| programs produced whole | 841 | **842** |
| declarations produced | 77,287 of 78,504 | **77,541 of 78,504** |

**Exactly one file moves and +254 is exactly its gap.** Nothing regresses. The
remaining corpus gap is 963 declarations over 22 files.

On the test-row program the typed leg reclaims the shape whole,
`allocs=4 frees=4 live_bytes=0`, where the AST leg strands **80 bytes**.

## Tests

`an-ascription-names-a-destination` — 3 of 3, `noLeak`, all four targets,
differential against the AST leg. It carries the empty literal, the non-empty
identity, a payload-less `None as Option[i32]`, an ascription in argument
position, and `(260 as u8) as i32` — which still answers 4, not 260, so the
numeric conversion is pinned to have stayed one.

That the row can FAIL was checked rather than assumed: with the two compiler
sources reverted it goes red on all four targets, on the produced count and on
`noLeak`.

Issue: #9940.
