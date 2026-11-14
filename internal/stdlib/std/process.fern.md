# Child processes with pipes

This module runs a program as a child process, and collects all of its standard output and its standard error.

## Model

`run` finds the program in the directories of `PATH` when its name has no `/`. The child reads its standard input from a file, or from nothing. The parent reads the two pipes of the child with `poll`, so that a child that writes much to its standard error does not block. A program that does not start gives an `IoError`, and the parent knows it from a pipe that the `exec` closes.

## Contracts

`run` gives the bytes of the standard output and of the standard error, and the exit status: the exit code, or 128 and the number of the signal that stopped the child. It reads the pipes to their end, so the output has no limit of size. The function needs fork and exec, so it works only on a target with processes.

## Executable example

```fern test name=std-process-example
import "std/process";

function main(): i32 {
  // A program that no directory of PATH holds does not start.
  let Err(NotFound(name)) = process.run("no-such-program-of-the-example", [], "") else { return 1; };
  if (name != "no-such-program-of-the-example") { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
import "std/string";

<<the search of the program>>

<<the run>>
```

### The search of the program

```fern
<<the search of the program>>=
// The output of a child: its standard output, its standard error, and its
// exit status.
pub struct Output { stdout: u8[], stderr: u8[], status: i32 }

// The path of the program `name`: the name itself when it has a `/`, else
// the first file of that name in a directory of PATH that a user can run.
function __proc_find(name: string): Result[string, IoError] {
  if (__memchr(name, 47, 0) >= 0) {
    return Ok(name);
  }
  let path = match (env("PATH")) {
    Some(p) => p,
    None => "/usr/bin:/bin",
  };
  for dir in path.split(":") {
    let candidate = (if (dir == "") { "." } else { dir }) + "/" + name;
    if let Ok(st) = stat(candidate) {
      if (st.is_file && (st.mode & 73u32) != 0u32) {
        return Ok(candidate);
      }
    }
  }
  return Err(NotFound(name));
}

// The error of the number `errno` that the child sends when its exec fails.
function __proc_error(errno: i32, path: string): IoError {
  return match (errno) {
    2 => NotFound(path),
    13 => PermissionDenied(path),
    _ => Other("exec", path, errno),
  };
}
```

### The run

The child moves the file to its standard input and the pipes to its standard output and its standard error, and then runs the program. When `exec` fails, the child writes the error number to the pipe of the status, and it exits with 127. A successful `exec` closes that pipe, so the parent reads no byte from it.

```fern
<<the run>>=
// run runs `program` with the arguments `args`, and gives its output and
// its exit status. The standard input of the child is the file `input`, or
// /dev/null when `input` is "".
pub function run(program: string, args: string[], input: string): Result[Output, IoError] {
  let path = __proc_find(program)?;
  let source = open_reader(if (input == "") { "/dev/null" } else { input })?;
  let out = pipe()?;
  let err = pipe()?;
  let status = pipe()?;
  let pid = proc_fork();
  if (pid == 0) {
    __proc_child(path, args, source, out, err, status);
  }
  let _ = source.close();
  let _ = out.w.close();
  let _ = err.w.close();
  let _ = status.w.close();
  if (pid < 0) {
    return Err(Other("fork", program, -pid));
  }
  let failed = __proc_read_all(status.r);
  if (failed.len() >= 4) {
    __proc_wait(pid);
    return Err(__proc_error(failed[0] as i32 | failed[1] as i32 << 8 | failed[2] as i32 << 16 | failed[3] as i32 << 24, program));
  }
  let (stdout, stderr) = __proc_drain(out.r, err.r);
  return Ok(Output { stdout: stdout, stderr: stderr, status: __proc_wait(pid) });
}

// The child: it moves its streams, and runs the program. It does not
// return.
function __proc_child(path: string, args: string[], source: Reader, out: Pipe, err: Pipe, status: Pipe): void {
  let _ = source.dup_onto(0);
  let _ = out.w.dup_onto(1);
  let _ = err.w.dup_onto(2);
  let rc = -proc_exec(path, args);
  let _ = status.w.write_bytes([(rc & 255) as u8, (rc >> 8 & 255) as u8, (rc >> 16 & 255) as u8, (rc >> 24 & 255) as u8]);
  exit(127);
}

// The bytes of a pipe up to its end.
function __proc_read_all(own r: Reader): u8[] {
  let parts: u8[][] = [];
  while (true) {
    let Ok(chunk) = r.read_chunk_bytes(65536) else {
      break;
    };
    if (chunk.len() == 0) {
      break;
    }
    parts = parts.append(chunk);
  }
  let _ = r.close();
  return __proc_join(parts);
}

function __proc_join(parts: u8[][]): u8[] {
  let n = 0;
  for p in parts {
    n += p.len();
  }
  let out = __alloc_u8(n);
  let at = 0;
  for p in parts {
    for k in 0..p.len() {
      out = out.with(at + k, p[k]);
    }
    at += p.len();
  }
  return out;
}

// Reads both pipes to their end in the order that their data comes.
function __proc_drain(own out: Reader, own err: Reader): (u8[], u8[]) {
  let outs: u8[][] = [];
  let errs: u8[][] = [];
  let open = [true, true];
  while (open[0] || open[1]) {
    let k = __proc_ready(out.fd, err.fd, open);
    let chunk = if (k == 0) { __proc_chunk(out) } else { __proc_chunk(err) };
    if (chunk.len() == 0) {
      open = open.with(k, false);
    } else if (k == 0) {
      outs = outs.append(chunk);
    } else {
      errs = errs.append(chunk);
    }
  }
  let _ = out.close();
  let _ = err.close();
  return (__proc_join(outs), __proc_join(errs));
}

// The pipe to read next: 0 for the standard output and 1 for the standard
// error. `open` says which pipes have not ended.
function __proc_ready(out: i32, err: i32, open: boolean[]): i32 {
  if (!open[0] || !open[1]) {
    return if (open[0]) { 0 } else { 1 };
  }
  let hit = poll([out, err], -1);
  return if (hit == 1) { 1 } else { 0 };
}

// The next bytes of a pipe, or none at its end or after an error.
function __proc_chunk(r: Reader): u8[] {
  return match (r.read_chunk_bytes(65536)) {
    Ok(c) => c,
    Err(_) => [],
  };
}

// Waits for the child, and gives its exit code, or 128 and the number of
// the signal that stopped it.
function __proc_wait(pid: i32): i32 {
  let w = proc_waitpid_status(pid);
  while (w == -4) {
    w = proc_waitpid_status(pid);
  }
  if (w < 0) {
    return 127;
  }
  return if ((w & 127) == 0) { w >> 8 & 255 } else { 128 + (w & 127) };
}
```
