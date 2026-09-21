# 2026-09-21 — a terminal method is the free op on a handle

`coreutils/stty` is 233 declarations and produced none of them. Four refused,
and all four named the same two keys: `call target has no semantic contract:
Reader.set_window_size` and `… Reader.termios_get`.

The free forms were there — `set_window_size(fd, rows, cols)`,
`termios_get(fd)` and `termios_set(fd, when, words)` are all in the OS floor,
and so is the ops half in both backends. What was missing is the METHOD
spelling on a handle, which is what stty writes: `r.termios_get()`. Only
`Reader.window_size` of the four had a contract.

## The fix

A handle IS its descriptor, so each of these methods is the free op with the
receiver as its first operand — the arrangement `Reader.window_size` and
`isatty` already use. Three contracts beside it in
`handle_metadata_contracts`, and three arms beside it in
`ssarc.handle_metadata_site`, with the method names added to the one list
`handle_method` reads.

The termios record crosses as `i64[]` in the kernel's own words, so the
ownership follows the free forms': `termios_get` answers a fresh array the
caller owns, and `termios_set` reads its bytes and keeps none, so that array
is lent.

## Measured

x86-64, native x86-64 as the oracle.

| program | before | after |
|---|---|---|
| `coreutils/stty` | 0 of 233 | 233 of 233 |

`stty -a`, `stty size` and `stty --help` answer byte-identically on the typed
path, the AST leg and native. Under `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1` the
typed leg holds 184 bytes on `--help` where the AST leg holds 360; both hold
something, which every coreutils does at exit, so the typed path is the better
of the two and neither number is new.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 827 | 828 |
| declarations produced | 84,367 of 87,231 | 84,600 of 87,231 |

## Traps

- **The IR legs pass either way.** `TestSelfHostHandleTtyIR` runs a probe that
  uses all four methods and asserts the runtime leaves appear in the asm. It
  passed throughout: `FERN_SEM_IR` is default-on, the module refused, the AST
  lowering stood, and the program still answered 0. A test that only reads the
  answer cannot see this class of gap, which is why the new leg reads the
  production tally and requires every declaration.
- **A contract is not a lowering.** Adding the three contracts alone would have
  produced the module and then handed the backend a call to a symbol no runtime
  defines. The `handle_method` list is the second half, and the new test runs
  the binary on a real pseudo-terminal so the two halves cannot land apart.
