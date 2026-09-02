// web/wasi-shim.js — a tiny WASI preview-1 host, just enough to run
// the core modules the Fern wasm backend emits for the wasi:cli/run
// world directly in a browser.
//
// The playground's "Run (wasm)" button compiles source to a raw
// preview-1 core command module (fernCompileCoreWasm → an exported
// `_start` + `memory`, classic `wasi_snapshot_preview1` imports) and
// hands the bytes here. Because we own the emitter we know the exact,
// small import surface the modules reach for — stdout via fd_write,
// process exit, and the clock / random helpers — so a hand-written
// host is ~a screen of code with no jco, no Component Model
// transpile, no CDN, and no Node `fs` dependency (which is what broke
// the earlier in-browser jco attempt).
//
// This is a playground host, not a general WASI runtime: anything we
// haven't implemented traps with a clear "unsupported WASI call"
// message rather than silently misbehaving.

// ExitSignal unwinds the `_start` call when the guest invokes
// proc_exit. We can't return normally from inside a wasm call to
// abort it, so proc_exit throws this and runCoreWasm catches it.
class ExitSignal {
  constructor(code) {
    this.code = code;
  }
}

// runCoreWasm instantiates a preview-1 core module and runs its
// `_start`, returning the captured stdout / stderr and the exit code.
//
// `opts.stdin` is the guest's standard input, read once and then at
// EOF; `opts.args` is its full argv, argv[0] included, the way a
// process receives one. Both default to empty. A guest that wants
// neither never calls fd_read or args_get, so passing nothing keeps
// the old behaviour exactly.
//
// The exit code is the guest's own: the emitted `_start` ends in
// proc_exit(main()), so a program that returns 20 reports 20 without
// having to call exit() itself.
export async function runCoreWasm(bytes, opts = {}) {
  const stdoutChunks = [];
  const stderrChunks = [];
  let exitCode = 0;
  let memory = null;

  const encoder = new TextEncoder();
  const stdin = encoder.encode(opts.stdin ?? "");
  let stdinPos = 0;
  const argv = opts.args ?? [];

  const decoder = new TextDecoder();
  // memory.buffer detaches whenever the guest grows linear memory, so
  // build fresh views per call rather than caching them.
  const view = () => new DataView(memory.buffer);
  const bytesAt = (ptr, len) => new Uint8Array(memory.buffer, ptr, len);

  // fd_write: gather the iovec array (pairs of i32 ptr,len) and append
  // the decoded text to stdout (fd 1) or stderr (fd 2). Writes the
  // total byte count back to nwrittenPtr and reports success.
  function fdWrite(fd, iovsPtr, iovsCount, nwrittenPtr) {
    const dv = view();
    const sink = fd === 2 ? stderrChunks : stdoutChunks;
    let written = 0;
    for (let i = 0; i < iovsCount; i++) {
      const base = dv.getUint32(iovsPtr + i * 8, true);
      const len = dv.getUint32(iovsPtr + i * 8 + 4, true);
      if (len > 0) sink.push(decoder.decode(bytesAt(base, len)));
      written += len;
    }
    dv.setUint32(nwrittenPtr, written, true);
    return 0;
  }

  // fd_read: fill the iovec array from the remaining stdin, write the
  // byte count to nreadPtr and report success. A short read is a
  // legitimate answer under preview-1, and 0 bytes is how EOF is
  // spelled — a reader loops until it sees one — so the guest gets the
  // whole buffer on the first call and 0 on every call after.
  //
  // Only fd 0 exists here. Anything else is EBADF (8) rather than an
  // empty read, which a guest would take for a valid empty file.
  function fdRead(fd, iovsPtr, iovsCount, nreadPtr) {
    if (fd !== 0) return 8;
    const dv = view();
    let read = 0;
    for (let i = 0; i < iovsCount; i++) {
      const base = dv.getUint32(iovsPtr + i * 8, true);
      const len = dv.getUint32(iovsPtr + i * 8 + 4, true);
      const n = Math.min(len, stdin.length - stdinPos);
      if (n <= 0) break;
      bytesAt(base, n).set(stdin.subarray(stdinPos, stdinPos + n));
      stdinPos += n;
      read += n;
    }
    dv.setUint32(nreadPtr, read, true);
    return 0;
  }

  const preview1 = {
    fd_write: fdWrite,
    fd_read: fdRead,
    proc_exit(code) {
      throw new ExitSignal(code >>> 0);
    },
    random_get(buf, len) {
      crypto.getRandomValues(bytesAt(buf, len));
      return 0;
    },
    clock_time_get(clockId, _precision, timePtr) {
      // clockId 0 = realtime (ns since epoch), 1 = monotonic.
      const ns =
        clockId === 0
          ? BigInt(Date.now()) * 1000000n
          : BigInt(Math.round(performance.now() * 1e6));
      view().setBigUint64(timePtr, ns, true);
      return 0;
    },
    // argv comes from the caller; the guest sees exactly what it passed,
    // argv[0] included. The two calls must agree: args_sizes_get is how
    // the guest sizes the buffers args_get then writes into, so both
    // count the same NUL terminators.
    args_sizes_get(argcPtr, argvBufSizePtr) {
      const dv = view();
      dv.setUint32(argcPtr, argv.length, true);
      let bufSize = 0;
      for (const a of argv) bufSize += encoder.encode(a).length + 1;
      dv.setUint32(argvBufSizePtr, bufSize, true);
      return 0;
    },
    args_get(argvPtr, argvBufPtr) {
      const dv = view();
      let at = argvBufPtr;
      for (let i = 0; i < argv.length; i++) {
        dv.setUint32(argvPtr + i * 4, at, true);
        const b = encoder.encode(argv[i]);
        bytesAt(at, b.length).set(b);
        at += b.length;
        dv.setUint8(at, 0);
        at += 1;
      }
      return 0;
    },
    environ_sizes_get(countPtr, bufSizePtr) {
      const dv = view();
      dv.setUint32(countPtr, 0, true);
      dv.setUint32(bufSizePtr, 0, true);
      return 0;
    },
    environ_get() {
      return 0;
    },
    fd_close() {
      return 0;
    },
    sched_yield() {
      return 0;
    },
  };

  // Any preview-1 import the module declares that we haven't
  // implemented resolves to a function that traps with the missing
  // name, so the playground surfaces *which* syscall a program needs
  // instead of a cryptic LinkError at instantiate time.
  const wasiNamespace = new Proxy(preview1, {
    get(target, name) {
      if (name in target) return target[name];
      if (typeof name !== "string") return undefined;
      return () => {
        throw new Error("unsupported WASI call: wasi_snapshot_preview1." + name);
      };
    },
  });

  const { instance } = await WebAssembly.instantiate(bytes, {
    wasi_snapshot_preview1: wasiNamespace,
  });

  memory = instance.exports.memory;
  if (!memory) throw new Error("module has no exported `memory`");
  const start = instance.exports._start;
  if (typeof start !== "function") {
    throw new Error("module has no `_start` export");
  }

  try {
    start();
  } catch (e) {
    if (e instanceof ExitSignal) {
      exitCode = e.code & 0xff;
    } else {
      throw e;
    }
  }

  return {
    stdout: stdoutChunks.join(""),
    stderr: stderrChunks.join(""),
    exit: exitCode,
  };
}
