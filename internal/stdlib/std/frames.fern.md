# Page frames

This module manages the page frames of a region of raw memory, 4096 bytes each. A kernel builds one over the RAM that its boot protocol reports, and then gives the Fern heap a run of frames with `__heap_adopt` (`docs/BARE-METAL-PLAN.md`, milestone 3). The allocator keeps all of its state in the region itself, so it allocates nothing. The `@norc` functions take only the base address. Thus a function that runs before the heap exists can allocate and free a frame, and so can the top half of an interrupt handler. The `Frames` methods wrap the same functions for ordinary code.

## Layout

The header is the first 64 bytes of the region: the frame count, the free count, and the index of the first bitmap word that can hold a free frame. The bitmap follows, with one bit for each frame. A set bit is a frame in use. The frames that hold the header and the bitmap are in use from the start. The bits after the last frame are set too, so a scan never finds them.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<constants>>

<<header and bitmap>>

<<alloc and free>>

<<runs>>

<<pub struct Frames>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module manages the page frames of a region of raw memory.
```

### `constants`

```fern
<<constants>>=
pub const FRAME_BYTES: usize = 4096;

const HEADER_BYTES: usize = 64;

const WORD_BYTES: usize = 8;

const BITS: usize = 64;

const ONE: usize = 1;

const ZERO: usize = 0;
```

### `header and bitmap`

`init` writes the header and the bitmap, and returns the base of the region, rounded up to a frame. It returns 0 when the region holds no frame after the header and the bitmap.

```fern
<<header and bitmap>>=
@norc
pub function init(base: usize, len: usize): usize {
  unsafe {
    let start = base + (FRAME_BYTES - base % FRAME_BYTES) % FRAME_BYTES;
    if (start >= base + len) {
      return ZERO;
    }
    let total = (base + len - start) / FRAME_BYTES;
    let words = (total + BITS - ONE) / BITS;
    let reserved = (HEADER_BYTES + words * WORD_BYTES + FRAME_BYTES - ONE) / FRAME_BYTES;
    if (reserved >= total) {
      return ZERO;
    }
    __store_i64(start, total as i64);
    __store_i64(start + WORD_BYTES, (total - reserved) as i64);
    __store_i64(start + WORD_BYTES * 2, 0i64);
    let w = ZERO;
    while (w < words) {
      __store_i64(start + HEADER_BYTES + w * WORD_BYTES, 0i64);
      w += ONE;
    }
    let i = ZERO;
    while (i < reserved) {
      set_bit(start, i);
      i += ONE;
    }
    i = total;
    while (i < words * BITS) {
      set_bit(start, i);
      i += ONE;
    }
    return start;
  }
}

// The frames of the region, the header and the bitmap included.
@norc
pub function count(base: usize): usize {
  unsafe {
    return __load_i64(base) as usize;
  }
}

// The frames that are free.
@norc
pub function free_count(base: usize): usize {
  unsafe {
    return __load_i64(base + WORD_BYTES) as usize;
  }
}

@norc
function word_of(base: usize, index: usize): usize {
  return base + HEADER_BYTES + (index / BITS) * WORD_BYTES;
}

@norc
function mask_of(index: usize): i64 {
  return 1i64 << (index % BITS) as i64;
}

@norc
function set_bit(base: usize, index: usize): void {
  unsafe {
    let at = word_of(base, index);
    __store_i64(at, __load_i64(at) | mask_of(index));
  }
}

@norc
function clear_bit(base: usize, index: usize): void {
  unsafe {
    let at = word_of(base, index);
    __store_i64(at, __load_i64(at) & (0i64 - 1i64 - mask_of(index)));
  }
}

@norc
function in_use(base: usize, index: usize): boolean {
  unsafe {
    return (__load_i64(word_of(base, index)) & mask_of(index)) != 0i64;
  }
}

