# Circuit: gates, feedback, and a four-bit processor

The editor shares its wiring over WebSocket. Each browser runs its own propagation simulation in a worker.
A generation reads the complete previous signal array, then swaps the two buffers.

The processor is a separate fixed design. Its ALU composes the same gate functions as the editor.
Participants share the sixteen-word program. Each browser can run or step it independently.

```fern
<<*>>=
<<Implementation>>
<<Entry>>
```

## Read the program

The named sections form one Fern module. Native code and browser Wasm use the same numerical rules.

```fern
<<Implementation>>=
<<Logic gates>>
<<A ripple-carry ALU>>
<<Execute an instruction>>
```

## Logic gates

Inputs use one bit. The D-latch reads A when B is high and retains its previous value otherwise.
The other gates implement their truth tables directly. The fip annotation requires an allocation-free body.

```fern
<<Logic gates>>=
@export("fern:showcase/circuit", "gate")
pub fip function gate(kind: i32, a: i32, b: i32, previous: i32): i32 {
  let x = a & 1;
  let y = b & 1;
  match (kind) {
    0 => {
      if (y == 1) {
        return x;
      }
      return previous & 1;
    },
    1 => {
      return x;
    },
    2 => {
      return 1 - x;
    },
    3 => {
      return x & y;
    },
    4 => {
      return x | y;
    },
    5 => {
      return x ^ y;
    },
    6 => {
      return 1 - (x & y);
    },
    7 => {
      return 1 - (x | y);
    },
    8 => {
      return 0;
    },
    9 => {
      return 1;
    },
    _ => {
      return 0;
    }
  }
}
```

## A ripple-carry ALU

Four full adders connect through their carry bits. XOR, AND, and OR implement each full adder.
The same function supports bitwise XOR and AND. Only addition returns a carry flag.

```fern
<<A ripple-carry ALU>>=
@export("fern:showcase/circuit", "alu")
pub function alu(operation: i32, a: i32, b: i32): i32 {
  let result = 0;
  let carry = 0;
  for bit in 0..4 {
    let x = a >> bit & 1;
    let y = b >> bit & 1;
    let xor = gate(5, x, y, 0);
    let value = gate(5, xor, carry, 0);
    let next_carry = gate(4, gate(3, x, y, 0), gate(3, xor, carry, 0), 0);
    if (operation == 3) {
      value = xor;
    }
    if (operation == 4) {
      value = gate(3, x, y, 0);
    }
    result = result | value << bit;
    carry = next_carry;
  }
  if (operation != 2) {
    carry = 0;
  }
  return result | carry << 4;
}
```

## Execute an instruction

The processor has a four-bit accumulator and a sixteen-word program. Each instruction contains an operation and operand.
LOAD, ADD, XOR, AND, conditional jump, jump, output, and halt update explicit state.
The result packs the accumulator, next program counter, halt flag, output flag, and carry flag.

```fern
<<Execute an instruction>>=
@export("fern:showcase/circuit", "cpu")
pub function cpu(instruction: i32, accumulator: i32, pc: i32): i32 {
  let operation = instruction >> 4 & 15;
  let operand = instruction & 15;
  let value = accumulator & 15;
  let next = pc + 1 & 15;
  let flags = 0;
  if (operation == 1) {
    value = operand;
  } else if (operation == 2 || operation == 3 || operation == 4) {
    let output = alu(operation, value, operand);
    value = output & 15;
    flags = (output >> 4 & 1) << 10;
  } else if (operation == 5) {
    if (value == 0) {
      next = operand;
    }
  } else if (operation == 6) {
    next = operand;
  } else if (operation == 7) {
    flags = flags | 512;
  } else if (operation == 8) {
    next = pc & 15;
    flags = flags | 256;
  }
  return value | next << 4 | flags;
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

```fern test name=circuit-example
<<Implementation>>
function main(): i32 {
 if(gate(3,1,0,0)!=0 || gate(0,1,1,0)!=1 || alu(2,15,1)!=16){return 1;}
 let state=cpu(17,0,0);state=cpu(34,state&15,state>>4&15);
 if((state&15)!=3){return 2;}
 return 0;
}
```
