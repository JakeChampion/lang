# Imported enum qualifiers

`module.Enum.Variant` now names an imported enum variant in expressions and
patterns. This resolves #12839.

The Go pattern parser retains every qualifier segment. The module loader
resolves the enum as a type before it checks the variant. This also preserves
the visibility rule for private enums.

The self-host module rewrite carries the names and source prefixes of imported
enums. It applies the same prefix to each enum and its variant. A type re-export
keeps the prefix of the original declaration. Ordinary fields keep their names.
