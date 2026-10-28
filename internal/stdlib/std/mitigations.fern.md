# Hardware mitigation report

This module reports which hardware control-flow and memory mitigations the processor offers, the kernel enables, and this process uses.

## Model

`report` returns one `Support` row per mitigation the target can carry. Each row holds three `State` values. `cpu` says whether the processor lists the feature. `kernel` says whether the running kernel enables it for user space. `process` says whether this process runs with it active.

A `State` is `Yes`, `No`, or `Unknown`. `Unknown` means that the source for the cell was unavailable, not that the mitigation is absent.

The rows on x86-64 Linux are `shstk`, the CET shadow stack, and `ibt`, indirect branch tracking. The rows on arm64 Linux are `pac`, pointer authentication, `bti`, branch target identification, and `mte`, memory tagging. Other targets return no rows.

`manifest` returns one `Status` row per mitigation with five cells. `compiled` says whether the image was built with the feature, from the build manifest the compiler stamps into every native executable. `supported` says whether the processor and the kernel have it. `requested` says whether the image asks for it at startup. `enabled` says whether this process runs with it. `verified` names the evidence that the feature holds, which is a test name and the host kind it ran on.

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

The build manifest is the line that starts with `FERNBUILD1 ` in the image at `/proc/self/exe`. `build_line` finds it in any byte array, and `parse_build` reads its `key=value` fields. Without the line, which an interpreted or a wasm program has, every `compiled` cell is `unknown`. A software feature counts as `enabled` when it is compiled, because nothing can turn it off. A hardware feature takes `enabled` from the `process` cell of `report`.

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

<<13 pub struct Build>>

<<14 pub function build_line(image: [u8]): Option[string]>>

<<15 pub function parse_build(line: string): Option[Build]>>

<<16 pub struct Status>>

<<17 pub function manifest(): Status[]>>

