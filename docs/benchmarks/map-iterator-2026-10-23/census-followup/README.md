# Map fixture census rows

The two new iterator fixtures each have a measured census row.
Both the compiler built by stage0 and the final stage 3 compiler run each fixture.
All four runs pair every traced allocation with its release.
The protocol fixture emits 166 trace events per run.
The nested tuple adapter emits 44 trace events per run.
Both census rows are zero, with no signal exits or unmeasured cases.

The report records compiler hashes, exact input hashes, commands and exit statuses.
All raw build and execution outputs are preserved with gzip.
The fixture bytes match the prepared Map source.
The census metadata does not change compiler or library source.
