# Unsafe syntax bootstrap

This change prepares the parser for SEC-003. It does not enforce the proposed
unsafe boundary. Raw memory and syscall builtins retain their current access
rules. The existing ownership, type, and scope checks still apply.

The Go parser and the native parser accept these contextual markers:

```fern
unsafe function answer(): i32 {
  unsafe {
    return 42;
  }
}
```

Function declarations retain an unsafe flag. Statement blocks retain a distinct
unsafe marker. The formatters preserve both markers. Public declarations,
allocation modifiers, async declarations, traits, and trait implementations can
retain the function marker. The unsafe marker comes immediately before
`function`. Nested unsafe function declarations are refused at this stage.

The name `unsafe` remains valid in identifier positions. This permits existing
functions and variables with that name to compile without a rename.

## Bootstrap sequence

The compiler implementation uses syntax that the current stage0 compiler can
read. A later bootstrap release must contain this parser before repository
sources can use the markers. Use the normal bootstrap workflow for that release.
This change does not publish a release or update the stage0 pin.

The next stage must enforce unsafe effects through calls, function values,
generics, and traits. It must classify raw builtins and annotate their callers.
It must add compile-fail tests for each prohibited access path. Until those
checks are complete, a program without unsafe markers has no new safety promise.

Ordinary integer arithmetic keeps its current wrapping and non-trapping rules.
The proposed boundary does not change integer arithmetic.

## Validation

Parser tests cover modifier retention and contextual identifiers. Formatter
round trips cover top-level declarations, traits, and implementations. Native
compiler tests reject scope escape, invalid return types, and unsupported nested
declarations. Execution controls cover x86-64, AArch64, and WASI.
