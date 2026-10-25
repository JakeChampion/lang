# Literate library checks after the main CI failure

The measured source is `b15d4e7e10ddaec58bba62c4d3d8d8c41c06e632`.
The previous PR source is `4f6f5d5b0578378f2026a12ab638b5aa84e5b840`.
This change addresses the two failures recorded in #13148.

The loader preserves the standard library path marker for a literate document.
The LSP uses that marker to refuse a rename of a library declaration.
References and user document renames retain their current behavior.
The asset test counts both `.fern` and `.fern.md` modules and checks the literate I/O module's contents.

All 44 selected native cases pass without skips.
They cover LSP references and renames, embedded library contents, literate library imports, the playground, and the complexity limit.
The published stage0 pin produces identical stages 2 and 3 at 14,449,360 bytes.
Their SHA-256 is `1d702e27210018739e3d146db95c4a573c4e81479722ae308617f71d14f34e00`.
All 11 strict driver size gates pass without baseline changes.

The fresh cast matrix passes 33 commands across both interpreters and four build targets.
All nine runtime allocation counts balance with zero live bytes.
Darwin images are built but are not executed locally.
Go vet and the format checks pass.
The Go oracle records the measured commit and a clean worktree.

The capsule records the exact source files, patch, test output, command output, and artifact hashes.
Earlier validation records retain their stated source scopes.
