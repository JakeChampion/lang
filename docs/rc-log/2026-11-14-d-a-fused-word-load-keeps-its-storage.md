# 2026-11-14 — a fused word load keeps its storage

All targets. `ssasem.borrow_parents`, `ssasem.address_base`. Fixes #15952.

## The shape

```fern
function u32of(): u32 {
    let b = rand.bytes(4);
    return b[0] as u32 << 24 as u32
        | b[1] as u32 << 16 as u32
        | b[2] as u32 << 8 as u32
        | b[3] as u32;
}
```

`ssabounds.word_loads` replaces the four byte reads with one load. For an
array it writes `__raw_arr_ptr(b)`, two additions and `__load_i32`. For a
string it writes `__raw_data(s)`.

The lowered function released `b` before the load. Under `-harden` a
released block is zero, so the function returned 0 on each call.
`rand.between` did not return, because its rejection loop read 0 each time.
Without `-harden` the block kept its old bytes, so no test saw the defect.

## The cause

The address is an integer. The analysis gave a dependency only to a
projection whose type needs an anchor, so the address had no link to the
array. The last use of the array was the call that hands out the address.

## What changed

`borrow_parents` now gives a parent to each raw address. The result of
`__raw_arr_ptr` or `__raw_data` has its argument as its parent. The sum of
an address and an offset has that address as its parent. A load through
the address is then a use of the storage, and the release comes after it.

The rule is not only for the fused load. A function that calls
`__raw_arr_ptr` in an `unsafe` block gets the same link.

An address that goes through a phi has no parent. The rule does not
protect a loop that steps an address.

## The gate

`TestSelfHostWordLoadKeepsItsStorage` builds five shapes with `-harden` for
x86-64-linux and arm64-linux. The shapes are a big-endian read of 4 bytes, a
little-endian read of 4 bytes and a read of 8 bytes. A read at a variable
index and a read from a string complete the set. Each shape returned 0 before the change.
