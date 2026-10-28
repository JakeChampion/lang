# 2026-10-28 — the graph checks run once per graph

`ssadeps.verify` and the semantic analysis. Refs #8171.

## The shape

The semantic analysis verified each SSA graph two times. The schema
check verified the graph with an empty dependency table. Then
`ssadeps.analyze` verified the same graph with the real table. Each
verify found the definitions, the reachable blocks and the dominators
again. The unit search verified the graph a third time when it changed
the dependencies. The two verifies cost about 2.2 G Ir in the
self-compile.

## What changed

`ssadeps.check_graph` does the checks that read only the graph. It keeps
the definitions, the reachable blocks and the dominators in a `Facts`
record. `ssadeps.dependencies_error` does the checks that read the
dependency table, and it uses those facts. When no value has a
dependency, it does not walk the uses again.

`ssasem.analyze` runs `check_graph` one time. `Analysis` keeps the facts,
and `ssaunits` gives them to `ssadeps.analyze_checked`. `ssadeps.verify`
and `ssadeps.analyze` keep their behaviour. `ssalive.input_error` is now
`dependency_error` followed by `graph_error`.

An input with more than one defect can now report a different defect
first. An input with one defect reports the same error.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at 903c9a490.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 105.335 G | 104.267 G (−1.01%) |

The whole-compiler binary is byte-identical to the base compiler's.
