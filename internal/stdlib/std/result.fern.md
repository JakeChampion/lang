# Values and errors

This module provides methods for `Result` values.
There is no `unwrap` or `expect`; std/option's header says why.

```fern
<<*>>=
// This module provides methods for `Result` values.
// There is no `unwrap` or `expect`; std/option's header says why.

import "core/cmp";

<<is_ok>>

<<is_err>>

<<unwrap_or>>

<<unwrap_or_else>>

<<map>>

<<and_then>>

<<map_err>>

<<ok>>

<<err>>

<<map_or>>

<<is_ok_and>>

<<is_err_and>>

<<or>>

<<and>>

<<or_else>>

<<map_or_else>>

<<flatten>>

<<transpose>>
```

## The result state

`Ok(value)` contains a successful value. `Err(error)` contains an error.
`is_ok_and` tests only a successful value. `is_err_and` tests only an error.

```fern
<<is_ok>>=
// is_ok returns true for Ok.
pub function (r: Result[T, E]) is_ok(): boolean {
  match (r) {
    Ok(x) => {
      return true;
    },
    Err(e) => {
      return false;
    }
  }
}
```

```fern
<<is_err>>=
// is_err returns true for Err.
pub function (r: Result[T, E]) is_err(): boolean {
  match (r) {
    Ok(x) => {
      return false;
    },
    Err(e) => {
      return true;
    }
  }
}
```

```fern
<<is_ok_and>>=
// is_ok_and tests the successful value, or returns false for Err.
pub function (r: Result[T, E]) is_ok_and(pred: (T) => boolean): boolean {
  match (r) {
    Ok(x) => {
      return pred(x);
    },
    Err(e) => {
      return false;
    }
  }
}
```

```fern
<<is_err_and>>=
// is_err_and tests the error, or returns false for Ok.
pub function (r: Result[T, E]) is_err_and(pred: (E) => boolean): boolean {
  match (r) {
    Ok(x) => {
      return false;
    },
    Err(e) => {
      return pred(e);
    }
  }
}
```


## Values and defaults

An argument expression executes before the method call. A default callback executes only for `Err` and receives the error.
`or` selects another result after an error. `or_else` can also change the error type.

```fern
<<unwrap_or>>=
// unwrap_or returns the successful value, or the supplied default for Err.
pub function (r: Result[T, E]) unwrap_or(fallback: T): T {
  match (r) {
    Ok(x) => {
      return x;
    },
    Err(e) => {
      return fallback;
    }
  }
}
```

```fern
<<unwrap_or_else>>=
// unwrap_or_else calculates a default from the error only for Err.
pub function (r: Result[T, E]) unwrap_or_else(f: (E) => T): T {
  match (r) {
    Ok(x) => {
      return x;
    },
    Err(e) => {
      return f(e);
    }
  }
}
```

```fern
<<or>>=
// or retains this result for Ok and selects the other result for Err.
pub function (r: Result[T, E]) or(other: Result[T, E]): Result[T, E] {
  match (r) {
    Ok(x) => {
      return Ok(x);
    },
    Err(e) => {
      return other;
    }
  }
}
```

```fern
<<or_else>>=
// or_else calls the alternative callback only for Err.
pub function (r: Result[T, E]) or_else[F](f: (E) => Result[T, F]): Result[T, F] {
  match (r) {
    Ok(x) => {
      return Ok(x);
    },
    Err(e) => {
      return f(e);
    }
  }
}
```


## Value and error transformations

`map` changes a successful value and retains an error. `map_err` changes an error and retains a successful value.
`map_or` and `map_or_else` produce a plain value. The callback form can use the error to calculate its default.

```fern
<<map>>=
// map transforms the value inside Ok and retains Err.
pub function (r: Result[T, E]) map[U](f: (T) => U): Result[U, E] {
  match (r) {
    Ok(x) => {
      return Ok(f(x));
    },
    Err(e) => {
      return Err(e);
    }
  }
}
```

```fern
<<map_err>>=
// map_err transforms the error inside Err and retains Ok.
pub function (r: Result[T, E]) map_err[F](f: (E) => F): Result[T, F] {
  match (r) {
    Ok(x) => {
      return Ok(x);
    },
    Err(e) => {
      return Err(f(e));
    }
  }
}
```

