# Hardware mitigation report

This module reports which hardware control-flow and memory mitigations the processor offers, the kernel enables, and this process uses.

## Model

`report` returns one `Support` row per mitigation the target can carry. Each row holds three `State` values. `cpu` says whether the processor lists the feature. `kernel` says whether the running kernel enables it for user space. `process` says whether this process runs with it active.

A `State` is `Yes`, `No`, or `Unknown`. `Unknown` means that the source for the cell was unavailable, not that the mitigation is absent.

The rows on x86-64 Linux are `shstk`, the CET shadow stack, and `ibt`, indirect branch tracking. The rows on arm64 Linux are `pac`, pointer authentication, `bti`, branch target identification, and `mte`, memory tagging. Other targets return no rows.

## Contracts

Each cell comes from one source, and the module reads nothing else.

| Row | `cpu` | `kernel` | `process` |
|---|---|---|---|
| `shstk` | `user_shstk` in the `flags` line of `/proc/cpuinfo` | `/proc/self/status` has an `x86_Thread_features` line | that line lists `shstk` |
| `ibt` | `ibt` in the `flags` line | the `x86_Thread_features` line lists `ibt` | the same line lists `ibt` |
| `pac` | `paca` and `pacg` in the `Features` line | bits 30 and 31 of `AT_HWCAP` in `/proc/self/auxv` | `prctl(PR_PAC_GET_ENABLED_KEYS)` answers a nonzero key set |
| `bti` | `bti` in the `Features` line | bit 17 of `AT_HWCAP2` | a `VmFlags` line of `/proc/self/smaps` lists `bt` |
| `mte` | `mte` in the `Features` line | bit 18 of `AT_HWCAP2` | `prctl(PR_GET_TAGGED_ADDR_CTRL)` answers a tag check mode |

A cell is `Unknown` when its file cannot be read or has no line of the expected shape. A cell is also `Unknown` when the kernel refuses the query. A token must match a flag exactly, so `bti` does not match `btix`. The `x86_Thread_features_locked` line is not the `x86_Thread_features` line.

The kernel has no interface that enables indirect branch tracking for user space. The `ibt` row therefore reads `kernel` and `process` from the same line. Both stay `No` on a current kernel.

## Executable example

```fern test name=std-mitigations-example
import "std/mitigations";
import "std/option";

function main(): i32 {
  let auxv: u8[] = [];
  for b in [26, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0] { auxv = auxv.append(b as u8); }
  match (mitigations.auxv_value(auxv, 26i64)) {
    Some(v) => { if (v != 131072i64) { return 1; } },
    None => { return 2; }
  }
  if (mitigations.auxv_value(auxv, 16i64).is_some()) { return 3; }
  let cpu: string = "processor\t: 0\nFeatures\t: fp asimd btix\n";
  if (mitigations.feature_listed(cpu, "bti").to_string() != "no") { return 4; }
  if (mitigations.feature_listed(cpu, "asimd").to_string() != "yes") { return 5; }
  if (mitigations.feature_listed("", "bti").to_string() != "unknown") { return 6; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub enum State>>

<<3 pub struct Support>>

<<4 pub function report(): Support[]>>

<<5 function __x86_rows(cpuinfo: string, status: string): Support[]>>

<<6 function __arm64_rows(cpuinfo: string, auxv: u8[], smaps: string, pac_keys: i64, tag_ctl: i64): Support[]>>

<<7 pub function auxv_value(auxv: [u8], key: i64): Option[i64]>>

<<8 pub function feature_listed(cpuinfo: string, flag: string): State>>

<<9 pub function thread_features(status: string): Option[string[]]>>

<<10 pub function mapping_flag(smaps: string, flag: string): State>>

<<11 function __tokens(line: string): string[]>>

<<12 function __text(path: string): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reports which hardware mitigations the processor offers, the kernel enables, and this process uses.
import "std/string";
```

### `2 pub enum State`

```fern
<<2 pub enum State>>=
pub enum State {
  Yes,
  No,
  Unknown,
}

pub function (s: State) to_string(): string {
  let out: string = "unknown";
  match (s) {
    State.Yes => {
      out = "yes";
    },
    State.No => {
      out = "no";
    },
    State.Unknown => {}
  }
  return out;
}
```

