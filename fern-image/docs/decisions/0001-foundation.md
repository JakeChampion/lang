# 0001: Isolate the Fern image foundation

Status: accepted for M0.

Keep the package under `fern-image` in the language repository.
This location preserves the compiler project and gives image contributors a small test command.
Build the compiler from the checkout with the local formatter fix.
Record the nightly probe version separately from the final compiler version.

Use native Fern byte arrays and explicit Result errors.
The current compiler supports the required scalar operations.

Use immutable image values with explicit replacement after writes.
Validate public representations on every access because public types do not enforce private constructors here.
This choice favors a small, verifiable API.

It can copy whole arrays when aliases exist.
It does not establish production memory bounds.

Use i32 dimensions and checked operators because array lengths use i32.
Reject zero dimensions and impose a 64 MiB hard limit on each storage array.
Allocate complete padded rows to keep storage length exact.

Keep future row and region APIs open to a different allocation model.
Do not add codecs, transport, or speculative optimization in M0.
