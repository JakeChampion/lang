# Current codec integration

The source commit is `0e4dae30a8af5cad5458fbc051dcae18086878ef`.
This commit adds the current JSON constructor and codec contracts to the system API changes.
It also preserves the owner changes on main.

All 100 focused cases pass without failures or skips.
The 66-command matrix covers path, CLI, log and fuzz programs.
Both checkers and interpreters agree. Four targets compile.
All 18 runtime allocation counts balance with zero live bytes.
Darwin has compile checks.

The released stage0 pin produces byte-identical stages 2 and 3.
The final image equals the previous system API image.
All 11 strict driver size gates pass against the current baselines.
Go vet, Go format and all 1200 canonical Fern format checks pass.

The manifest records 496 source file hashes and the exact source commit.
The source patch passes a reverse-application check.
The archive contains all 132 matrix output logs.