### `3 pub struct Support`

```fern
<<3 pub struct Support>>=
pub struct Support { name: string, cpu: State, kernel: State, process: State }

pub function (s: Support) line(): string {
  return s.name + " cpu=" + s.cpu.to_string() + " kernel=" + s.kernel.to_string() + " process=" + s.process.to_string();
}
```

### `4 pub function report(): Support[]`

The kernel queries run only inside the Linux branch, so the other targets do not reach the `sysinfo` capability.

```fern
<<4 pub function report(): Support[]>>=
pub function report(): Support[] {
  let rows: Support[] = [];
  if (target_os() == "linux" || target_os() == "android") {
    let cpuinfo: string = __text("/proc/cpuinfo");
    if (target_arch() == "x86-64") {
      rows = __x86_rows(cpuinfo, __text("/proc/self/status"));
    }
    if (target_arch() == "arm64") {
      rows = __arm64_rows(cpuinfo, __bytes("/proc/self/auxv"), __text("/proc/self/smaps"), prctl_query(61), prctl_query(56));
    }
  }
  return rows;
}
```

### `5 function __x86_rows(cpuinfo: string, status: string): Support[]`

```fern
<<5 function __x86_rows(cpuinfo: string, status: string): Support[]>>=
function __x86_rows(cpuinfo: string, status: string): Support[] {
  let kernel: State = State.Unknown;
  let shstk: State = State.Unknown;
  let ibt: State = State.Unknown;
  if (status.len() > 0) {
    kernel = State.No;
    shstk = State.No;
    ibt = State.No;
    match (thread_features(status)) {
      Some(features) => {
        kernel = State.Yes;
        shstk = __listed(features, "shstk");
        ibt = __listed(features, "ibt");
      },
      None => {}
    }
  }
  let rows: Support[] = [];
  rows = rows.append(Support { name: "shstk", cpu: feature_listed(cpuinfo, "user_shstk"), kernel: kernel, process: shstk });
  rows = rows.append(Support { name: "ibt", cpu: feature_listed(cpuinfo, "ibt"), kernel: ibt, process: ibt });
  return rows;
}
```

### `6 function __arm64_rows(cpuinfo: string, auxv: u8[], smaps: string, pac_keys: i64, tag_ctl: i64): Support[]`

`pac_keys` is the answer of `prctl(PR_PAC_GET_ENABLED_KEYS)` and `tag_ctl` the answer of `prctl(PR_GET_TAGGED_ADDR_CTRL)`. A negative answer is the negated errno, and a kernel without the query answers `EINVAL`.

```fern
<<6 function __arm64_rows(cpuinfo: string, auxv: u8[], smaps: string, pac_keys: i64, tag_ctl: i64): Support[]>>=
function __arm64_rows(cpuinfo: string, auxv: u8[], smaps: string, pac_keys: i64, tag_ctl: i64): Support[] {
  let hwcap: Option[i64] = auxv_value(auxv, 16i64);
  let hwcap2: Option[i64] = auxv_value(auxv, 26i64);
  let rows: Support[] = [];
  rows = rows.append(Support {
    name: "pac",
    cpu: __both(feature_listed(cpuinfo, "paca"), feature_listed(cpuinfo, "pacg")),
    kernel: __bits(hwcap, 1i64 << 30 | 1i64 << 31),
    process: __answer(pac_keys, 0i64 - 1i64),
  });
  rows = rows.append(Support {
    name: "bti",
    cpu: feature_listed(cpuinfo, "bti"),
    kernel: __bits(hwcap2, 1i64 << 17),
    process: mapping_flag(smaps, "bt"),
  });
  rows = rows.append(Support {
    name: "mte",
    cpu: feature_listed(cpuinfo, "mte"),
    kernel: __bits(hwcap2, 1i64 << 18),
    process: __answer(tag_ctl, 6i64),
  });
  return rows;
}

function __both(a: State, b: State): State {
  if (a.to_string() == "unknown" || b.to_string() == "unknown") {
    return State.Unknown;
  }
  if (a.to_string() == "yes" && b.to_string() == "yes") {
    return State.Yes;
  }
  return State.No;
}

function __bits(word: Option[i64], mask: i64): State {
  let out: State = State.Unknown;
  match (word) {
    Some(w) => {
      out = State.No;
      if ((w & mask) == mask) {
        out = State.Yes;
      }
    },
    None => {}
  }
  return out;
}

// A kernel answer below zero is an errno. Otherwise the bits under the mask say whether the feature is active.
function __answer(answer: i64, mask: i64): State {
  if (answer < 0i64) {
    return State.Unknown;
  }
  if ((answer & mask) != 0i64) {
    return State.Yes;
  }
  return State.No;
}
```

