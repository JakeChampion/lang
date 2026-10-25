# Circuit: discrete logic simulation

Gates read one complete input generation. Each step commits a new generation.
Feedback circuits therefore have explicit propagation delays.

```fern
<<*>>=
<<Kernel>>
<<Entry>>
```

## Kernel

The same functions compile for native execution and browser WebAssembly.
The browser controls the display. Fern computes the result.

```fern
<<Kernel>>=
@export("fern:showcase/circuit", "gate")
pub function gate(kind: i32, a: i32, b: i32, previous: i32): i32 {
  let x = a & 1;
  let y = b & 1;
  match (kind) {
    0 => {
      return previous & 1;
    },
    1 => {
      return x;
    },
    2 => {
      return 1 - x;
    },
    3 => {
      return x & y;
    },
    4 => {
      return x | y;
    },
    5 => {
      return x ^ y;
    },
    6 => {
      return 1 - (x & y);
    },
    7 => {
      return 1 - (x | y);
    },
    8 => {
      return 0;
    },
    9 => {
      return 1;
    },
    _ => {
      return 0;
    }
  }
}
```

## Entry

The browser calls the exports directly. The command entry returns without work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```