@norc
function add_free(base: usize, delta: i64): void {
  unsafe {
    __store_i64(base + WORD_BYTES, __load_i64(base + WORD_BYTES) + delta);
  }
}
```

### `alloc and free`

`alloc` gives the lowest free frame at or after the hint word, or 0 when every frame is in use. `free` returns a frame. Neither checks its argument: a frame outside the region, or a frame freed twice, corrupts the bitmap. The methods of `Frames` check.

```fern
<<alloc and free>>=
@norc
pub function alloc(base: usize): usize {
  unsafe {
    let words = (count(base) + BITS - ONE) / BITS;
    let w = __load_i64(base + WORD_BYTES * 2) as usize;
    while (w < words) {
      let at = base + HEADER_BYTES + w * WORD_BYTES;
      let bits = __load_i64(at);
      if (bits != 0i64 - 1i64) {
        let bit = __ctz64((0i64 - 1i64 - bits) as u64) as usize;
        __store_i64(at, bits | (1i64 << bit as i64));
        __store_i64(base + WORD_BYTES * 2, w as i64);
        add_free(base, 0i64 - 1i64);
        return base + (w * BITS + bit) * FRAME_BYTES;
      }
      w += ONE;
    }
    return ZERO;
  }
}

@norc
pub function free(base: usize, frame: usize): void {
  unsafe {
    let index = (frame - base) / FRAME_BYTES;
    clear_bit(base, index);
    add_free(base, 1i64);
    if (index / BITS < __load_i64(base + WORD_BYTES * 2) as usize) {
      __store_i64(base + WORD_BYTES * 2, (index / BITS) as i64);
    }
  }
}
```

### `runs`

A run is `n` frames at consecutive addresses, for a heap region or a DMA ring. `alloc_run` scans the bitmap for the lowest run of free frames, so it is slower than `alloc`. It gives 0 when no such run exists.

```fern
<<runs>>=
@norc
pub function alloc_run(base: usize, n: usize): usize {
  if (n == ZERO) {
    return ZERO;
  }
  let total = count(base);
  let start = ZERO;
  let i = ZERO;
  while (i < total) {
    if (in_use(base, i)) {
      start = i + ONE;
    } else if (i + ONE - start == n) {
      let k = start;
      while (k <= i) {
        set_bit(base, k);
        k += ONE;
      }
      add_free(base, 0i64 - n as i64);
      return base + start * FRAME_BYTES;
    }
    i += ONE;
  }
  return ZERO;
}

@norc
pub function free_run(base: usize, frame: usize, n: usize): void {
  let k = ZERO;
  while (k < n) {
    free(base, frame + k * FRAME_BYTES);
    k += ONE;
  }
}
```

### `pub struct Frames`

`Frames` holds the base of a region that `init` prepared. Its methods refuse a frame outside the region, a frame that is not in use, and a frame address inside a frame.

```fern
<<pub struct Frames>>=
pub struct Frames { base: usize }

// The frames of the region at `base` of `len` bytes, or None when the region
// holds no frame after the header and the bitmap.
pub function new(base: usize, len: usize): Option[Frames] {
  let start = init(base, len);
  if (start == ZERO) {
    return None;
  }
  return Some(Frames { base: start });
}

pub function (f: Frames) count(): usize {
  return count(f.base);
}

pub function (f: Frames) free_count(): usize {
  return free_count(f.base);
}

// The end of the region: the address after its last frame.
pub function (f: Frames) end(): usize {
  return f.base + count(f.base) * FRAME_BYTES;
}

pub function (f: Frames) alloc(): Option[usize] {
  let frame = alloc(f.base);
  if (frame == ZERO) {
    return None;
  }
  return Some(frame);
}

pub function (f: Frames) alloc_run(n: usize): Option[usize] {
  let frame = alloc_run(f.base, n);
  if (frame == ZERO) {
    return None;
  }
  return Some(frame);
}

// Whether `frame` is the address of a frame of the region that is in use.
pub function (f: Frames) owns(frame: usize): boolean {
  if (frame < f.base || frame >= f.end() || frame % FRAME_BYTES != ZERO) {
    return false;
  }
  return in_use(f.base, (frame - f.base) / FRAME_BYTES);
}

// Returns a frame. False when the region does not own it.
pub function (f: Frames) free(frame: usize): boolean {
  if (!f.owns(frame)) {
    return false;
  }
  free(f.base, frame);
  return true;
}

// Returns the `n` frames of a run. False, and nothing freed, when the region
// does not own every one of them.
pub function (f: Frames) free_run(frame: usize, n: usize): boolean {
  let k = ZERO;
  while (k < n) {
    if (!f.owns(frame + k * FRAME_BYTES)) {
      return false;
    }
    k += ONE;
  }
  free_run(f.base, frame, n);
  return true;
}
```
