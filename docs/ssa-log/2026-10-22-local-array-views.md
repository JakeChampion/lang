# Local array view initialization

The Go checker now accepts an owned array as the initializer of a local view.
The self-host checker also accepts named and temporary owners. It previously
accepted only array literals at this position. This resolves #13007.

The checker converts the initializer to a full-range slice. This gives the
runtime the view representation and preserves the storage lifetime. Element
types must match exactly. The conformance case covers literals, named owners,
temporary owners, empty arrays, generic elements, and copy-on-write updates.
The escape checks reject a return of a view into storage from the same frame.
