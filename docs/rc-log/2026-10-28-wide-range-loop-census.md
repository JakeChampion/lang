# Use range loops for wide capacity tables

Issue #13733 records 5202 manual increments against the unchanged feature-census ceiling of 5200.
The wide capacity table and class lookup now use the existing integer range-loop syntax.
The capacity loop still visits indices zero through 60 in order.
The lookup still returns the first capacity that contains the input.

The provisional census now counts exactly 5200 manual increments.
The mathematical domains, generated reference files, driver assertions, legacy helpers, baselines, and thresholds retain their original bytes.
No decoder or machine integration accompanies this change.
The full retained-machine recovery and all actual M5 and epic gates remain open.

The first fresh Go test link failed for lack of temporary storage before any test ran.
The next before and after probes omit only Go test DWARF data with `-ldflags=-w`.
The test selection, assertions, source inputs, ceiling, and Fern validation flags remain unchanged.
The original failed link record remains separate from the actual census failure.
