# Value lifetime hooks

This module controls value lifetimes.

## Model

`Drop` defines the finalizer hook for values that own resources. A type that implements this trait supplies a `drop` method with no result.

## Contracts

The hook belongs to the value lifetime. Resource owners must keep their release operation consistent with the compiler ownership rules.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub trait Drop>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module controls value lifetimes.
```

### `2 pub trait Drop`

```fern
<<2 pub trait Drop>>=
pub trait Drop {
  function drop(self: Self): void;
}
```