<<18 function __status_rows(build: Option[Build], support: Support[]): Status[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reports which hardware mitigations the processor offers, the kernel enables, and this process uses.
import "std/string";
import "std/utf8";
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

### `13 pub struct Build`

```fern
<<13 pub struct Build>>=
pub struct Build { target: string, profile: string, harden: boolean, pie: boolean, sanitize: boolean, backtrace: boolean, size: boolean, shared: boolean, heap_limit: i64 }
```

### `14 pub function build_line(image: [u8]): Option[string]`

The line ends at the first newline after the magic. The magic is built from bytes at run time, so no literal pool holds it. A candidate must name its target and carry every field, which a stray copy of the magic does not.

```fern
<<14 pub function build_line(image: [u8]): Option[string]>>=
pub function build_line(image: [u8]): Option[string] {
  let magic: string = string_from_bytes_unchecked([70, 69, 82, 78, 66, 85, 73, 76, 68, 49, 32, 116, 97, 114, 103, 101, 116, 61]);
  let i: i32 = 0;
  while (i + magic.len() <= image.len()) {
    let j: i32 = 0;
    while (j < magic.len() && image[i + j] == magic[j]) {
      j = j + 1;
    }
    if (j == magic.len()) {
      let start: i32 = i + magic.len() - 7;
      let end: i32 = start;
      while (end < image.len() && end - start < 512 && image[end] != b'\n') {
        end = end + 1;
      }
      if (end < image.len() && image[end] == b'\n') {
        match (utf8.from_bytes(image[start:end])) {
          Some(line) => {
            if (__manifest_shape(line)) {
              return Some(line);
            }
          },
          None => {}
        }
      }
    }
    i = i + 1;
  }
  return None;
}

// A real manifest names its target and carries every field; a copy of the
// magic in a literal pool is followed by other text.
function __manifest_shape(line: string): boolean {
  return line.len() > 7 && line[7] != b' ' && line.contains(" profile=") && line.contains(" harden=") && line.contains(" heap_limit=");
}
```

### `15 pub function parse_build(line: string): Option[Build]`

A field without `=` or an unknown key is ignored, so a later manifest with more fields still parses.

```fern
<<15 pub function parse_build(line: string): Option[Build]>>=
pub function parse_build(line: string): Option[Build] {
  let b: Build = Build { target: "", profile: "", harden: false, pie: false, sanitize: false, backtrace: false, size: false, shared: false, heap_limit: 0i64 };
  let seen: boolean = false;
  for field in line.trim().split(" ") {
    let eq: i32 = __memchr(field, 61, 0);
    if (eq < 0) {
      eq = field.len();
    } else {
      seen = true;
    }
    let key: string = slice_unchecked(field, 0, eq) + "";
    let value: string = "";
    if (eq < field.len()) {
      value = slice_unchecked(field, eq + 1, field.len()) + "";
    }
    let on: boolean = value == "1";
    if (key == "target") {
      b = Build { ...b, target: value };
    } else if (key == "profile") {
      b = Build { ...b, profile: value };
    } else if (key == "harden") {
      b = Build { ...b, harden: on };
    } else if (key == "pie") {
      b = Build { ...b, pie: on };
    } else if (key == "sanitize") {
      b = Build { ...b, sanitize: on };
    } else if (key == "backtrace") {
      b = Build { ...b, backtrace: on };
    } else if (key == "size") {
      b = Build { ...b, size: on };
    } else if (key == "shared") {
      b = Build { ...b, shared: on };
    } else if (key == "heap_limit") {
      b = Build { ...b, heap_limit: __digits(value) };
    }
  }
  if (!seen) {
    return None;
  }
  return Some(b);
}
```

### `16 pub struct Status`

```fern
<<16 pub struct Status>>=
pub struct Status { name: string, compiled: string, supported: string, requested: string, enabled: string, verified: string }

pub function (s: Status) line(): string {
  return s.name + " compiled=" + s.compiled + " supported=" + s.supported + " requested=" + s.requested + " enabled=" + s.enabled + " verified=" + s.verified;
}
```

### `17 pub function manifest(): Status[]`

```fern
<<17 pub function manifest(): Status[]>>=
pub function manifest(): Status[] {
  let build: Option[Build] = None;
  if (target_os() == "linux" || target_os() == "android") {
    match (build_line(__bytes("/proc/self/exe"))) {
      Some(line) => {
        build = parse_build(line);
      },
      None => {}
    }
  }
  return __status_rows(build, report());
}
```

### `18 function __status_rows(build: Option[Build], support: Support[]): Status[]`

A software feature is `enabled` when it is compiled. A hardware feature takes `supported` from the `cpu` and `kernel` cells and `enabled` from the `process` cell.

```fern
<<18 function __status_rows(build: Option[Build], support: Support[]): Status[]>>=
function __status_rows(build: Option[Build], support: Support[]): Status[] {
  let harden: string = "unknown";
  let pie: string = "unknown";
  let sanitize: string = "unknown";
  let compiled: string = "unknown";
  match (build) {
    Some(b) => {
      harden = __flag(b.harden);
      pie = __flag(b.pie);
      sanitize = __flag(b.sanitize);
      compiled = "yes";
    },
    None => {}
  }
  let rows: Status[] = [
    Status { name: "nx-stack", compiled: compiled, supported: "yes", requested: compiled, enabled: compiled, verified: "native (TestSelfHostImagePermissions)" },
    Status { name: "canary", compiled: harden, supported: "yes", requested: harden, enabled: harden, verified: "native (TestSelfHostStackCanary)" },
    Status { name: "cfi", compiled: harden, supported: "yes", requested: harden, enabled: harden, verified: "native (TestSelfHostForwardEdgeCFI)" },
    Status { name: "pie", compiled: pie, supported: "yes", requested: pie, enabled: pie, verified: "native (TestSelfHostPIE)" },
    Status { name: "sanitizer", compiled: sanitize, supported: "yes", requested: sanitize, enabled: sanitize, verified: "native (the sanitizer suites)" },
  ];
  for row in support {
    let have: string = __both(row.cpu, row.kernel).to_string();
    let process: string = row.process.to_string();
    if (row.name == "shstk") {
      rows = rows.append(Status { name: "shadow-stack", compiled: harden, supported: have, requested: harden, enabled: process, verified: "unverified (no host with the feature yet)" });
    } else if (row.name == "ibt") {
      rows = rows.append(Status { name: "ibt", compiled: harden, supported: have, requested: "no", enabled: process, verified: "unavailable (Linux enforces no user-space IBT)" });
    } else if (row.name == "pac") {
      rows = rows.append(Status { name: "pac", compiled: harden, supported: have, requested: harden, enabled: process, verified: "native, one host (TestSelfHostHardenArm64)" });
    } else if (row.name == "bti") {
      rows = rows.append(Status { name: "bti", compiled: harden, supported: have, requested: harden, enabled: process, verified: "emulated (TestSelfHostHardenArm64 under QEMU)" });
    } else if (row.name == "mte") {
      rows = rows.append(Status { name: "mte", compiled: "no", supported: have, requested: "no", enabled: process, verified: "design (MTE-DESIGN.md)" });
    }
  }
  return rows;
}

function __flag(on: boolean): string {
  if (on) {
    return "yes";
  }
  return "no";
}

function __digits(text: string): i64 {
  let n: i64 = 0i64;
  for c in text.bytes() {
    if (c < b'0' || c > b'9') {
      return 0i64;
    }
    n = n * 10i64 + (c - b'0') as i64;
  }
  return n;
}
```
