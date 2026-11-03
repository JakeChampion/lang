# Scalar match bindings

Status: implemented for issue #14099, pending validation and merge.

A bare name in a scalar match binds the scrutinee value. This rule applies to
statement matches and value matches. Scalar types include integers, floats,
booleans, characters, owned strings and string views. Arrays, tuples, structs
and enums retain their existing pattern rules.

The binding is in scope in its guard and body. A guard must return a boolean.
An unguarded binding matches every value. A guarded binding does not establish
exhaustiveness. An arm after an unguarded binding is unreachable.

The match evaluates its scrutinee once. Each arm gets a separate binding.
A false guard can change outer variables. Later arms still inspect the original
scrutinee value. Captured scalar bindings use the existing shared-cell rules.
A known constructor or struct name retains its meaning. A payload pattern such
as `name()` does not become a binding.

Both formatters preserve literal patterns, ranges, names, aliases and guards.
The `scalar_match_bindings` conformance case covers execution, capture scope
and reference cleanup. Checker controls cover invalid patterns and scope escape.
