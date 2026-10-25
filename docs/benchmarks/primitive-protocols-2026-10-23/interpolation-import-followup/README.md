# Interpolation and array import follow-up

Primitive interpolation needs no import.
The diagnostic corpus removes the obsolete negative interpolation case.
Both generic array fixtures now import `std/array` directly.
They previously reached that module through the old float formatter.

All 55 diagnostic corpus checks pass without skips.
All 1,088 IR corpus checks pass without skips.
The two array programs pass builds and execution on three runtimes.
All six runtime allocation counts balance.

The compiler and library inputs equal source `90ada84f0329462cd1bd55c586c157a68a2dea22`.
The retained compiler and Go oracle therefore keep their original source scope.
The source certificate excludes only the updated diagnostic test file from the internal tree.
The earlier capsule retains the full feature checks.
