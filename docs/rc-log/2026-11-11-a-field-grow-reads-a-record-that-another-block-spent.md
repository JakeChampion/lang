# A field grow reads a record that another block spent

Nightly fuzz seed 160218 (#12868) gave a use-after-free under `-sanitize` on x86-64 and arm64.
The oracle and the plain build gave the correct exit status.

## The shape

```fern
struct S0 { f0: i32, f1: string }

function main(): i32 {
  let b: S0 = S0 { f0: 36, f1: f"jle{51}pzio" };
  let d: string = b.f1;
  let n = 0;
  if (d.len() > 100) {
    n = 1;
  } else {
    let e: S0[] = [b];
    n = e.len();
  }
  let x = d + "x";
  print(d.len() + n);
  return 0;
}
```

- `d` is a read of the field `f1` of the record `b`.
- One arm moves `b` into an array and releases the array. The other arm releases `b`.
- After the join, `d + "x"` appends to `d`, and `d` is read again.

## The cause

`ssaunits.field_grow_root` gives an append the record that its left operand was read from.
The lowering then emits the field grow: it tests that the count of the record is 1, grows the string in place, and clears the field of the record.
`projection_root` checks that the record is owned and that nothing needs the field after the append.
It did not check that the record is still there.

In this program each arm spends the record before the join.
The grow then read the count word of a freed box.
When the word read as 1 and the string had room, the grow also wrote the field of the freed box.
The sanitizer reports that write at exit.

The certifier did not refuse the function.

## The rule

`field_grow_root` now refuses a record that has a use in another block, unless the use is a field read.
A move, a call, a phi or a terminator in another block can spend the box before the grow runs.
A use in the block of the grow keeps its present rules.

The cases that still grow in place:

- The read and the append are in one block.
- The record is only read in the other blocks. A loop that appends to a field is the common form.

## Tests

`TestX86_64FieldGrowSpentRecordSanitized`, `TestArm64FieldGrowSpentRecordSanitized` and `TestWASMFieldGrowSpentRecordSanitized` in `internal/testing/e2e/field_grow_spent_record_test.go` run the shape under the sanitizer.
The two native tests fail without the rule.
