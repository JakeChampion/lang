# Foundry: a deterministic factory

Each cell holds a machine, a cargo count, and a progress counter.
A two-phase simulation reads the previous grid before it commits the next grid.

```fern
<<*>>=
<<Kernel>>
<<Factory state>>
<<Entry>>
```

## Kernel

The same functions compile for native execution and browser WebAssembly.
The browser controls the display. Fern computes the result.

```fern
<<Kernel>>=
import "std/array";

@export("fern:showcase/foundry", "cell")
pub function cell(kind: i32, cargo: i32, incoming: i32, accepting: i32, tick: i32): i32 {
  if (kind < 1 || kind > 6) {
    return 0;
  }
  let amount = cargo.max(0).min(15);
  if (kind == 1 && tick % 8 == 0) {
    amount = (amount + 1).min(15);
  }
  if (kind == 6) {
    return 0;
  }
  if (accepting > 0 && amount > 0) {
    amount = amount - 1;
  }
  return (amount + incoming.max(0)).min(15);
}

@export("fern:showcase/foundry", "output")
pub function output(kind: i32, index: i32, width: i32, size: i32): i32 {
  if (width < 1 || index < 0 || index >= size) {
    return index;
  }
  if ((kind == 1 || kind == 2) && index % width < width - 1) {
    return index + 1;
  }
  if (kind == 3 && index + width < size) {
    return index + width;
  }
  if (kind == 4 && index % width > 0) {
    return index - 1;
  }
  if (kind == 5 && index >= width) {
    return index - width;
  }
  return index;
}
```

## Factory state

The native server owns the shared cargo state. A step reserves destination capacity before it moves cargo.
The two-phase update prevents duplicate movement and cargo loss when several belts feed one cell.
Layout edits remove cargo only from the edited cells. The removed counter retains that quantity for conservation checks.

```fern
<<Factory state>>=
pub const PERIOD: i64 = 100000000;

pub struct Factory { cargo: i32[], tick: i64, produced: i64, consumed: i64, removed: i64 }

pub function factory(size: i32): Factory {
  return Factory { cargo: __alloc_i32(size), tick: 0i64, produced: 0i64, consumed: 0i64, removed: 0i64 };
}

pub function step(layout: i32[], before: Factory, width: i32): Factory {
  let size = before.cargo.len();
  let incoming: i32[] = __alloc_i32(size);
  let outgoing: i32[] = __alloc_i32(size);
  let capacity: i32[] = __alloc_i32(size);
  let i = 0;
  while (i < size) {
    capacity = capacity.with(i, 15 - before.cargo[i]);
    i = i + 1;
  }
  i = 0;
  while (i < size) {
    let kind = layout[i];
    if (kind > 0 && kind != 6 && before.cargo[i] > 0) {
      let target = output(kind, i, width, size);
      if (target != i && layout[target] > 0 && capacity[target] > 0) {
        outgoing = outgoing.with(i, 1);
        incoming = incoming.with(target, incoming[target] + 1);
        capacity = capacity.with(target, capacity[target] - 1);
      }
    }
    i = i + 1;
  }
  let cargo = before.cargo;
  let produced = before.produced;
  let consumed = before.consumed;
  i = 0;
  while (i < size) {
    let kind = layout[i];
    if (kind == 6) {
      consumed = consumed + incoming[i];
    }
    if (kind == 1 && before.tick % 8 == 0 && before.cargo[i] < 15) {
      produced = produced + 1;
    }
    cargo = cargo.with(i, cell(kind, before.cargo[i], incoming[i], outgoing[i], (before.tick % 8) as i32));
    i = i + 1;
  }
  return Factory { ...before, cargo: cargo, tick: before.tick + 1, produced: produced, consumed: consumed };
}

pub function clear(factory: Factory, points: i32[]): Factory {
  let cargo = factory.cargo;
  let removed = factory.removed;
  for at in points {
    removed = removed + cargo[at];
    cargo = cargo.with(at, 0);
  }
  return Factory { ...factory, cargo: cargo, removed: removed };
}
```

## Entry

The browser calls the exports directly. The command entry returns without work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```
