# Foundry: shared factory transport, power, and refinement

The native server owns a 256 by 256 factory. A shared project contains its layout, cargo, power network, and counters.
The server advances the factory ten times per second while participants are connected. A paused factory has no simulation timer.

Mines produce ore. Powered smelters convert two ore units into one ingot after eight ticks.
Freight rails move cargo four cells on every fourth tick. Stations connect rails to the belt network.

```fern
<<*>>=
<<Implementation>>
<<Entry>>
```

## Read the program

The named sections form one Fern module. Native code and browser Wasm use the same numerical rules.

```fern
<<Implementation>>=
<<Grid coordinates>>
<<Factory values>>
<<Machine rules>>
<<Power network>>
<<Advance one tick>>
```

## Grid coordinates

A cell index determines its row and column. Output selection checks the grid boundary before it moves cargo.
Belts select one neighboring cell. Rails select a four-cell hop. The caller also checks the complete rail path.

```fern
<<Grid coordinates>>=
import "std/array";
import "std/json";

@export("fern:showcase/foundry", "output")
pub fip function output(kind: i32, index: i32, width: i32, size: i32): i32 {
  if (width < 1 || index < 0 || index >= size) {
    return index;
  }
  if (kind == 9 && index % width + 4 < width && index + 4 < size) {
    return index + 4;
  }
  if ((kind == 1 || kind == 2 || kind == 7 || kind == 11) && index % width < width - 1) {
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

## Factory values

Each cargo word packs an ore count, an ingot count, and smelter progress. A cell holds at most fifteen items.
Production, delivery, removal, and refinement use separate counters. An ingot represents two original ore units.
A layout edit removes cargo only from the changed cells and records its ore-equivalent quantity.

```fern
<<Factory values>>=
pub const PERIOD: i64 = 100000000;

@derive(json.Json, json.FromJson)
pub struct Factory { cargo: i32[], tick: i64, produced: i64, consumed: i64, removed: i64, refined: i64, powered: i32[] }

