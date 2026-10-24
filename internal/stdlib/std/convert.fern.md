# Value conversion traits

This module defines value conversion traits.

## Model

`From` and `Into` describe conversions without a separate error result. `TryFrom` and `TryInto` retain a concrete error type in `Result`.

## Contracts

Fallible conversion bounds name both the input and error types. Each implementation supplies the conversion rule and preserves its own error data.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub trait From[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines value conversion traits.
```

### `2 pub trait From[T]`

```fern
<<2 pub trait From[T]>>=
pub trait From[T] {
  function from(v: T): Self;
}

pub trait Into[T] {
  function into(self: Self): T;
}

// TryFrom and TryInto supply conversions that can fail.
// E is the concrete error type. It implements error.Error and cmp.Display.
// Each implementation preserves its error data in Result.
// A generic bound specifies both types: convert.TryFrom[string, ParseError].
pub trait TryFrom[T, E] {
  function try_from(v: T): Result[Self, E];
}

pub trait TryInto[T, E] {
  function try_into(self: Self): Result[T, E];
}
```
