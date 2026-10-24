# Process signal policy

This module configures process signals and reports caught signals.

## Model

Signal helpers install an ignore, default, or catch policy. Poll helpers report signals that the runtime has caught.

## Contracts

These operations change process state. The public constants use the runtime signal convention, and host-specific conversion stays behind the runtime interface.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub const SIGINT: i32>>

<<3 pub function ignore(sig: i32): i32>>

<<4 pub const DFL: i32>>

<<5 pub function disposition(sig: i32): i32>>

<<6 const HOW_BLOCK: i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module configures process signals and reports caught signals.
```

### `2 pub const SIGINT: i32`

```fern
<<2 pub const SIGINT: i32>>=
pub const SIGINT: i32 = 2;

pub const SIGPIPE: i32 = 13;

pub const SIGHUP: i32 = 1;

pub const SIGQUIT: i32 = 3;

pub const SIGTERM: i32 = 15;
```

### `3 pub function ignore(sig: i32): i32`

```fern
<<3 pub function ignore(sig: i32): i32>>=
pub function ignore(sig: i32): i32 {
  return signal_ignore(sig);
}

pub function default(sig: i32): i32 {
  return signal_default(sig);
}

pub function catch(sig: i32): i32 {
  return signal_catch(sig);
}

pub function catch_interrupting(sig: i32): i32 {
  return signal_catch_interrupting(sig);
}

pub function raise(sig: i32): i32 {
  return signal_raise(sig);
}

pub function taken(sig: i32): boolean {
  return signal_taken(sig);
}

pub function blocked(): i64 {
  return signal_mask(HOW_BLOCK, 0 as i64);
}

pub function block(mask: i64): i64 {
  return signal_mask(HOW_BLOCK, mask);
}

pub function unblock(mask: i64): i64 {
  return signal_mask(HOW_UNBLOCK, mask);
}

pub function set_blocked(mask: i64): i64 {
  return signal_mask(HOW_SETMASK, mask);
}

pub function bit(sig: i32): i64 {
  return 1 as i64 << (sig - 1) as i64;
}
```

### `4 pub const DFL: i32`

```fern
<<4 pub const DFL: i32>>=
pub const DFL: i32 = 0;

pub const IGN: i32 = 1;

pub const HANDLER: i32 = 2;
```

### `5 pub function disposition(sig: i32): i32`

```fern
<<5 pub function disposition(sig: i32): i32>>=
pub function disposition(sig: i32): i32 {
  return signal_disposition(sig);
}
```

### `6 const HOW_BLOCK: i32`

```fern
<<6 const HOW_BLOCK: i32>>=
const HOW_BLOCK: i32 = 0;

const HOW_UNBLOCK: i32 = 1;

const HOW_SETMASK: i32 = 2;
```
