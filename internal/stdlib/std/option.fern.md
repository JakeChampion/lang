# Optional values

This module provides methods for `Option` values.

There is no `unwrap` or `expect`, here or on `Result`. A method that traps
on `None` turns the absence the type records back into a crash. Take the
value with `match`, `let Some(x) = o else { … }`, `?` or `unwrap_or`, and
an absence that cannot happen becomes an explicit `else` branch that says
what happens if it does.

```fern
<<*>>=
// This module provides methods for `Option` values.
//
// There is no `unwrap` or `expect`, here or on `Result`. A method that traps
// on `None` turns the absence the type records back into a crash. Take the
// value with `match`, `let Some(x) = o else { … }`, `?` or `unwrap_or`, and
// an absence that cannot happen becomes an explicit `else` branch that says
// what happens if it does.

import "core/cmp";

<<is_some>>

<<is_none>>

<<unwrap_or>>

<<unwrap_or_else>>

<<map>>

<<and_then>>

<<or_else>>

<<filter>>

<<ok_or>>

<<map_or>>

<<is_some_and>>

<<is_none_or>>

<<or>>

<<and>>

<<ok_or_else>>

<<map_or_else>>

<<zip>>

<<unzip>>

<<xor>>

<<flatten>>

<<transpose>>
```

## The presence of a value

`Some(value)` contains a value. `None` represents the absence of a value.
The predicate methods call their callback only for `Some`. For `None`, `is_some_and` returns false and `is_none_or` returns true.

```fern
<<is_some>>=
// is_some returns true for Some.
pub function (o: Option[T]) is_some(): boolean {
  match (o) {
    Some(x) => {
      return true;
    },
    None => {
      return false;
    }
  }
}
```

```fern
<<is_none>>=
// is_none returns true for None.
pub function (o: Option[T]) is_none(): boolean {
  match (o) {
    Some(x) => {
      return false;
    },
    None => {
      return true;
    }
  }
}
```

```fern
<<is_some_and>>=
// is_some_and tests the value, or returns false for None.
pub function (o: Option[T]) is_some_and(pred: (T) => boolean): boolean {
  match (o) {
    Some(x) => {
      return pred(x);
    },
    None => {
      return false;
    }
  }
}
```

```fern
<<is_none_or>>=
// is_none_or tests the value, or returns true for None.
pub function (o: Option[T]) is_none_or(pred: (T) => boolean): boolean {
  match (o) {
    Some(x) => {
      return pred(x);
    },
    None => {
      return true;
    }
  }
}
```


## Values and defaults

An argument expression executes before the method call. The callback forms defer the default calculation until the option is `None`.
An option that contains a value retains that value.

```fern
<<unwrap_or>>=
// unwrap_or returns the value, or the supplied default for None.
pub function (o: Option[T]) unwrap_or(fallback: T): T {
  match (o) {
    Some(x) => {
      return x;
    },
    None => {
      return fallback;
    }
  }
}
```

```fern
<<unwrap_or_else>>=
// unwrap_or_else calls the default callback only for None.
pub function (o: Option[T]) unwrap_or_else(f: () => T): T {
  match (o) {
    Some(x) => {
      return x;
    },
    None => {
      return f();
    }
  }
}
```

```fern
<<or>>=
// or returns this option when it contains a value, or the other option for None.
pub function (o: Option[T]) or(other: Option[T]): Option[T] {
  match (o) {
    Some(x) => {
      return Some(x);
    },
    None => {
      return other;
    }
  }
}
```

```fern
<<or_else>>=
// or_else calls the alternative callback only for None.
pub function (o: Option[T]) or_else(f: () => Option[T]): Option[T] {
  match (o) {
    Some(x) => {
      return Some(x);
    },
    None => {
      return f();
    }
  }
}
```


## Value transformations

`map` transforms the value inside `Some`. `map_or` and `map_or_else` return a plain value.
`filter` retains the value only when its predicate returns true. None of these methods calls the value callback for `None`.

```fern
<<map>>=
// map transforms the value inside Some and retains None.
pub function (o: Option[T]) map[U](f: (T) => U): Option[U] {
  match (o) {
    Some(x) => {
      return Some(f(x));
    },
    None => {
      return None;
    }
  }
}
```

```fern
<<map_or>>=
// map_or transforms the value, or returns the supplied default for None.
pub function (o: Option[T]) map_or[U](fallback: U, f: (T) => U): U {
  match (o) {
    Some(x) => {
      return f(x);
    },
    None => {
      return fallback;
    }
  }
}
```

```fern
<<map_or_else>>=
// map_or_else calls the value callback for Some and the default callback for None.
pub function (o: Option[T]) map_or_else[U](default_fn: () => U, f: (T) => U): U {
  match (o) {
    Some(x) => {
      return f(x);
    },
    None => {
      return default_fn();
    }
  }
}
```

