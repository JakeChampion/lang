# Boolean environment flags

This module reads boolean environment flags. Empty values, `0`, `false`, and `off` disable a flag without regard to ASCII case.

## Model

`value_on` interprets a supplied string. `on` and `value` read a process-environment entry through the same policy.

## Contracts

Empty values, `0`, `false`, and `off` disable a flag without regard to ASCII case. Other nonempty values enable it.

## Executable example

```fern test name=std-envflag-example
import "std/envflag";

function main(): i32 {
  if (envflag.value_on("OFF") || envflag.value_on("0")) { return 1; }
  if (!envflag.value_on("yes")) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function value_on(value: string): boolean>>

<<3 function __folds_to(s: string, w: string): boolean>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads boolean environment flags. Empty values, `0`, `false`, and `off` disable a flag without regard to ASCII case.
```

### `2 pub function value_on(value: string): boolean`

```fern
<<2 pub function value_on(value: string): boolean>>=
pub function value_on(value: string): boolean {
  return !(value == "" || value == "0" || __folds_to(value, "false") || __folds_to(value, "off"));
}

pub function on(name: string): boolean {
  match (env(name)) {
    Some(v) => {
      return value_on(v);
    },
    None => {
      return false;
    }
  }
}

pub function value(name: string): Option[string] {
  match (env(name)) {
    Some(v) => {
      if (v != "") {
        return Some(v);
      }
    },
    None => {}
  }
  return None;
}
```

### `3 function __folds_to(s: string, w: string): boolean`

```fern
<<3 function __folds_to(s: string, w: string): boolean>>=
function __folds_to(s: string, w: string): boolean {
  if (s.len() != w.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < s.len()) {
    let c: u8 = s[i];
    if (c >= b'A' && c <= b'Z') {
      c = c + 32u8;
    }
    if (c != w[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```
