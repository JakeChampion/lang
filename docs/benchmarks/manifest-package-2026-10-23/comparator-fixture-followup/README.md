# Comparator fixture dependency

Source: `fb89d886c0dbfba47a9c3778b9b2f74445e5313b`.

The comparator fixture now copies the power cache required by its decimal parser.
All five Linux and WASI execution variants pass.
The parent check also passes.
The Darwin execution variant is skipped on Linux.
Both frontends build the Darwin image without local execution.
The parser, library, compiler, and parity assertions retain their bytes.

The earlier caller and package checks retain their recorded source scopes.
The source patch changes only the fixture dependency list.
