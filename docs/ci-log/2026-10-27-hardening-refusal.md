# Shared hardening refusal

The CLI refusal table passed `-target x86-64-linux -harden -o x.so` and expected exit status 2.
The CLI supports `-harden` for x86-64 Linux executables.
The output filename does not select shared mode.
The command therefore succeeded, and the CLI lane failed on PR #13718.

The refusal case now requests `-shared` explicitly.
The CLI rejects that unsupported combination.
The focused refusal test passes in 1.238 seconds.
The stack-canary suite passes in 1.487 seconds on x86-64 and ARM64.
Its valid images and deliberate stack-corruption controls retain their expected behavior.

The failed job is [122505773365](https://github.com/JakeChampion/lang/actions/runs/40593586623/job/122505773365).
This correction changes the test input to match the documented CLI contract.
