# Declaration parse positions

Issue #12834 reports different messages and positions for malformed declarations.
The self-host used a fallback at the declaration start.
The oracle reports the token where a name or parameter type must appear.

The self-host parser now records that token and the oracle message.
It skips nameless declarations after the finding, so their remaining tokens do not cause extra errors.
A receiver lookahead distinguishes a method from a function with no name.
Local functions preserve their precise declaration finding when the parser converts them into lambdas.
Trait defaults preserve signature findings even when no impl uses the default.

The old untyped-parameter fallback no longer emits a second message at the function start.
The nameless-declaration fallback remains for modules from other producers.

The differential checks code, message, and position through the CLI, formatter, and language server.
It includes several findings in one file, so a correct first finding cannot hide a later disagreement.
Existing native, Wasm, and loading-driver tests still require refusal before code emission.