```fern
<<filter>>=
// filter retains the value when the predicate returns true.
pub function (o: Option[T]) filter(pred: (T) => boolean): Option[T] {
  match (o) {
    Some(x) => {
      if (pred(x)) {
        return Some(x);
      }
      return None;
    },
    None => {
      return None;
    }
  }
}
```


## Combinations

`and` selects the other option when this option contains a value. `and_then` calculates that option from the value.
`zip` requires both values and returns a tuple. `xor` requires exactly one value.
`flatten` removes one option layer. `unzip` separates an optional tuple into two options.

```fern
<<and>>=
// and returns the other option for Some and None for None.
pub function (o: Option[T]) and[U](other: Option[U]): Option[U] {
  match (o) {
    Some(x) => {
      return other;
    },
    None => {
      return None;
    }
  }
}
```

```fern
<<and_then>>=
// and_then calls the next option callback only for Some.
pub function (o: Option[T]) and_then[U](f: (T) => Option[U]): Option[U] {
  match (o) {
    Some(x) => {
      return f(x);
    },
    None => {
      return None;
    }
  }
}
```

```fern
<<zip>>=
// zip returns a tuple when both options contain values.
pub function (o: Option[T]) zip[U](other: Option[U]): Option[(T, U)] {
  match (o) {
    Some(a) => {
      match (other) {
        Some(b) => {
          return Some((a, b));
        },
        None => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
}
```

```fern
<<unzip>>=
// unzip separates an optional tuple into two options.
pub function (o: Option[(T, U)]) unzip[T, U](): (Option[T], Option[U]) {
  match (o) {
    Some(p) => {
      return (Some(p.0), Some(p.1));
    },
    None => {
      return (None, None);
    }
  }
}
```

```fern
<<xor>>=
// xor retains a value when exactly one option contains a value.
pub function (o: Option[T]) xor(other: Option[T]): Option[T] {
  match (o) {
    Some(x) => {
      match (other) {
        Some(y) => {
          return None;
        },
        None => {
          return Some(x);
        }
      }
    },
    None => {
      return other;
    }
  }
}
```

```fern
<<flatten>>=
// flatten removes one option layer.
pub function (o: Option[Option[T]]) flatten(): Option[T] {
  match (o) {
    Some(inner) => {
      return inner;
    },
    None => {
      return None;
    }
  }
}
```


## Conversions to results

`ok_or` and `ok_or_else` convert absence into an error. The callback form calculates the error only for `None`.
`transpose` exchanges the outer `Option` and inner `Result`. An absent result becomes `Ok(None)`, and an inner error remains an error.

```fern
<<ok_or>>=
// ok_or converts Some to Ok and None to the supplied error.
pub function (o: Option[T]) ok_or[E](e: E): Result[T, E] {
  match (o) {
    Some(x) => {
      return Ok(x);
    },
    None => {
      return Err(e);
    }
  }
}
```

```fern
<<ok_or_else>>=
// ok_or_else calculates an error only for None.
pub function (o: Option[T]) ok_or_else[E](f: () => E): Result[T, E] {
  match (o) {
    Some(x) => {
      return Ok(x);
    },
    None => {
      return Err(f());
    }
  }
}
```

```fern
<<transpose>>=
// transpose exchanges Option and Result, with None as Ok(None).
pub function (o: Option[Result[T, E]]) transpose[T, E](): Result[Option[T], E] {
  match (o) {
    Some(r) => {
      match (r) {
        Ok(x) => {
          return Ok(Some(x));
        },
        Err(e) => {
          return Err(e);
        }
      }
    },
    None => {
      return Ok(None);
    }
  }
}
```

## Executable examples

These examples import this module through its public path. Each example returns zero when its checks pass.

```fern test name=option-transform-and-default
import "std/option";
function double(n: i32): i32 { return n * 2; }
function default_value(): i32 { return 7; }
function main(): i32 {
  let present: Option[i32] = Some(21);
  let absent: Option[i32] = None;
  if (present.map(double).unwrap_or(0) != 42) { return 1; }
  if (absent.unwrap_or_else(default_value) != 7) { return 2; }
  if (!present.is_some() || !absent.is_none()) { return 3; }
  return 0;
}
```

```fern test name=option-transpose
import "std/option";
function main(): i32 {
  let value: Option[Result[i32, string]] = Some(Ok(42));
  match (value.transpose()) {
    Ok(Some(n)) => { if (n != 42) { return 1; } },
    _ => { return 2; }
  }
  let absent: Option[Result[i32, string]] = None;
  match (absent.transpose()) {
    Ok(None()) => {},
    _ => { return 3; }
  }
  return 0;
}
```