pub function factory(size: i32): Factory {
  return Factory { cargo: __alloc_i32(size), tick: 0i64, produced: 0i64, consumed: 0i64, removed: 0i64, refined: 0i64, powered: __alloc_i32(size) };
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

## Machine rules

The scalar helpers define conductivity, accepted materials, rail continuity, and a machine update.
The gate-sized transport helpers declare fip. The numerical Wasm tests check the remaining kernels for stable memory use.
The native simulation and the browser comparison call these same rules.

```fern
<<Machine rules>>=
@export("fern:showcase/foundry", "conducts")
pub fip function conducts(kind: i32): boolean {
  return kind == 7 || kind == 8 || kind == 10;
}

@export("fern:showcase/foundry", "units")
pub fip function units(state: i32): i32 {
  return (state & 15) + (state >> 4 & 15) * 2;
}

@export("fern:showcase/foundry", "material")
pub fip function material(kind: i32, state: i32): i32 {
  if ((state >> 4 & 15) > 0) {
    return 16;
  }
  if (kind != 7 && (state & 15) > 0) {
    return 1;
  }
  return 0;
}

@export("fern:showcase/foundry", "accepts")
pub fip function accepts(kind: i32, material: i32): boolean {
  if (kind == 7) {
    return material == 1;
  }
  return kind >= 2 && kind <= 6 || kind == 9 || kind == 11;
}

@export("fern:showcase/foundry", "rail-path")
pub fip function rail_path(a: i32, b: i32, c: i32, d: i32): boolean {
  return a == 9 && b == 9 && c == 9 && (d == 9 || d == 11);
}

@export("fern:showcase/foundry", "state")
pub function machine_state(kind: i32, state: i32, incoming: i32, outgoing: i32, tick: i32, powered: i32): i32 {
  if (kind == 0 || kind == 6 || kind == 8 || kind == 10) {
    return 0;
  }
  let ore = (state & 15) - (outgoing & 15) + (incoming & 15);
  let ingots = (state >> 4 & 15) - (outgoing >> 4 & 15) + (incoming >> 4 & 15);
  let progress = 0;
  if (kind == 1 && tick % 8 == 0 && ore < 15) {
    ore = ore + 1;
  }
  if (kind == 7 && powered > 0 && ore >= 2 && ingots + ore <= 15) {
    progress = (state >> 8 & 15) + 1;
    if (progress >= 8) {
      ore = ore - 2;
      ingots = ingots + 1;
      progress = 0;
    }
  }
  return ore.max(0).min(15) | ingots.max(0).min(15) << 4 | progress << 8;
}
```

## Power network

A breadth-first traversal starts at every generator. Cables and smelters conduct power through four neighbors.
The traversal marks a cell before it adds that cell to the queue. Each cell therefore enters the queue at most once.
Layout edits recompute this derived network. Recovery recomputes it from the saved layout.

```fern
<<Power network>>=
pub function power(layout: i32[], width: i32): i32[] {
  let powered: i32[] = __alloc_i32(layout.len());
  let queue: i32[] = __alloc_i32(layout.len());
  let head = 0;
  let tail = 0;
  let at = 0;
  for kind in layout {
    if (kind == 8) {
      powered = powered.with(at, 1);
      queue = queue.with(tail, at);
      tail = tail + 1;
    }
    at = at + 1;
  }
  while (head < tail) {
    let source = queue[head];
    head = head + 1;
    for direction in [0 - 1, 1, 0 - width, width] {
      let target = source + direction;
      if (target >= 0 && target < layout.len() && (direction != 1 && direction != 0 - 1 || source / width == target / width) && powered[target] == 0 && conducts(layout[target])) {
        powered = powered.with(target, 1);
        queue = queue.with(tail, target);
        tail = tail + 1;
      }
    }
  }
  return powered;
}

pub function configure(layout: i32[], factory: Factory, width: i32): Factory {
  return Factory { ...factory, powered: power(layout, width) };
}
```

## Advance one tick

The first pass reserves destination capacity. Deterministic index order resolves competing inputs.
The second pass applies all transfers to a new cargo generation. An item cannot move twice in one tick.
A test verifies production equals delivery, removal, and the ore-equivalent inventory.

```fern
<<Advance one tick>>=
pub function step(layout: i32[], before: Factory, width: i32): Factory {
  let size = before.cargo.len();
  let incoming: i32[] = __alloc_i32(size);
  let outgoing: i32[] = __alloc_i32(size);
  let capacity: i32[] = __alloc_i32(size);
  let i = 0;
  while (i < size) {
    capacity = capacity.with(i, 15 - (before.cargo[i] & 15) - (before.cargo[i] >> 4 & 15));
    i = i + 1;
  }
  i = 0;
  while (i < size) {
    let kind = layout[i];
    let item = material(kind, before.cargo[i]);
    if (item > 0 && kind != 6 && kind != 0 && (kind != 7 || before.powered[i] > 0)) {
      let target = output(kind, i, width, size);
      let connected = true;
      if (kind == 9) {
        connected = before.tick % 4 == 0 && i % width + 4 < width && i + 4 < size;
        if (connected) {
          connected = rail_path(layout[i + 1], layout[i + 2], layout[i + 3], layout[i + 4]);
        }
      }
      if (target != i && connected && accepts(layout[target], item) && capacity[target] > 0) {
        outgoing = outgoing.with(i, item);
        incoming = incoming.with(target, incoming[target] + item);
        capacity = capacity.with(target, capacity[target] - 1);
      }
    }
    i = i + 1;
  }
  let cargo = before.cargo;
  let produced = before.produced;
  let consumed = before.consumed;
  let refined = before.refined;
  i = 0;
  while (i < size) {
    let kind = layout[i];
    if (kind == 6) {
      consumed = consumed + units(incoming[i]) as i64;
    }
    if (kind == 1 && before.tick % 8 == 0 && (before.cargo[i] & 15) - (outgoing[i] & 15) < 15) {
      produced = produced + 1;
    }
    let next = machine_state(kind, before.cargo[i], incoming[i], outgoing[i], (before.tick % 8) as i32, before.powered[i]);
    if (kind == 7 && (next >> 4 & 15) > (before.cargo[i] >> 4 & 15) + (incoming[i] >> 4 & 15) - (outgoing[i] >> 4 & 15)) {
      refined = refined + 1;
    }
    cargo = cargo.with(i, next);
    i = i + 1;
  }
  return Factory { ...before, cargo: cargo, tick: before.tick + 1, produced: produced, consumed: consumed, refined: refined };
}
```

## Entry

The browser calls scalar exports directly. The command entry returns without work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

Run this example with `fern -doctest`. It uses the implementation above.

```fern test name=foundry-example
<<Implementation>>
function main(): i32 {
 let layout:i32[]=[1,2,2,6];let value=factory(4);
 for tick in 0..64 {value=step(layout,value,4);}
 let inventory=0i64;for cargo in value.cargo {inventory=inventory+units(cargo) as i64;}
 if(value.consumed==0 || value.produced!=value.consumed+inventory){return 1;}
 return 0;
}
```
