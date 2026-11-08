# Raw memory

A kernel and a driver read and write memory that no value of the program owns: a page table, the ring of a device, the image of a program, a register of a device. The reference counts do not see this memory. The heap builtins of an `unsafe` block, `__load_i64` and `__store_i64`, are for a block that the program took from `__alloc`. This module is the surface for the memory that the kernel owns: an address, and a load and a store at each width (`docs/BARE-METAL-PLAN.md`, the raw-pointer item).

An address is a `usize`. A load reads one value of its width at the address, and a store writes one. The compiler does not remove, merge or move an access. A driver can rely on the order of its accesses. Align the address to the width.

Only a kernel grants the `privileged` capability that an access needs. A hosted program that calls this module fails with E066. A dependency needs the `privileged` package capability in its `fern.toml` entry.

Every function is `@norc`: it allocates nothing, so a `@norc` function and an interrupt handler can call it.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<offset>>

<<loads>>

<<stores>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a kernel loads and stores at a raw address.
```

### `offset`

```fern
<<offset>>=
// The address `bytes` after `a`.
@norc
pub function offset(a: usize, bytes: usize): usize {
  return a + bytes;
}
```

### `loads`

A load of 8, 16 or 32 bits gives the unsigned value of that width. Fern has no 16-bit type. `load_u16` gives the value in a `u32`, and `store_u16` writes the low 16 bits of its `u32`. `load_usize` reads the word that holds an address.

```fern
<<loads>>=
@norc
pub function load_u8(a: usize): u8 {
  unsafe {
    return __mem_load8(a) as u8;
  }
}

@norc
pub function load_u16(a: usize): u32 {
  unsafe {
    return __mem_load16(a) as u32;
  }
}

@norc
pub function load_u32(a: usize): u32 {
  unsafe {
    return __mem_load32(a) as u32;
  }
}

@norc
pub function load_u64(a: usize): u64 {
  unsafe {
    return __mem_load64(a) as u64;
  }
}

@norc
pub function load_usize(a: usize): usize {
  unsafe {
    return __mem_load64(a) as usize;
  }
}
```

### `stores`

```fern
<<stores>>=
@norc
pub function store_u8(a: usize, v: u8): void {
  unsafe {
    __mem_store8(a, v as i32);
  }
}

@norc
pub function store_u16(a: usize, v: u32): void {
  unsafe {
    __mem_store16(a, v as i32);
  }
}

@norc
pub function store_u32(a: usize, v: u32): void {
  unsafe {
    __mem_store32(a, v as i32);
  }
}

@norc
pub function store_u64(a: usize, v: u64): void {
  unsafe {
    __mem_store64(a, v as i64);
  }
}

@norc
pub function store_usize(a: usize, v: usize): void {
  unsafe {
    __mem_store64(a, v as i64);
  }
}
```
