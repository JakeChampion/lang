# A program in two modules

This literate document tangles to **two** Fern modules rather than one.
A ```` ```fern file=PATH ```` block is a *file-root*: its body becomes
the file `PATH`, and the generated modules `import` each other exactly
like hand-written ones. Run it directly:

```sh
fern -interp examples/literate/multi_module.fern.md
```

or extract the modules with `fern -tangle …` (each is printed under a
`// ==> path <==` banner), or compile with `fern -o prog …`.

## The entry module

`main.fern` is marked `entry`, so it's the module the compiler starts
from. It depends on a small math module:

```fern file=main.fern entry
import "core/no_prelude";
import "./mathx";

function main(): i32 {
    // 3² + 4² = 25
    return mathx.sum_of_squares(3, 4);
}
```

## The math module

`mathx.fern` lives in its own file-root. Its body is assembled from
named chunks, so the prose can introduce the pieces one at a time —
the chunk references resolve no matter where they're defined:

```fern file=mathx.fern
<<square>>
<<sum of squares>>
```

Squaring is the primitive everything else builds on:

```fern
<<square>>=
pub function square(n: i32): i32 {
    return n * n;
}
```

And the public entry point the program calls — note it uses `square`,
defined just above, but tangling would resolve it from anywhere in the
document:

```fern
<<sum of squares>>=
pub function sum_of_squares(a: i32, b: i32): i32 {
    return square(a) + square(b);
}
```

Because each module gets its own provenance map, a type error in either
file is reported against the line you wrote *here*, in this document —
not against the generated intermediate source.
