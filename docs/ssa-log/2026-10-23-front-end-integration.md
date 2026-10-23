# Front-end integration

This batch combines eight fixes that share the compiler and checker gates.
Each issue keeps its own implementation and regression commits.
One full CI run now checks their interaction.

- Local array views accept owned storage in both checkers (#13007).
- Imported enum qualifiers resolve in expressions and patterns (#12839).
- Missing handler imports and executable entry points report errors at their source locations (#12602).
- The diagnostic corpus has no remaining checker exceptions (#12889).
- Generated stdlib references have module introductions and current links (#12736, #12737).
- Generic unit payloads support Result and Option combinators (#12845).
- Indirect calls enforce their ownership contracts (#12847).

The combined tree passes the full Go checker, monomorphizer, and source checks.
It also passes the full error corpus and four checker differential suites.
All five new runnable cases pass on both interpreters and three compiled targets.
All four semantic suites, the full leak census, and the formatter corpus pass.
The feature census, Go vet, and source formatting pass.

The batch retains the main CI repairs in the original branches.
Those repairs correct parser expectations and move measured performance baselines.
They also limit builtin-name discovery to its function body.
