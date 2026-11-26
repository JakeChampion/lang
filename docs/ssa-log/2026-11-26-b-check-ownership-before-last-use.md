# Check ownership before requesting last-use facts

Part of #11534 and #8171.

An identifier that is already owned cannot take a match payload through a last use. A field of a borrowed root cannot satisfy the last-use test for an owned expression. Check those existing conditions before asking for the function’s lazy last-use index. The field case still falls through to the replacement and admitted-field checks.

A symbolized x86-64 stage-2 compiler built at `abf49f36b92c` used 57,596,567,239 instructions to compile the fixed checker with IR verification and RC certification enabled. The 6,491 calls to `lu_keys` accounted for 737,980,905 inclusive instructions. Of those, 640,410,392 went to computing last uses and 94,146,032 to resolving the function. Formatting plain last-use positions cost 6,508,819 instructions across 14,495 calls. Field-key construction includes some of those calls. On the mutable closure fixture, the three calls to `capturebox.site_key` cost 966 instructions in total.

Checking the fixed checker with a compiler containing the two reordered tests used 15,036,331,819 instructions, down from 15,095,784,067. Both compilers agreed on all 39 ownership checks, including last-use reassignment, sibling scopes, field moves, allocation balance and sanitizer checks. A separate trial that skipped statement key lookups when the literal-rewrite index was empty added 93,985 instructions to the full compile and 188,455 to checking. It was discarded.

On the same fixed checker the full compile fell from 57,596,567,239 to 57,537,110,990 instructions (59,456,249 fewer, 0.1032%). The x86-64 output was byte-identical. Across the three-target conformance corpus, all 6,258 exit statuses and diagnostics matched, with 4,458 identical emitted binaries and 1,800 refusals.

On integration base `fafaffe10b85`, each stage-2 compiler reproduced itself. With a fixed checker input, changing compilers removed 2,363 shared-array copies and 45,976 bytes copied. Changing only the checker input removed two copies and 11,000 bytes for either compiler. The branch workload measured 1,019,935 copies and 58,530,296 bytes. Both builds reported 71 unproved on the WASI checker and 5,350 refuted, 226 weakened and 227 missed on the x86-64 checker.
