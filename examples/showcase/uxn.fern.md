# Uxn: a little computer, implemented in Fern

[Uxn](https://wiki.xxiivv.com/site/uxn.html) is the virtual machine at the heart
of Hundred Rabbits' ecosystem. This independent implementation follows the
current Uxn 0K instruction set: 64 KiB of RAM, two circular 256-byte stacks,
256 device ports, big-endian shorts, and all eight combinations of opcode modes.
The same source builds natively or as WebAssembly. There is no JavaScript CPU.

The showcase includes a bounded Uxntal assembler and a browser device host.
Supported Varvara devices are System stack indexes and palette, Console,
Screen pixels and sprites, Controller, Mouse, and Datetime. Audio, files,
subprocesses, and expansion banks are not implemented; the inspector reports
attempted use. This is a Uxn workbench, not a complete Varvara emulator.

```fern
<<*>>=
<<State layout>>
<<Screen device>>
<<Device writes>>
<<Execution>>
<<Assembler>>
<<Entry>>
```

## State layout

A serialized machine image makes ownership explicit across the Wasm boundary.
RAM occupies the first 65536 bytes, followed by the two stacks and device page.
The remaining bytes hold execution metadata, bounded console output, and a
512-by-512 packed two-layer framebuffer. The host only transports this image,
supplies input events, and presents the result.

```fern
<<State layout>>=
import "std/array";
import "std/string";
import "std/option";
import "core/map";

pub const WST: i32 = 65536;

pub const RST: i32 = 65792;

pub const DEV: i32 = 66048;

pub const META: i32 = 66304;

pub const CONSOLE: i32 = 66336;

pub const SCREEN: i32 = 74528;

pub const STATE_SIZE: i32 = 336672;

function word(s: u8[], at: i32): i32 {
  return s[at] as i32 << 8 | s[at + 1] as i32;
}

function putword(s: u8[], at: i32, value: i32): u8[] {
  return s.with(at, (value >> 8) as u8).with(at + 1, value as u8);
}

function signed(value: i32): i32 {
  if (value > 127) {
    return value - 256;
  }
  return value;
}

function peek(s: u8[], base: i32, ptr: i32, offset: i32, size: i32): i32 {
  let at = ptr - offset - size & 255;
  if (size == 1) {
    return s[base + at] as i32;
  }
  return s[base + at] as i32 << 8 | s[base + (at + 1 & 255)] as i32;
}

function push(s: u8[], base: i32, ptr: i32, value: i32, size: i32): u8[] {
  if (size == 2) {
    return s.with(base + (ptr & 255), (value >> 8) as u8).with(base + (ptr + 1 & 255), value as u8);
  }
  return s.with(base + (ptr & 255), value as u8);
}

@export("fern:showcase/uxn", "boot")
pub function boot(rom: u8[]): u8[] {
  if (rom.len() == 0 || rom.len() > 65280) {
    return [];
  }
  let s = __alloc_u8(STATE_SIZE);
  for i in 0..rom.len() {
    s = s.with(256 + i, rom[i]);
  }
  s = putword(s, META, 256).with(META + 4, 1);
  s = putword(s, META + 16, 256);
  s = putword(s, META + 18, 192);
  s = putword(s, DEV + 0x22, 256);
  s = putword(s, DEV + 0x24, 192);
  s = putword(s, DEV + 0x08, 0xf07f);
  s = putword(s, DEV + 0x0a, 0xf0d6);
  return putword(s, DEV + 0x0c, 0xf0b2);
}
```

## Screen device

Both layers use two bits per pixel. Foreground colour zero is transparent.
Pixels, quadrant fills, 1bpp/2bpp sprites, flipping, palette mapping, and automatic
position/address increments are handled in Fern. Resizes clear both layers and
clamp dimensions to 1–512 pixels; reads report the actual dimensions.

```fern
<<Screen device>>=
function pixel(s: u8[], x: i32, y: i32, layer: i32, color: i32): u8[] {
  if (x < 0 || y < 0 || x >= word(s, META + 16) || y >= word(s, META + 18)) {
    return s;
  }
  let at = SCREEN + y * 512 + x;
  let shift = layer * 2;
  return s.with(at, (s[at] as i32 & (15 ^ 3 << shift) | (color & 3) << shift) as u8);
}

function draw_pixel(s: u8[], value: i32): u8[] {
  let x = word(s, DEV + 0x28);
  let y = word(s, DEV + 0x2a);
  let layer = value >> 6 & 1;
  if ((value & 128) != 0) {
    let width = word(s, META + 16);
    let height = word(s, META + 18);
    let sx = x;
    let ex = width;
    let sy = y;
    let ey = height;
    if ((value & 16) != 0) {
      sx = 0;
      ex = x;
    }
    if ((value & 32) != 0) {
      sy = 0;
      ey = y;
    }
    // Screen coordinates are signed. A negative edge clips outside the display.
    if (x >= 32768) {
      if ((value & 16) != 0) {
        ex = 0;
      } else {
        sx = 0;
      }
    }
    if (y >= 32768) {
      if ((value & 32) != 0) {
        ey = 0;
      } else {
        sy = 0;
      }
    }
    for py in sy.max(0).min(height)..ey.max(0).min(height) {
      for px in sx.max(0).min(width)..ex.max(0).min(width) {
        s = pixel(s, px, py, layer, value & 3);
      }
    }
  } else {
    s = pixel(s, x, y, layer, value & 3);
    let auto = s[DEV + 0x26] as i32;
    if ((auto & 1) != 0) {
      s = putword(s, DEV + 0x28, x + 1 - (value >> 4 & 1) * 2);
    }
    if ((auto & 2) != 0) {
      s = putword(s, DEV + 0x2a, y + 1 - (value >> 5 & 1) * 2);
    }
  }
  return s;
}

function blend(color: i32, channel: i32): i32 {
  if (channel == 0) {
    return color >> 2;
  }
  if (channel == 1) {
    return color & 3;
  }
  if (channel == 2) {
    return (color & 3) % 3 + 1;
  }
  return ((color & 3) + 1) % 3 + 1;
}

function draw_sprite(s: u8[], value: i32): u8[] {
  let auto = s[DEV + 0x26] as i32;
  let x = word(s, DEV + 0x28);
  let y = word(s, DEV + 0x2a);
  if (x >= 32768) {
    x = x - 65536;
  }
  if (y >= 32768) {
    y = y - 65536;
  }
  let addr = word(s, DEV + 0x2c);
  let mode = value >> 7;
  let color = value & 15;
  let dx = 8;
  let dy = 8;
  if ((value & 16) != 0) {
    dx = 0 - 8;
  }
  if ((value & 32) != 0) {
    dy = 0 - 8;
  }
  for n in 0..(auto >> 4) + 1 {
    for row in 0..8 {
      let lo = s[addr + row & 65535] as i32;
      let hi: i32 = 0;
      if (mode != 0) {
        hi = s[addr + row + 8 & 65535] as i32;
      }
      for col in 0..8 {
        let channel = lo >> 7 - col & 1 | (hi >> 7 - col & 1) << 1;
        if (channel != 0 || color % 5 != 0) {
          let px = col;
          let py = row;
          if ((value & 16) != 0) {
            px = 7 - col;
          }
          if ((value & 32) != 0) {
            py = 7 - row;
          }
          s = pixel(s, x + px, y + py, value >> 6 & 1, blend(color, channel));
        }
      }
    }
    if ((auto & 4) != 0) {
      addr = addr + 8 + mode * 8 & 65535;
    }
    // Repeats move perpendicular to the final position increment.
    if ((auto & 2) != 0) {
      x = x + dx;
    }
    if ((auto & 1) != 0) {
      y = y + dy;
    }
  }
  if ((auto & 1) != 0) {
    s = putword(s, DEV + 0x28, word(s, DEV + 0x28) + dx);
  }
  if ((auto & 2) != 0) {
    s = putword(s, DEV + 0x2a, word(s, DEV + 0x2a) + dy);
  }
  return putword(s, DEV + 0x2c, addr);
}
```

## Device writes

DEO2 stores the first byte directly and invokes the device only on the second
port, matching Uxn 0K. Console output is capped at 8192 bytes. Unsupported device
writes set inspector flags; file operations report zero success.

```fern
<<Device writes>>=
function output(s: u8[], port: i32): u8[] {
  let value = s[DEV + port] as i32;
  if (port == 0x18 || port == 0x19) {
    let length = word(s, META + 12);
    if (length < 8192) {
      s = s.with(CONSOLE + length, value as u8);
      s = putword(s, META + 12, length + 1);
    }
  }
  if (port == 0x23 || port == 0x25) {
    let at = DEV + 0x22;
    let actual = META + 16;
    if (port == 0x25) {
      at = DEV + 0x24;
      actual = META + 18;
    }
    let size = word(s, at).max(1).min(512);
    let changed = size != word(s, actual);
    s = putword(s, at, size);
    s = putword(s, actual, size);
    if (changed) {
      for i in 0..262144 {
        s = s.with(SCREEN + i, 0);
      }
    }
  }
  if (port == 0x2e) {
    s = draw_pixel(s, value);
  }
  if (port == 0x2f) {
    s = draw_sprite(s, value);
  }
  if (port >= 0x30 && port < 0x70) {
    s = s.with(META + 14, s[META + 14] | 1);
  }
  if (port >= 0xa0 && port < 0xc0) {
    s = s.with(META + 14, s[META + 14] | 2);
    s = putword(s, DEV + (port & 0xf0) + 2, 0);
  }
  if (port == 0x03) {
    s = s.with(META + 14, s[META + 14] | 4);
  }
  if (port == 0x1f) {
    s = s.with(META + 14, s[META + 14] | 8);
  }
  return s;
}
```

## Execution

Keep mode reads through a temporary stack cursor and leaves the original items
in place. Return mode selects the other stack. Stack pointers wrap modulo 256;
zero-page shorts wrap at 0xff, RAM accesses at 0xffff, and devices at 0xff.
A slice executes at most 20000 instructions, so a ROM cannot monopolize the UI.
BRK yields to the host, preserving the next program counter for the inspector.

```fern
<<Execution>>=
@export("fern:showcase/uxn", "run")
pub function run(state: u8[], budget: i32, entry: i32): u8[] {
  if (state.len() != STATE_SIZE) {
    return [];
  }
  let s = state;
  let pc = word(s, META);
  let wp = s[META + 2] as i32;
  let rp = s[META + 3] as i32;
  let active = s[META + 4] != 0;
  if (entry > 0 && entry <= 65535 && s[DEV + 0x0f] == 0) {
    pc = entry;
    active = true;
  }
  let steps = word(s, META + 8) << 16 | word(s, META + 10);
  for cycle in 0..budget.max(0).min(20000) {
    if (!active) {
      break;
    }
    s = putword(s, META + 5, pc);
    let instruction = s[pc] as i32;
    pc = pc + 1 & 65535;
    steps = steps + 1;
    let op = instruction & 31;
    let size: i32 = 1;
    if ((instruction & 32) != 0) {
      size = 2;
    }
    let ret = (instruction & 64) != 0;
    let keep = (instruction & 128) != 0;
    let base = WST;
    let ptr = wp;
    if (ret) {
      base = RST;
      ptr = rp;
    }
    let sp = ptr;
    let a: i32 = 0;
    let b: i32 = 0;
    let c: i32 = 0;
    let consumed: i32 = 0;
    let result: i32 = 0;
    let outsize = size;
    let pushes: i32 = 0;
    if (op == 0) {
      if (instruction == 0) {
        active = false;
      } else if (instruction == 0x20 || instruction == 0x40 || instruction == 0x60) {
        let offset = s[pc] as i32 << 8 | s[pc + 1 & 65535] as i32;
        pc = pc + 2 & 65535;
        if (instruction == 0x20) {
          wp = wp - 1 & 255;
          if (s[WST + wp] != 0) {
            pc = pc + offset & 65535;
          }
        } else {
          if (instruction == 0x60) {
            s = push(s, RST, rp, pc, 2);
            rp = rp + 2 & 255;
          }
          pc = pc + offset & 65535;
        }
      } else {
        a = s[pc] as i32;
        pc = pc + 1 & 65535;
        if (size == 2) {
          a = a << 8 | s[pc] as i32;
          pc = pc + 1 & 65535;
        }
        s = push(s, base, ptr, a, size);
        if (ret) {
          rp = ptr + size & 255;
        } else {
          wp = ptr + size & 255;
        }
      }
    } else {
      // Decode each operand with its specified width before performing effects.
      let first = size;
      if (op == 0x10 || op == 0x11 || op == 0x12 || op == 0x13 || op == 0x16 || op == 0x17 || op == 0x1f) {
        first = 1;
      }
      if (op == 0x14 || op == 0x15) {
        first = 2;
      }
      a = peek(s, base, ptr, 0, first);
      consumed = first;
      if (op == 3 || op == 4 || op == 5 || op == 7 || op >= 8 && op <= 11 || op == 0x11 || op == 0x13 || op == 0x15 || op == 0x17 || op >= 0x18) {
        b = peek(s, base, ptr, consumed, size);
        consumed = consumed + size;
      }
      if (op == 5) {
        c = peek(s, base, ptr, consumed, size);
        consumed = consumed + size;
      }
      if (op == 0x0d) {
        b = peek(s, base, ptr, consumed, 1);
        consumed = consumed + 1;
      }
      if (!keep) {
        sp = ptr - consumed & 255;
      }
      if (ret) {
        rp = sp;
      } else {
        wp = sp;
      }
      if (op == 1) {
        result = a + 1;
        pushes = 1;
      }
      if (op == 3) {
        result = a;
        pushes = 1;
      }
      if (op == 4) {
        s = push(s, base, sp, a, size);
        sp = sp + size & 255;
        result = b;
        pushes = 1;
      }
      if (op == 5) {
        s = push(s, base, sp, b, size);
        sp = sp + size & 255;
        s = push(s, base, sp, a, size);
        sp = sp + size & 255;
        result = c;
        pushes = 1;
      }
      if (op == 6) {
        s = push(s, base, sp, a, size);
        sp = sp + size & 255;
        result = a;
        pushes = 1;
      }
      if (op == 7) {
        s = push(s, base, sp, b, size);
        sp = sp + size & 255;
        s = push(s, base, sp, a, size);
        sp = sp + size & 255;
        result = b;
        pushes = 1;
      }
      if (op >= 8 && op <= 11) {
        if (op == 8 && b == a || op == 9 && b != a || op == 10 && b > a || op == 11 && b < a) {
          result = 1;
        }
        pushes = 1;
        outsize = 1;
      }
      if (op == 12 || op == 13 || op == 14) {
        if (op == 14) {
          let other = RST;
          let otherptr = rp;
          if (ret) {
            other = WST;
            otherptr = wp;
          }
          s = push(s, other, otherptr, pc, 2);
          if (ret) {
            wp = wp + 2 & 255;
          } else {
            rp = rp + 2 & 255;
          }
        }
        if (op != 13 || b != 0) {
          if (size == 2) {
            pc = a;
          } else {
            pc = pc + signed(a) & 65535;
          }
        }
      }
      if (op == 15) {
        let other = RST;
        let otherptr = rp;
        if (ret) {
          other = WST;
          otherptr = wp;
        }
        s = push(s, other, otherptr, a, size);
        if (ret) {
          wp = wp + size & 255;
        } else {
          rp = rp + size & 255;
        }
      }
      if (op >= 0x10 && op <= 0x17) {
        let address = a;
        let mask = 65535;
        let offset: i32 = 0;
        if (op == 0x10 || op == 0x11) {
          mask = 255;
        }
        if (op == 0x12 || op == 0x13) {
          address = pc + signed(a) & 65535;
        }
        if (op == 0x16 || op == 0x17) {
          mask = 255;
          offset = DEV;
        }
        if (op == 0x16) {
          s = s.with(DEV + 4, wp as u8).with(DEV + 5, rp as u8);
          if (address == 0x22) {
            s = putword(s, DEV + 0x22, word(s, META + 16));
          }
          if (address == 0x24) {
            s = putword(s, DEV + 0x24, word(s, META + 18));
          }
        }
        if ((op & 1) == 0) {
          result = s[offset + address] as i32;
          if (size == 2) {
            result = result << 8 | s[offset + (address + 1 & mask)] as i32;
          }
          pushes = 1;
        } else {
          if (size == 2) {
            s = s.with(offset + address, (b >> 8) as u8);
            address = address + 1 & mask;
          }
          s = s.with(offset + address, b as u8);
          if (op == 0x17) {
            // Stack ports see the post-pop indexes; a write can replace them.
            s = s.with(DEV + 4, wp as u8).with(DEV + 5, rp as u8);
            if (size == 2) {
              s = s.with(DEV + a, (b >> 8) as u8);
            }
            s = s.with(DEV + address, b as u8);
            s = output(s, address);
          }
        }
      }
      if (op == 0x18) {
        result = b + a;
        pushes = 1;
      }
      if (op == 0x19) {
        result = b - a;
        pushes = 1;
      }
      if (op == 0x1a) {
        result = b * a;
        pushes = 1;
      }
      if (op == 0x1b) {
        if (a != 0) {
          result = b / a;
        }
        pushes = 1;
      }
      if (op == 0x1c) {
        result = b & a;
        pushes = 1;
      }
      if (op == 0x1d) {
        result = b | a;
        pushes = 1;
      }
      if (op == 0x1e) {
        result = b ^ a;
        pushes = 1;
      }
      if (op == 0x1f) {
        result = b >> (a & 15) << (a >> 4);
        pushes = 1;
      }
      if (pushes != 0) {
        s = push(s, base, sp, result, outsize);
        sp = sp + outsize & 255;
      }
      if (ret) {
        rp = sp;
      } else {
        wp = sp;
      }
      if (op == 0x17) {
        let last = a + size - 1 & 255;
        if (last == 4) {
          wp = s[DEV + 4] as i32;
        }
        if (last == 5) {
          rp = s[DEV + 5] as i32;
        }
      }
    }
  }
  s = putword(s, META, pc).with(META + 2, wp as u8).with(META + 3, rp as u8);
  s = s.with(DEV + 4, wp as u8).with(DEV + 5, rp as u8).with(META + 4, 0);
  if (active) {
    s = s.with(META + 4, 1);
  }
  s = putword(s, META + 8, steps >> 16);
  return putword(s, META + 10, steps);
}
```

## Assembler

The assembler supports all instructions and modes, hex bytes/shorts, literals,
absolute/relative padding, parent/child labels, all six address runes, immediate
jumps and calls, nested anonymous labels, raw ASCII strings, nested comments,
and macros defined before use. Includes and multi-bank ROMs are intentionally
excluded. Input is limited to 64 KiB, macro expansion to 4096 substitutions,
and expanded tokens to 32768. Errors return a message instead of a partial ROM.
The result begins with a status byte: zero followed by ROM bytes, or one followed
by a UTF-8 error. ROM bytes always start at address 0x0100.

```fern
<<Assembler>>=
struct Fixup { name: string, at: i32, kind: i32, token: string }

function fail(message: string): u8[] {
  return [1 as u8].concat(message.bytes());
}

function hex(s: string): i32 {
  if (s.len() == 0 || s.len() > 4) {
    return 0 - 1;
  }
  let value: i32 = 0;
  for c in s.bytes() {
    let digit = c as i32 - 48;
    if (c >= 97 && c <= 102) {
      digit = c as i32 - 87;
    } else if (c < 48 || c > 57) {
      return 0 - 1;
    }
    value = value * 16 + digit;
  }
  return value;
}

function opcode(token: string): i32 {
  if (token == "BRK") {
    return 0;
  }
  if (token == "JCI") {
    return 32;
  }
  if (token == "JMI") {
    return 64;
  }
  if (token == "JSI") {
    return 96;
  }
  let names = ["LIT", "INC", "POP", "NIP", "SWP", "ROT", "DUP", "OVR", "EQU", "NEQ", "GTH", "LTH", "JMP", "JCN", "JSR", "STH", "LDZ", "STZ", "LDR", "STR", "LDA", "STA", "DEI", "DEO", "ADD", "SUB", "MUL", "DIV", "AND", "ORA", "EOR", "SFT"];
  if (token.len() < 3) {
    return 0 - 1;
  }
  let op: i32 = 0 - 1;
  for i in 0..names.len() {
    if (token.take(3) == names[i]) {
      op = i;
    }
  }
  if (op < 0) {
    return op;
  }
  if (op == 0) {
    op = 128;
  }
  for c in token.drop(3).bytes() {
    let flag: i32 = 0;
    if (c == b'2') {
      flag = 32;
    }
    if (c == b'r') {
      flag = 64;
    }
    if (c == b'k') {
      flag = 128;
    }
    if (flag == 0) {
      return 0 - 1;
    }
    op = op | flag;
  }
  return op;
}

function qualify(name: string, scope: string): string {
  if (name.starts_with("&") || name.starts_with("/")) {
    return scope + "/" + name.drop(1);
  }
  return name;
}

function lex(source: string): Result[string[], string] {
  if (source.len() > 65536) {
    return Err("Source exceeds 64 KiB.");
  }
  let tokens: string[] = [];
  let at: i32 = 0;
  let depth: i32 = 0;
  while (at < source.len()) {
    if (source[at] <= 32) {
      at = at + 1;
      continue;
    }
    let start = at;
    while (at < source.len() && source[at] > 32) {
      at = at + 1;
    }
    let t = source.drop(start).take(at - start);
    if (t.starts_with("(")) {
      if (!t.ends_with(")")) {
        depth = depth + 1;
      }
      continue;
    }
    if (t == ")") {
      depth = depth - 1;
      if (depth < 0) {
        return Err("Unexpected comment closing parenthesis.");
      }
      continue;
    }
    if (depth != 0) {
      continue;
    }
    tokens = tokens.append(t);
    if (tokens.len() > 32768) {
      return Err("Too many source tokens.");
    }
  }
  if (depth != 0) {
    return Err("Unclosed comment.");
  }
  return Ok(tokens);
}

@export("fern:showcase/uxn", "assemble")
pub function assemble(source: string): u8[] {
  let tokens: string[] = [];
  match (lex(source)) {
    Ok(value) => {
      tokens = value;
    },
    Err(message) => {
      return fail(message);
    }
  }
  let labels: Map[string, i32] = map_new(256);
  let macros: Map[string, string[]] = map_new(64);
  let fixups: Fixup[] = [];
  let anonymous: string[] = [];
  let image = __alloc_u8(65536);
  let scope = "";
  let pc: i32 = 256;
  let end: i32 = 256;
  let i: i32 = 0;
  let expansions: i32 = 0;
  let unique: i32 = 0;
  while (i < tokens.len()) {
    let t = tokens[i];
    i = i + 1;
    if (t.starts_with("[") || t == "]") {
      continue;
    }
    if (t.starts_with("%")) {
      let name = t.drop(1);
      if (name == "" || macros.contains(name) || labels.contains(name) || opcode(name) >= 0 || hex(name) >= 0) {
        return fail("Invalid or duplicate macro: " + name);
      }
      if (i >= tokens.len() || tokens[i] != "{") {
        return fail("Expected { after macro " + name);
      }
      i = i + 1;
      let start = i;
      let nesting: i32 = 1;
      while (i < tokens.len() && nesting > 0) {
        if (tokens[i].ends_with("{") && !tokens[i].starts_with("\"")) {
          nesting = nesting + 1;
        }
        if (tokens[i] == "}") {
          nesting = nesting - 1;
        }
        i = i + 1;
      }
      if (nesting != 0) {
        return fail("Unclosed macro " + name);
      }
      macros = macros.insert(name, tokens.drop(start).take(i - start - 1));
      continue;
    }
    let macro_name = qualify(t, scope);
    if (macros.contains(macro_name)) {
      expansions = expansions + 1;
      let body = macros.get(macro_name).unwrap_or([] as string[]);
      tokens = tokens.take(i - 1).concat(body).concat(tokens.drop(i));
      i = i - 1;
      if (expansions > 4096 || tokens.len() > 32768) {
        return fail("Macro expansion limit exceeded.");
      }
      continue;
    }
    if (t == "}") {
      if (anonymous.len() == 0) {
        return fail("Unmatched }.");
      }
      labels = labels.insert(anonymous[anonymous.len() - 1], pc);
      anonymous = anonymous.take(anonymous.len() - 1);
      continue;
    }
    if (t.starts_with("@") || t.starts_with("&")) {
      let name = t.drop(1);
      if (t.starts_with("&")) {
        name = scope + "/" + name;
      }
      if (name == "" || labels.contains(name) || macros.contains(name) || opcode(name) >= 0 || hex(name) >= 0) {
        return fail("Invalid or duplicate label: " + name);
      }
      labels = labels.insert(name, pc);
      if (t.starts_with("@")) {
        scope = name;
        let slash = name.index_of("/").unwrap_or(0 - 1);
        if (slash >= 0) {
          scope = name.take(slash);
        }
      }
      continue;
    }
    if (t.starts_with("|") || t.starts_with("$")) {
      let amount = hex(t.drop(1));
      if (amount < 0) {
        amount = labels.get(qualify(t.drop(1), scope)).unwrap_or(0 - 1);
      }
      if (amount < 0) {
        return fail("Unknown padding: " + t);
      }
      if (t.starts_with("|")) {
        pc = amount;
      } else {
        pc = pc + amount;
      }
      if (pc > 65535) {
        return fail("Padding exceeds RAM: " + t);
      }
      continue;
    }
    let data: u8[] = [];
    let op = opcode(t);
    if (op >= 0) {
      data = [op as u8];
    } else if (t.starts_with("\"")) {
      data = t.drop(1).bytes();
    } else if (hex(t) >= 0 && (t.len() == 2 || t.len() == 4)) {
      if (t.len() == 4) {
        data = [(hex(t) >> 8) as u8, hex(t) as u8];
      } else {
        data = [hex(t) as u8];
      }
    } else if (t.starts_with("#")) {
      let h = t.drop(1);
      let value = hex(h);
      if (value < 0 || h.len() != 2 && h.len() != 4) {
        return fail("Invalid literal: " + t);
      }
      if (h.len() == 4) {
        data = [0xa0 as u8, (value >> 8) as u8, value as u8];
      } else {
        data = [0x80 as u8, value as u8];
      }
    } else {
      let kind: i32 = 0;
      let prefix = t.take(1);
      let name = t.drop(1);
      let literal = false;
      let instruction: i32 = 0;
      if (prefix == "," || prefix == "_") {
        kind = 1;
        literal = prefix == ",";
        instruction = 128;
      } else if (prefix == "." || prefix == "-") {
        kind = 2;
        literal = prefix == ".";
        instruction = 128;
      } else if (prefix == ";" || prefix == "=") {
        kind = 3;
        literal = prefix == ";";
        instruction = 160;
      } else if (prefix == "!" || prefix == "?") {
        kind = 4;
        literal = true;
        instruction = 64;
        if (prefix == "?") {
          instruction = 32;
        }
      } else {
        kind = 4;
        literal = true;
        instruction = 96;
        name = t;
      }
      if (name == "{") {
        unique = unique + 1;
        name = "~anonymous-" + unique.to_string();
        anonymous = anonymous.append(name);
      } else {
        name = qualify(name, scope);
      }
      if (name == "" || t.starts_with("~") || prefix == "}") {
        return fail("Unsupported token: " + t);
      }
      if (literal) {
        data = data.append(instruction as u8);
      }
      fixups = fixups.append(Fixup { name: name, at: pc + data.len(), kind: kind, token: t });
      data = data.append(0);
      if (kind >= 3) {
        data = data.append(0);
      }
    }
    if (data.len() > 0 && pc < 256) {
      return fail("Cannot emit bytes into the zero page: " + t);
    }
    if (pc + data.len() > 65536) {
      return fail("ROM exceeds 64 KiB address space.");
    }
    for value in data {
      image = image.with(pc, value);
      pc = pc + 1;
    }
    end = end.max(pc);
  }
  if (anonymous.len() != 0) {
    return fail("Unclosed anonymous label.");
  }
  for f in fixups {
    if (!labels.contains(f.name)) {
      return fail("Unknown label: " + f.token);
    }
    let value = labels.get(f.name).unwrap_or(0);
    if (f.kind == 1) {
      value = value - f.at - 2;
      if (value < 0 - 128 || value > 127) {
        return fail("Relative address out of range: " + f.token);
      }
    }
    if (f.kind == 4) {
      value = value - f.at - 2;
    }
    if (f.kind >= 3) {
      image = putword(image, f.at, value);
    } else {
      image = image.with(f.at, value as u8);
    }
  }
  if (end == 256) {
    return fail("No ROM bytes emitted.");
  }
  return [0 as u8].concat(image.drop(256).take(end - 256));
}
```

## Entry

The native command runs a small arithmetic program. Tests use the public boot,
run and assemble functions; the browser uses the same functions through Wasm.

```fern
<<Entry>>=
function main(): i32 {
  let rom = assemble("|0100 #02 #03 ADD BRK");
  if (rom[0] != 0) {
    return 1;
  }
  let s = run(boot(rom.drop(1)), 100, 0 - 1);
  if (s[WST] != 5 || s[META + 2] != 1 || s[META + 4] != 0) {
    return 2;
  }
  return 0;
}
```
