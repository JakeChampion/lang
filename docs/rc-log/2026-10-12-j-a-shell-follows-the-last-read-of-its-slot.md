# 2026-10-12 — a shell follows the last read of its slot

`rccertify.shell_taken`; `rcmemo` version 4. Fixes #12189.

## The shape

```fern
let o: Option[str] = "hello"[1:4];
match (o) {
    Some(sl) => { if (sl.len() != 3) { return 16; } },
    None => { return 17; }
}
match (o) {
    Some(s2) => { if (s2.len() != 3) { return 18; } },
    None => { return 19; }
}
```

The second match takes the payload: it reads the slot and then releases
the Option's box alone, as a shell. The first match's read of the same slot
only borrows, because the Option lives on to the second match.

## The rule

The certifier counted a load as the frame's own unit when it read a box
the frame releases as a shell after it. Both reads come before the release,
so both were counted, and the first was reported as leaked wherever it
died. The default-on certifier refused the program, and also
`conformance/cases/audit_strings_arrays_maps` once semsink re-read its
payload.

A load is now the shell's payload only when no other load of the same slot
sits between it and the release. That later load is the one the release
follows. The compiled code was right all along: the leak census and the
sanitizer were clean on every path.

The memo's verdicts were made by the old model, so its version moves to 4.

## Measured

Two new cases in `TestSelfHostRcCertifyCases`:
- `shell-slot-read-twice` certifies. Without the change it was "leaked at
  return of v1 (child ld_at of a shell)".
- `shell-payload-kept` still names its one read as leaked.

`TestX86_64OptionViewRematch`, with arm64 and wasm legs, compiles the
program above through each of its early returns, census balanced. The
compiler certifies itself clean with the memo off.
