# Proposal dumps

Phase 2 of a proposal (`docs/FERN-PROPOSALS.md`) is: draw a line from
`docs/proposals/random_app.txt`, build the app it inspires as a single Fern file,
and ship that file with the PR. This is where it lands, as
`examples/proposals/<name>.fern`.

These are not curated examples — `examples/cli/` and `examples/tests/` are
where the deliberately-exemplary programs live. A proposal dump is evidence: the
program that made the defect visible, kept so the next reader can see what a
user was actually trying to do when the language got in their way.

Every file here has passed all five Phase 2 legs at the time it landed:
`-check`, `-interp`, x86-64, wasm, and the self-host compiler. They are not
run by CI. If one rots, that is a finding — file it.

Keep the header comment on each file naming the drawn line and the defect the
turn produced, so a dump stays legible after its PR scrolls out of view.
