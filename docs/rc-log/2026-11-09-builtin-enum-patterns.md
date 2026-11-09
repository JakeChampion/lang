# Qualified builtin enum patterns in the Go oracle

The module loader treated every qualified pattern as a local enum or an imported module.
It rejected builtin enum qualifiers such as `IoError` with E087.
The self-hosted compiler accepts these valid patterns.
A reduced nested error pattern fails with the prior Go oracle and passes with the fix.

The loader now preserves builtin enum qualifiers when no imported module matches.
Local enums and imported modules keep their prior resolution order.
The checker still validates the variant name and its scrutinee type.

The tests derive valid controls from every builtin enum declaration.
They cover direct and nested patterns for all five builtin enums.
Invalid controls retain unknown-module, unknown-variant, and wrong-enum diagnostics.
The complete checker and module-loader test packages pass.

Issue: #14783. Parent: #14509. Source tests: #14778.
