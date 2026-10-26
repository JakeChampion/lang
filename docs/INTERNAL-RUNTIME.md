# Internal runtime modules

Status: [reference] source modules, declaration selection, and checks.

The native runtime helpers live in `compiler/runtime/` as ordinary Fern source.
Each file declares one function or type.
The `common/` directory holds implementations that all native targets share.
The `linux/` directory holds implementations that both Linux targets share.
Each target directory holds its other implementations.
The `templates/` directory holds the coverage, HTTP entry, and map-key declarations that the compiler adapts.

`rtmodule.fern` loads the requested declarations with the normal lexer and parser.
It follows references to other runtime declarations.
It then selects reachable functions with the normal tree shaker.
The runtime checker and semantic lowering consume that module.
The native emitters receive that module and its lowered bodies.
Each emitter writes a shared helper once per output unit.

Source positions identify the original runtime file.
Runtime type errors report that path, line, and column.
The compiler substitutes configuration values in the AST.
It does not insert configuration values into source text.
The map-key helpers also use AST construction for operations that depend on a program's types.

`rtbundle.fern` packages the native source files for the compiler binary.
`rttemplates.fern` packages the compiler templates separately.
Wasm drivers need the templates and do not include the native source contents.
Both bundles contain generated data, not implementations to edit.
The bundles let the pinned stage0 build the compiler without a new language builtin or an external runtime directory.
The compiler source hash also covers both bundles.

To change a helper:

1. Edit its source file in `compiler/runtime/`.
2. Format the source with `fern -fmt -w`.
3. Run `python3 scripts/gen-runtime-bundle.py`.
4. Run `make runtime-check` and the relevant execution tests.

`make fmt-check` covers the source files.
The repository complexity check includes them too.
`make runtime-bundle-check` rejects a stale bundle.
`make check-sources` checks and lowers every native runtime declaration for each target through `make runtime-check`.
Coverage, HTTP, and structural map tests check the compiler templates after their AST substitutions.
