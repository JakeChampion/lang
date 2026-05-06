# lang

A small statically-typed language that compiles to ARM 32-bit assembly,
written in Go.

The language is a tiny TypeScript-flavoured subset: functions, `var`, `if` /
`while`, numbers, booleans, arrays, and the usual arithmetic / logical
operators. The compiler is end-to-end:

```
source ──► lexer ──► parser ──► type checker ──► ARM32 emitter ──► .s
```

## Origin

This project was inspired by Vladimir Keleshev's book *Compiling to Assembly
from Scratch* (https://keleshev.com/compiling-to-assembly-from-scratch). The
book teaches the same end-to-end pipeline against ARM32 using a TypeScript
subset. **No source from the book or its companion repo was copied or
translated.** The architecture here was designed independently in idiomatic
Go (recursive-descent parser, interface-typed AST, type switches for
visitors). Read the book if you want the theory; this repo is just one way
to assemble those ideas in Go.

## Build & run

```
go build ./cmd/lang
./lang examples/factorial.lang > factorial.s
arm-linux-gnueabihf-gcc -static factorial.s -o factorial
qemu-arm factorial
```

`go test ./...` runs the unit tests (lexer, parser, checker, codegen).

## Language at a glance

```
function factorial(n: number): number {
  if (n == 0) {
    return 1;
  }
  return n * factorial(n - 1);
}

function main(): number {
  return factorial(6);
}
```

Supported:

- Top-level `function` declarations, with parameter and return types.
- `var x: T = expr;` (type annotation optional — inferred from initialiser).
- `if / else`, `while`, `return`, blocks, expression statements.
- Number (32-bit signed), boolean, `void`, array (`number[]`).
- Operators: `+ - * /`, `== != < > <= >=`, `&& ||`, unary `- !`.
- Calls, indexing (`a[i]`), array literals (`[1, 2, 3]`).
- A built-in `putchar(n: number): void` for hello-world style output.

## Calling convention

Standard AAPCS, with one simplification: every expression leaves its result
in `r0`. Binary operators push `r0`, evaluate the right operand into `r0`,
then pop the left back into `r1`. Locals live on the stack at `fp - offset`.
