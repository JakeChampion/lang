# A woven program

Intro with **bold**, *italic*, `code`, a ```` ```fern file=x ```` span, a
[link](https://example.com), a [mail](mailto:me@x.com), a [relative](./rel/path),
a [fragment](#frag) and a [script](javascript:alert(1)) that must not link.

## The main loop

> A quote
> over two lines.

- one item
- two & <three>

1. first
2.  second

---

```sh
# not a heading: a shell comment in a fenced block
fern -weave doc.fern.md
```

```fern
<<*>>=
function main(): i32 {
    <<the main loop>>
    \<<helper>>
    return 0;
}
```

The loop, in two pieces:

```fern
<<the main loop>>=
let s: string = "hi \"there\"";
```

```fern
<<the main loop>>=
let n: i32 = 0x1F + 2.5; // a comment & <tag>
```

```fern
illustrative … not a chunk, with a "string" and 42
```

```fern
<<café>>=
function helper() {}
```

```fern file=lib.fern
<<shared>>
```

```fern
<<shared>>=
pub function s(): i32 { return 1; }
```

### Examples

```fern test name=adds
function main(): i32 { return 0; }
```
