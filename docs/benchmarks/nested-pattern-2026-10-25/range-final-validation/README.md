# Nested constructor coverage and documentation

The source commit is `1772fdf1ef2e2a3eaa1afa31eac1313ea3479d06`.
The clean Go oracle uses source `2b75b1135dada4574e858ce8f7d1fb2312571da0`.
The source certificate verifies equal Go inputs across these commits.

A bare identifier inside a constructor pattern binds the payload.
A qualified constructor pattern selects that constructor.
The language reference explains both forms with a complete program.

The documented program has the exact bytes of its conformance fixture.

The native checker now rejects an unreachable constructor arm after a complete payload binder.
The checker retains guarded arms, partial payload coverage, and tuple shape diagnostics.
Ten focused cases cover these rules in the checker registry.

The conformance suite includes positive and negative cases.

All 2,704 Go checks pass across four packages without skips.
All 5,626 native checks pass without skips.
The official bootstrap reaches equal stage two and stage three images.

The final compiler hash is `1310eaf01ce9a28b0e42c93e92e3645d83ba417b7139b4fe6c9778fcc1807029`.
All 24 matrix commands pass across both frontends and three runtime targets.
All three runtime allocation counts balance at zero.

Both frontends build Darwin images.
Local Darwin execution is unavailable.
All 1,218 source format checks and all 11 strict size gates pass.

Go format and vet checks pass.
The feature and complexity limits remain unchanged.
