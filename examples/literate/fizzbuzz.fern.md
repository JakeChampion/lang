# FizzBuzz, told as a story

This is a **literate** Fern program (`.fern.md`). The prose you're
reading is the program; the code lives in named *chunks* that are
woven into this narrative and reassembled — *tangled* — into a
compilable Fern program by `fern`.

Run it directly:

```sh
fern -interp examples/literate/fizzbuzz.fern.md
```

or extract the plain Fern source with `fern -tangle …`, or render a
cross-referenced reading copy with `fern -weave …`.

## The shape of the program

The root chunk `<<*>>` is what `fern` tangles. We describe the program
top-down — *what* it does — and fill in the *how* further down. The
references below are resolved no matter where in the document their
definitions appear:

```fern
<<*>>=
import "core/no_prelude";

<<the divisibility test>>

<<the main loop>>
```

`putchar` is a built-in and the divisibility check is pure
arithmetic, so the only thing we need is the `core/no_prelude`
opt-out declaring we won't pull anything from `std/`.

## Is a number divisible?

FizzBuzz turns on divisibility, so that's the first idea worth naming.
Fern has no `%` operator in this little subset, so we reconstruct the
remainder from integer division:

```fern
<<the divisibility test>>=
function divisible(n: i32, by: i32): boolean {
  return n - n / by * by == 0;
}
```

## The loop

Now the heart of it. We walk the numbers 1 through 15 and, for each,
decide what to print. We print `F` for multiples of three, `B` for
multiples of five, both for multiples of fifteen, and a `.` otherwise:

```fern
<<the main loop>>=
function main(): i32 {
  var i: i32 = 1;
  while (i <= 15) {
    <<classify one number>>
    putchar(10);  // newline
    i = i + 1;
  }
  return 0;
}
```

The classification is its own chunk so the loop above reads as prose.
Note it's referenced *inside* the `while` body, so tangling keeps its
indentation:

```fern
<<classify one number>>=
if (divisible(i, 15)) {
  putchar(70); putchar(66);   // FB
} else if (divisible(i, 3)) {
  putchar(70);                // F
} else if (divisible(i, 5)) {
  putchar(66);                // B
} else {
  putchar(46);                // .
}
```

That's the whole program. Because the carrier format is Markdown, this
file also renders as documentation on GitHub with no extra tooling —
`fern -weave` only adds the ⟨chunk⟩ labels and cross-references.
