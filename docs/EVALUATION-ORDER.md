# Evaluation order

Status: [policy] observable evaluation order.

Fern evaluates an expression before it uses that expression's value.
An expression with an observable effect runs once at its specified position.
The following rules apply to both interpreters and every compiled target.

## Calls

A call evaluates its callee before its arguments.
A method call evaluates its receiver before its arguments.
An indirect call retains the function value that the callee expression produced.
The function body runs after all arguments have values.

Positional arguments evaluate from left to right.
Named arguments first bind to parameters, then evaluate in parameter declaration order.
Their written order does not determine their evaluation order.
For example, `f(b = second(), a = first())` evaluates `first()` before `second()` when `f` declares `a` before `b`.
This rule also applies when positional and named arguments appear together.

A parameter default must be a constant expression. Calls in defaults are rejected with E076.
An omitted argument uses its default at the call site.
Default arguments cannot introduce callback effects between explicit arguments.

## Operands and aggregates

- A binary operator evaluates its left operand before its right operand, except for the short-circuit rules below.
- An index evaluates its source before its index. A slice evaluates its source, start, and end in that order.
- An array or tuple literal evaluates its elements from left to right.
- A variant constructor evaluates its payload arguments from left to right.
- A record literal evaluates its field values in written order, regardless of field declaration order.
- A record update evaluates its base before its replacement field values. The replacement field values evaluate in written order.
- A map literal evaluates each key before its value. Entries evaluate in written order.

## Conditional evaluation

`a && b` evaluates `b` only when `a` is true.
`a || b` evaluates `b` only when `a` is false.
An `if` expression evaluates its condition before its selected arm. It does not evaluate the other arm.
A `match` evaluates its scrutinee once, then evaluates only the selected arm.

## Scope and optimization

These rules constrain observable effects, including callback traces and I/O.
They do not require an allocation for every source expression.
[ALLOCATION-OBSERVABLE.md](ALLOCATION-OBSERVABLE.md) defines the separate allocation observations.
[ARRAY-ALGEBRA.md](ARRAY-ALGEBRA.md) defines the permitted array fusion and reduction behavior.
Pure computations can move or disappear when no permitted observation changes.
