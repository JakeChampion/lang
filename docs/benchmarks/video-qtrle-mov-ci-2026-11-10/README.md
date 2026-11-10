# QTRLE MOV integration CI measurements

CI run [42824963204](https://github.com/JakeChampion/lang/actions/runs/42824963204) measures head `b45460dd721b1641751c888d4527156cc12ff35c`.
Its base is main `5d6c692eb222454d7c4f491945f65ce47d91f8b6`.
Performance job [129860813857](https://github.com/JakeChampion/lang/actions/runs/42824963204/job/129860813857) reports the six exact values in `measurements.tsv`.

The baseline update records both increases and reductions. It changes no tolerance or metric key.
These are integration measurements after compiler and standard-library changes on main.
They do not isolate a QTRLE MOV performance effect.
Native ARM retired-instruction counts require their own CI measurements.
No physical-device power, throughput, or lip-sync measurements are available.

The same CI run exposes stale digest-generator output and integer operands in compiler test programs.
The repair reuses the generator change from PR #15128 and the test corrections from PR #15130.
It preserves digest buffer erasure and the strict integer-type rule.
It does not add the separate HTTP/2 changes from PR #15128.

Android job 129867217236 also reads an old editor record as the current editor.
Android 14 prints editor history before the current client's `mCurrentEditorInfo` section.
The Paper harness now checks that section and the Paper package name.
It still requires the text input type, Go action, keyboard behavior, and rendered home pixels.
Host controls reject stale history, missing editors, other packages, and incorrect actions.
The next live emulator run must confirm the repair.

The modern-form ratchet also reports two new findings in the HOTP mask on main.
The equivalent `bin |= window & -same` expression removes both findings, without a larger ceiling.
The MOV file indexer delegates its final metadata validation to a private helper.
This keeps each changed function within the complexity limit, without an exception.
