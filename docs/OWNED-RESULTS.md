# Checked owned results

Status: [reference]

An `own` result promises an owned value to the caller.
The checker validates each return.
The promise is part of a function type.

```fern
function pass[T](own value: T): own T {
  return value;
}

function apply[T](own value: T, step: (own T) => own T): own T {
  return step(value);
}
```

Annotate an owned-result lambda explicitly:

```fern
let step = (own value: string): own string => value;
```

An ordinary result does not carry this promise.
An owned-result function can satisfy an ordinary-result function type.
The reverse assignment fails, even when one implementation returns an owned input.
Add the annotation to check and expose that guarantee.
A parameter keeps its ownership mode during assignment.

A borrowed resource parameter cannot become an owned result.
An aggregate or local alias cannot hide a resource loan.
A fresh aggregate can contain copied data parts when those parts cannot hold a handle.
A reusable closure borrows its captures.
This rule also applies to an outer owned parameter.
The closure cannot return such a capture as an owned result.

The caller can copy immutable data results.
The checker validates their full type.
A cleanup type needs `@value` to retain this copy contract.
Cells and callbacks do not satisfy that immutable-data proof.

The result annotation does not change the ABI or add a copy.
The move checks still refuse repeated consumption.
WIT `own Resource` results retain their resource-handle contract.

Compiler-source adoption requires a published stage0 that accepts this syntax.
An updated checker built with the previous stage0 can validate programs that use it.

A return can transfer a fresh local at its last use.
It can also transfer an owned field.
A deferred cleanup cannot also consume the returned resource.
A cleanup of a separate field remains valid.
