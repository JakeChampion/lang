---
title: std/samples
description: "Audio samples of 16 bits."
---

# `std/samples`

Audio samples of 16 bits.

## `const PEAK`

```fern
pub const PEAK: i16
```

The largest sample.

## `struct Block`

```fern
pub struct Block { start: u16, samples: i16[] }
```

A block of samples and the position of its first sample.

## `sample`

```fern
pub function sample(b: Block, at: u16): i16
```

The sample at `at`, or 0 past the end.

## `levels`

```fern
pub function levels(xs: [i16]): u16[]
```

The peaks of `xs` as unsigned levels.

