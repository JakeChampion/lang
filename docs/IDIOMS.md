# Idiomatic Fern

Status: [reference] the target style for every Fern source in this repository.

This document gives the rules that make Fern code idiomatic. Each rule shows
the old form and the new form. Each new form compiles and runs on the
interpreter, on x86-64 and on wasm32-wasi with the current compiler.

The old forms come from the TypeScript-shaped start of the language. They
still compile. Do not write them in new code. When you change a file, convert
the parts of it that you touch.

## Scope

The rules apply to every tracked `.fern` and `.fern.md` file, with six
exceptions:

- `conformance/cases/` is normative. A case shows one behaviour, and its
  expected diagnostics pin a line and a column. Change a case only to change
  the language.
- `testdata/` inputs stay as written. A test reads them as data.
- `tests/proposals/` holds evidence. Each file shows what a user wrote when
  the language stopped them (`tests/proposals/README.md`).
- `tests/probes/` holds probes. Each probe keeps the shape that showed one
  defect.
- `docs/benchmarks/` holds the programs that a dated measurement record
  measured.
- A program that tests one form keeps that form. Examples are a parser
  test, a differential case for an old form, and a probe for a bug in one
  construct.

`compiler/` follows the rules with the limits in
[Limits in the compiler](#limits-in-the-compiler).

## Control flow

### Use `for` for a counted loop

A `while` loop with a manual index is the old form. Use a range `for`.
`a..b` excludes `b`. `a..=b` includes `b`.

```fern
// Old
let i: i32 = 0;
while (i < n) {
  total = total + xs[i];
  i = i + 1;
}

// New
for i in 0..n {
  total += xs[i];
}
```

Use `for x in xs` when the loop body does not use the index.

```fern
for x in xs {
  total += x;
}
```

Keep `while` when the step is not one or the body changes the index. The
checker refuses an assignment to the index of a range `for` (E091). Also
keep `while` when the condition is not a bound. Use `loop` for a loop that only `break`
stops.

### Use `match` for a choice between values

A chain of `if` statements that compares one value with constants is the old
form. Use `match`. Use an or-pattern for arms that give the same result.

```fern
// Old
if (v == "allow") {
  return ALLOW;
}
if (v == "warn") {
  return WARN;
}
return 0 - 1;

// New
return match (v) {
  "allow" => ALLOW,
  "warn" | "warning" => WARN,
  _ => -1,
};
```

A `match` on an enum lists each variant. Write `_` only when the arms really
ignore the other variants.

### Use `if let` and `let else` to unwrap

```fern
// Old
match (lookup(k)) {
  Some(v) => {
    use_value(v);
  },
  None => {},
}

// New
if let Some(v) = lookup(k) {
  use_value(v);
}
```

Use `let else` when the code continues only with the value. The `else` block
must leave the scope.

```fern
let Some(v) = xs.get(0) else { return -1; };
```

### Use `?` to pass an error up

A `match` that only returns the `Err` or the `None` is the old form.

```fern
// Old
let r = parse(s);
match (r) {
  Ok(v) => { n = v; },
  Err(e) => { return Err(e); },
}

// New
let n = parse(s)?;
```

### Use a label in place of a flag

Use `break label` or `continue label` to leave an outer loop. Do not set a
boolean and test it after the inner loop.

```fern
outer: for i in 0..rows {
  for j in 0..cols {
    if (grid[i][j] == 0) {
      continue outer;
    }
  }
}
```

## Expressions

### Use `if` and `match` as expressions

When each branch only sets one variable, make the branch the value.

```fern
// Old
let label: string = "";
if (n > 3) {
  label = "big";
} else {
  label = "small";
}

// New
let label = if (n > 3) { "big" } else { "small" };
```

A branch can be a block. The last expression, with no `;`, is the value of
the block.

```fern
let size = match (shape) {
  Circle(r) => r * r,
  Rect(w, h) => {
    let area = w * h;
    area
  },
};
```

A function body is not a block expression. A function returns its value with
`return`.

### Use f-strings to build text

```fern
// Old
let msg: string = "line " + line.to_string() + ": " + text;

// New
let msg = f"line {line}: {text}";
```

Keep `+` when it joins two strings and nothing more.

### Write literals as the reader thinks of them

- Write `-1`, not `0 - 1`.
- Write `-5i64` for a negative literal of another width, not `-5 as i64`.
  The cast binds tighter than the minus, so `-5 as i64` is `-(5 as i64)`.
- Write `b'a'` for a byte, not `97`. Write `'a'` for a `char`.
- Write `x += 1`, not `x = x + 1`. The same applies to `-=`, `*=` and the
  other compound operators.
- Give a constant a name when the value alone does not show what it is.

```fern
// Old
if (c == 10 || c == 32) { … }

// New
if (c == b'\n' || c == b' ') { … }
```

## Inference

Write a type on a `let` only when the compiler needs it. Also write it when
the initializer does not make the type clear.

```fern
// Old
let count: i32 = 0;
let name: string = user.name;
let parts: string[] = line.split(",");

// New
let count = 0;
let name = user.name;
let parts = line.split(",");
```

Keep the annotation in these cases:

- The literal must have a width that is not the default: `let n: i64 = 0;`.
  A suffix is also correct: `let n = 0i64;`.
- The local must stay `i32` and a later use can widen it. An integer
  literal settles from all of its uses in the function, in any order: after
  `let b = 0;`, the shift `v >> b` with an `i64` value makes `b` an `i64`.
  A method call or an f-string on the local does not fix its width. The
  local is `i32` only when no use asks for another width.
- The local holds a member struct of a `type A | B` union, and the code uses
  it as the union: `let e: ast.Expr = ast.ExprIdent { ... };`. Without the
  annotation, the local has the struct type, and a later assignment of
  another member is an error. An `enum` variant does not need this: the
  local has the enum type.
- The initializer is an empty collection: `let out: string[] = [];`.
- The initializer is `None`, `Ok(…)` or `Err(…)` and the other half of the
  type is not clear.

Keep every type on a function signature. Lambda parameters also need their
types.

## Data and errors

### Return `Option` or `Result` in place of a sentinel

A function that returns `-1`, `""` or `false` to mean "no result" is the old
form. Return `Option[T]`. Return `Result[T, E]` when the caller must know why
the operation failed.

```fern
// Old
function index_of(xs: string[], x: string): i32 {
  for i in 0..xs.len() {
    if (xs[i] == x) {
      return i;
    }
  }
  return -1;
}

// New
function index_of(xs: string[], x: string): Option[i32] {
  for i in 0..xs.len() {
    if (xs[i] == x) {
      return Some(i);
    }
  }
  return None;
}
```

This rule changes an API. Change every caller in the same change.

### Use struct update to change one field

A struct is immutable after construction. To change a field, make a new value
from the old one.

```fern
sh = Show { ...sh, lines: true };
```

### Use the standard library

Use `std/array` (`map`, `filter`, `fold`, `any`, `find`, …) and `std/string`
in place of a loop that does the same work. Use `|>` to chain the calls.

```fern
let doubled = xs |> array.map((v: i32) => v * 2);
```

Write the loop when the body does more than one thing. Also write it when a
measurement shows that the loop is faster. A combinator that is slower than its loop is a
compiler bug. Report it.

## Limits in the compiler

`compiler/` follows these rules with four limits.

1. **Bootstrap.** The pinned stage0 compiles the compiler. A construct can
   appear in `compiler/` only when that stage0 compiles it
   (`docs/BOOTSTRAP.md`).
2. **The feature census.** `TestSelfHostFeatureCensus` pins the count of some
   constructs in `compiler/`, for example `?` and generic structs. A change
   that uses one of them moves the pin in the same commit, and the commit
   message says why.
3. **The lint ratchet.** `self_host_lint_ratchet_test.go` holds the complexity
   numbers for each tree. A change that moves them updates the table in the
   same commit.
4. **Gates.** The fixpoint cannot find a stable miscompile. A change to
   `compiler/` passes `internal/testing/e2ecompiler` first, then the fixpoint
   (`docs/TEST-GATES.md`).

`compiler/runtime/` holds the runtime kernels. A kernel keeps its shape when
the emitted-size or perf lane shows that the new form is slower. File the
slower form as a compiler issue.

## How to convert a file

1. Convert one file at a time. Keep the behaviour the same.
2. Run `bin/fern -fmt -w` on the file.
3. Run `bin/fern -check` on the file, or the test that covers it.
4. When the program runs on its own, compare its output before and after the
   change.
