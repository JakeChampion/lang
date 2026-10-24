# Structured log entries

This module writes log messages.

## Model

A logger combines a minimum level, text format, and sink. An entry accumulates typed fields before it renders or emits a message.

## Contracts

A custom sink controls the destination. The render method returns text without an emission. Level constants and level names use the same order.

## Executable example

```fern test name=std-log-example
import "std/log";

function main(): i32 {
  if (log.level_name(log.WARN) != "WARN") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub function info(msg: string): void>>

<<3 pub const TRACE: i32>>

<<4 pub function level_name(n: i32): string>>

<<5 pub struct Logger>>

<<6 pub function new(min_level: i32): Logger>>

<<7 function __to_stderr(line: string): void>>

<<8 pub struct LogEntry>>

<<9 pub function (lg: Logger) at(level: i32): LogEntry>>

<<10 function __line(e: LogEntry, msg: string): string>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module writes log messages.
import "std/i32";
import "std/json";
```

### `2 pub function info(msg: string): void`

```fern
<<2 pub function info(msg: string): void>>=
pub function info(msg: string): void {
  eprint("[INFO] " + msg);
}

pub function warn(msg: string): void {
  eprint("[WARN] " + msg);
}

pub function error(msg: string): void {
  eprint("[ERROR] " + msg);
}
```

### `3 pub const TRACE: i32`

```fern
<<3 pub const TRACE: i32>>=
pub const TRACE: i32 = 0;

pub const DEBUG: i32 = 1;

pub const INFO: i32 = 2;

pub const WARN: i32 = 3;

pub const ERROR: i32 = 4;
```

### `4 pub function level_name(n: i32): string`

```fern
<<4 pub function level_name(n: i32): string>>=
pub function level_name(n: i32): string {
  if (n <= 0) {
    return "TRACE";
  }
  if (n == 1) {
    return "DEBUG";
  }
  if (n == 2) {
    return "INFO";
  }
  if (n == 3) {
    return "WARN";
  }
  return "ERROR";
}
```

### `5 pub struct Logger`

```fern
<<5 pub struct Logger>>=
pub struct Logger {
  min_level: i32,
  json: boolean,
  sink: (string) => void,
}
```

### `6 pub function new(min_level: i32): Logger`

```fern
<<6 pub function new(min_level: i32): Logger>>=
pub function new(min_level: i32): Logger {
  return Logger { min_level: min_level, json: false, sink: __to_stderr };
}

pub function new_json(min_level: i32): Logger {
  return Logger { min_level: min_level, json: true, sink: __to_stderr };
}

pub function (lg: Logger) to(sink: (string) => void): Logger {
  return Logger { min_level: lg.min_level, json: lg.json, sink: sink };
}
```

### `7 function __to_stderr(line: string): void`

```fern
<<7 function __to_stderr(line: string): void>>=
function __to_stderr(line: string): void {
  eprint(line);
}
```

### `8 pub struct LogEntry`

```fern
<<8 pub struct LogEntry>>=
pub struct LogEntry {
  min_level: i32,
  is_json: boolean,
  level: i32,
  text: string,
  json: string,
  sink: (string) => void,
}
```

### `9 pub function (lg: Logger) at(level: i32): LogEntry`

```fern
<<9 pub function (lg: Logger) at(level: i32): LogEntry>>=
pub function (lg: Logger) at(level: i32): LogEntry {
  return LogEntry { min_level: lg.min_level, is_json: lg.json, level: level, text: "", json: "", sink: lg.sink };
}

pub function (lg: Logger) trace_(): LogEntry {
  return lg.at(TRACE);
}

pub function (lg: Logger) debug_(): LogEntry {
  return lg.at(DEBUG);
}

pub function (lg: Logger) info_(): LogEntry {
  return lg.at(INFO);
}

pub function (lg: Logger) warn_(): LogEntry {
  return lg.at(WARN);
}

pub function (lg: Logger) error_(): LogEntry {
  return lg.at(ERROR);
}

pub function (e: LogEntry) str(key: string, val: string): LogEntry {
  let t: string = e.text + " " + key + "=" + val;
  let j: string = e.json + ",\"" + json.json_escape(key) + "\":\"" + json.json_escape(val) + "\"";
  return LogEntry { min_level: e.min_level, is_json: e.is_json, level: e.level, text: t, json: j, sink: e.sink };
}

pub function (e: LogEntry) int(key: string, val: i32): LogEntry {
  let t: string = e.text + " " + key + "=" + val.to_string();
  let j: string = e.json + ",\"" + json.json_escape(key) + "\":" + val.to_string();
  return LogEntry { min_level: e.min_level, is_json: e.is_json, level: e.level, text: t, json: j, sink: e.sink };
}

pub function (e: LogEntry) bool(key: string, val: boolean): LogEntry {
  let vs: string = "false";
  if (val) {
    vs = "true";
  }
  let t: string = e.text + " " + key + "=" + vs;
  let j: string = e.json + ",\"" + json.json_escape(key) + "\":" + vs;
  return LogEntry { min_level: e.min_level, is_json: e.is_json, level: e.level, text: t, json: j, sink: e.sink };
}

pub function (e: LogEntry) render(msg: string): string {
  if (e.level < e.min_level) {
    return "";
  }
  return __line(e, msg) + "\n";
}

pub function (e: LogEntry) emit(msg: string): void {
  if (e.level < e.min_level) {
    return;
  }
  let sink: (string) => void = e.sink;
  sink(__line(e, msg));
}
```

### `10 function __line(e: LogEntry, msg: string): string`

```fern
<<10 function __line(e: LogEntry, msg: string): string>>=
function __line(e: LogEntry, msg: string): string {
  if (e.is_json) {
    return "{\"level\":\"" + level_name(e.level) + "\",\"msg\":\"" + json.json_escape(msg) + "\"" + e.json + "}";
  }
  return "[" + level_name(e.level) + "] " + msg + e.text;
}
```
