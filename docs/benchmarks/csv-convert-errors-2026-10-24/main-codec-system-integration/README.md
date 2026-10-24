# Current codec and system integration

The source commit is `618ad7990f972c4b7f285cd28ad1a6183a49154d`.
This commit includes the current JSON, codec and system API changes.
It also includes the declaration scope fix for issue #13124.

All 742 selected compiler regression cases pass without failures or skips.
The five Go frontend packages run 2780 cases without failures.
Seven walker cases intentionally exclude synthetic AST nodes.
The optional whole-corpus formatter type-check sweep is excluded.
The other 266 formatter cases pass.

The 240-command matrix covers CSV, conversion, generic traits, JSON and collection vocabulary.
Both checkers and interpreters agree on valid programs and required diagnostics.
Four targets compile. All 60 runtime allocation counts balance with zero live bytes.
Darwin has compile checks.

Imported trait parameters shadow same-name declarations in both compilers.
The cases cover associated calls, receiver calls, default methods and nested type arguments.
Concrete module types retain their scope after a generic declaration.
Both parsers reject invalid array, tuple, function and view parent traits.

The released stage0 pin produces byte-identical stages 2 and 3.
All 11 strict driver size gates pass against the current baselines.
Go vet, Go format and all 1200 canonical Fern format checks pass.

The manifest records 496 source file hashes and the exact source commit.
The source patch passes a reverse-application check.
The archive contains all 480 matrix output logs.
Earlier diagnostic runs remain in the workspace.
One used an invalid package path. Another stopped the optional formatter sweep.
