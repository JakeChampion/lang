# Notes

A test module for `fern -doc -doc-source`. The chunks are in a different
order from the module, so a tangled line is not the line of this file.

```fern
<<*>>=
<<intro>>

<<Note>>

<<count>>
```

### `count`

```fern
<<count>>=
// The number of notes.
pub function count(ns: Note[]): i32 {
  return ns.len();
}
```

### `Note`

```fern
<<Note>>=
// A note with a title.
pub struct Note { title: string }
```

### `intro`

```fern
<<intro>>=
// This module keeps notes.
```