### `7 pub function auxv_value(auxv: [u8], key: i64): Option[i64]`

The auxiliary vector is a sequence of little-endian key and value words that ends at an `AT_NULL` key. A truncated vector answers `None` for a key past the truncation.

```fern
<<7 pub function auxv_value(auxv: [u8], key: i64): Option[i64]>>=
pub function auxv_value(auxv: [u8], key: i64): Option[i64] {
  let i: i32 = 0;
  while (i + 16 <= auxv.len()) {
    let k: i64 = __word(auxv, i);
    if (k == 0i64) {
      return None;
    }
    if (k == key) {
      return Some(__word(auxv, i + 8));
    }
    i = i + 16;
  }
  return None;
}

function __word(bytes: [u8], at: i32): i64 {
  let v: i64 = 0i64;
  let j: i32 = 7;
  while (j >= 0) {
    v = v << 8 | bytes[at + j] as i64;
    j = j - 1;
  }
  return v;
}
```

### `8 pub function feature_listed(cpuinfo: string, flag: string): State`

The first `flags` line on x86-64 or `Features` line on arm64 lists the features of the first processor.

```fern
<<8 pub function feature_listed(cpuinfo: string, flag: string): State>>=
pub function feature_listed(cpuinfo: string, flag: string): State {
  for line in cpuinfo.split("\n") {
    if (line.starts_with("flags") || line.starts_with("Features")) {
      return __listed(__tokens(line), flag);
    }
  }
  return State.Unknown;
}

function __listed(tokens: string[], flag: string): State {
  for tok in tokens {
    if (tok == flag) {
      return State.Yes;
    }
  }
  return State.No;
}
```

### `9 pub function thread_features(status: string): Option[string[]]`

```fern
<<9 pub function thread_features(status: string): Option[string[]]>>=
pub function thread_features(status: string): Option[string[]] {
  for line in status.split("\n") {
    if (line.starts_with("x86_Thread_features:")) {
      return Some(__tokens(line));
    }
  }
  return None;
}
```

### `10 pub function mapping_flag(smaps: string, flag: string): State`

```fern
<<10 pub function mapping_flag(smaps: string, flag: string): State>>=
pub function mapping_flag(smaps: string, flag: string): State {
  let seen: boolean = false;
  for line in smaps.split("\n") {
    if (line.starts_with("VmFlags:")) {
      seen = true;
      if (__listed(__tokens(line), flag).to_string() == "yes") {
        return State.Yes;
      }
    }
  }
  if (seen) {
    return State.No;
  }
  return State.Unknown;
}
```

### `11 function __tokens(line: string): string[]`

```fern
<<11 function __tokens(line: string): string[]>>=
function __tokens(line: string): string[] {
  let out: string[] = [];
  let parts: string[] = line.split(":");
  if (parts.len() < 2) {
    return out;
  }
  for tok in parts[1].trim().split(" ") {
    if (tok.len() > 0) {
      out = out.append(tok);
    }
  }
  return out;
}
```

### `12 function __text(path: string): string`

An unreadable file is an empty text, which every parser reports as `Unknown`.

```fern
<<12 function __text(path: string): string>>=
function __text(path: string): string {
  let out: string = "";
  match (read_file(path)) {
    Ok(s) => {
      out = s;
    },
    Err(_) => {}
  }
  return out;
}

function __bytes(path: string): u8[] {
  let out: u8[] = [];
  match (read_file_bytes(path)) {
    Ok(b) => {
      out = b;
    },
    Err(_) => {}
  }
  return out;
}
```
