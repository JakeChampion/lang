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



**⟨*⟩≡**

```fern
function main(): i32 {
    ⟨the main loop⟩
    <<helper>>
    return 0;
}
```


The loop, in two pieces:


**⟨the main loop⟩≡**

```fern
let s: string = "hi \"there\"";
```
*⟨the main loop⟩ is used in ⟨*⟩.*




**⟨the main loop⟩+≡**

```fern
let n: i32 = 0x1F + 2.5; // This comment contains & and <tag>.
```
*⟨the main loop⟩ is used in ⟨*⟩.*




```fern
illustrative … not a chunk, with a "string" and 42
```



**⟨café⟩≡**

```fern
function helper() {}
```



**📄 `lib.fern`≡**

```fern
⟨shared⟩
```



**⟨shared⟩≡**

```fern
pub function s(): i32 { return 1; }
```


### Examples


```fern test name=adds
function main(): i32 { return 0; }
```



## Chunk index

- ⟨*⟩ — *(root)*
- ⟨café⟩ — *(unused)*
- ⟨shared⟩ — *(used by a file root)*
- ⟨the main loop⟩ — used in ⟨*⟩
