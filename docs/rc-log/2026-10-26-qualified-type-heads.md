# Declared enum qualifiers and constructor names

Issue: #13642. This prerequisite blocks the Exif metadata reader in #13638.

## Failure

The reduced source declares `ByteOrder { Little, Big }`.
An unrelated enum also declares a variant named `ByteOrder`.
The source then selects `ByteOrder.Little` or `ByteOrder.Big`.
Both checkers accept the source, and the Go interpreter returns zero.
The self-host compiler refuses the expression with `unsupported projection`.

The clean before compiler uses source
`c03b8473e4a1458210d978ca2f4f581c7cba090f`.
Its CLI SHA-256 is
`13157d85d76cb5ab1790dcc06ad9575db60cd0cc4794ec6e3209e6a1ca9d50e2`.
The reduced source fails on x86-64, ARM64, and Wasm.
Removal of only the unrelated variant name permits compilation.

## Cause and change

The source classifier checked variant ownership before the declared type.
Constructor metadata also took precedence over a declared enum in the type classifier.
The classifier now resolves a declared enum before constructor metadata.
Local, parameter, and captured values retain precedence over type names.
Constructor values without a corresponding declared type retain their method behavior.

The regression also exposes two checker defects.
The ambiguity walk treated a declared enum qualifier as an unqualified value.
The walk now stops at a known type qualifier, but still examines call arguments.
Nullary enum constructors lack payload metadata, so their argument check returned early.
The check now uses the existing enum payload count when payload metadata is absent.
The Go checker remains unchanged.

## Regression controls

Eight positive cases cover nullary constructors, generic payloads, multiple variant owners, and owned record payloads.
They also cover local, parameter, and captured shadows, plus methods on constructor values.
Each case runs through the Go interpreter and all three compiled targets.
The compiled probes require nonzero, balanced heap counts and zero live blocks.

Ten negative cases retain member, argument, payload, shadow, and ambiguity diagnostics.
The Go oracle supplies the expected diagnostic code for each case.
Qualified nullary calls with extra arguments require E036.
Unqualified calls to nullary values require E038.
Ambiguous values remain errors, including arguments to qualified constructors.

The focused suite passes 74 test nodes without failures or skips.
The Exif application source remains frozen during this prerequisite.
No codec syntax change or name-specific exception substitutes for the compiler fix.