```fern
<<map_or>>=
// map_or transforms the successful value, or returns the supplied default for Err.
pub function (r: Result[T, E]) map_or[U](fallback: U, f: (T) => U): U {
  match (r) {
    Ok(x) => {
      return f(x);
    },
    Err(e) => {
      return fallback;
    }
  }
}
```

```fern
<<map_or_else>>=
// map_or_else selects the value callback for Ok and the error callback for Err.
pub function (r: Result[T, E]) map_or_else[U](default_fn: (E) => U, f: (T) => U): U {
  match (r) {
    Ok(x) => {
      return f(x);
    },
    Err(e) => {
      return default_fn(e);
    }
  }
}
```


## Result sequences

`and` selects the other result after success. `and_then` calculates the next result from the successful value.
Both methods retain an error without further calls. `flatten` removes one result layer while it retains either error.

```fern
<<and>>=
// and selects the other result for Ok and retains this error for Err.
pub function (r: Result[T, E]) and[U](other: Result[U, E]): Result[U, E] {
  match (r) {
    Ok(x) => {
      return other;
    },
    Err(e) => {
      return Err(e);
    }
  }
}
```

```fern
<<and_then>>=
// and_then calls the next result callback only for Ok.
pub function (r: Result[T, E]) and_then[U](f: (T) => Result[U, E]): Result[U, E] {
  match (r) {
    Ok(x) => {
      return f(x);
    },
    Err(e) => {
      return Err(e);
    }
  }
}
```

```fern
<<flatten>>=
// flatten removes one result layer and retains either error.
pub function (r: Result[Result[T, E], E]) flatten(): Result[T, E] {
  match (r) {
    Ok(inner) => {
      return inner;
    },
    Err(e) => {
      return Err(e);
    }
  }
}
```


## Conversions to options

`ok` retains only the successful value. `err` retains only the error.
`transpose` exchanges an outer `Result` and an inner `Option`. `Ok(None)` becomes `None`, and an error becomes `Some(Err(error))`.

```fern
<<ok>>=
// ok returns Some(value) for Ok and None for Err.
pub function (r: Result[T, E]) ok(): Option[T] {
  match (r) {
    Ok(x) => {
      return Some(x);
    },
    Err(e) => {
      return None;
    }
  }
}
```

```fern
<<err>>=
// err returns Some(error) for Err and None for Ok.
pub function (r: Result[T, E]) err(): Option[E] {
  match (r) {
    Ok(x) => {
      return None;
    },
    Err(e) => {
      return Some(e);
    }
  }
}
```

```fern
<<transpose>>=
// transpose exchanges Result and Option, with Ok(None) as None.
pub function (r: Result[Option[T], E]) transpose[T, E](): Option[Result[T, E]] {
  match (r) {
    Ok(inner) => {
      match (inner) {
        Some(x) => {
          return Some(Ok(x));
        },
        None => {
          return None;
        }
      }
    },
    Err(e) => {
      return Some(Err(e));
    }
  }
}
```

## Executable examples

These examples import this module through its public path. Each example returns zero when its checks pass.

```fern test name=result-transform-and-error
import "std/result";
function double(n: i32): i32 { return n * 2; }
function recover(code: i32): i32 { return code + 1; }
function main(): i32 {
  let success: Result[i32, i32] = Ok(21);
  let failure: Result[i32, i32] = Err(6);
  if (success.map(double).unwrap_or(0) != 42) { return 1; }
  if (failure.unwrap_or_else(recover) != 7) { return 2; }
  if (!success.is_ok() || !failure.is_err()) { return 3; }
  return 0;
}
```

```fern test name=result-transpose
import "std/result";
function main(): i32 {
  let value: Result[Option[i32], string] = Ok(Some(42));
  match (value.transpose()) {
    Some(Ok(n)) => { if (n != 42) { return 1; } },
    _ => { return 2; }
  }
  let absent: Result[Option[i32], string] = Ok(None);
  match (absent.transpose()) {
    None => {},
    _ => { return 3; }
  }
  return 0;
}
```
