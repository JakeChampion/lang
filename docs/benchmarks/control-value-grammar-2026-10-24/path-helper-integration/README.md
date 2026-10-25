# Grammar dependency and source checks

The native checkpoint is `5531cdfeddf0d90a6c6faf7842aa42a2edb9e053`.
The certificate records the later source commit with census metadata and shared test fixes.
Only the trait path helper changes compiler source from the prior checkpoint.
All three compiler stages are byte-identical to that earlier build.
The comparison reads the archived first stage and both retained final stages.

The Go frontend source, standard library, specification and conformance inputs remain unchanged.
The prior matrix has 190 commands, and the diagnostic checks have 54 commands.
The certificate links those results to the identical compiler bytes and inputs.
The earlier 563 compiler cases and six-package Go sweep retain their recorded source scope.
Eleven synthetic-AST cases intentionally skip in that Go sweep.

All four current source guard cases and all 195 source lint cases pass without failures or skips.
All 11 strict driver size checks pass without baseline changes.
The changed parser passes its canonical format check.
Each new control value fixture has a measured zero in the leak census.
The separate census evidence retains twenty traced runs with both compiler stages.

The source manifest records all 497 compiler and library files.
The identity certificate records every stage hash, source scope and unchanged input category.
The earlier semantic matrix and diagnostic artifacts remain in their original proof directory.
