# Component selection report

Status: [reference] local selection checks for repository-wide CI.

These counts come from the planner at this PR checkout. The prior column uses the lane filters before this PR. Lint runs in both versions. Counts measure selected lanes, not jobs or elapsed time.

| Changed path | Prior lanes | New lanes | Unit packages | Linux consumer tests | Darwin consumer tests |
| --- | ---: | ---: | ---: | ---: | ---: |
| `docs/README.md` | 0 | 1 | 2 | 0 | 0 |
| `fern-image/packages/codecs/jpeg/header.fern` | 14 | 3 | 2 | 2 | 0 |
| `fern-image/packages/codecs/qoi/qoi.fern` | 14 | 3 | 2 | 1 | 0 |
| `coreutils/cat.fern` | 13 | 4 | 3 | 18 | 7 |
| `site/src/content.config.ts` | 13 | 2 | 2 | 0 | 0 |
| `web/driver-worker.js` | 14 | 4 | 2 | 1 | 0 |
| `editors/vscode/package.json` | 13 | 2 | 2 | 0 | 0 |
| `examples/fip/kv_fip.fern` | 13 | 2 | 5 | 50 | 0 |
| `internal/syntax/lexer/lexer_test.go` | 16 | 1 | 4 | 0 | 0 |
| `compiler/checker.fern` | 18 | 18 | 60 | 0 | 0 |
| `Makefile` | 16 | 20 | all | 0 | 0 |

A selected lane can contain several jobs. The unit lane also limits its packages, host matrix, and printer groups. The Darwin lane limits its groups and native test names. The affected-consumer lane runs on both Linux hosts.

Compiler and shared build changes retain broad checks. The new consumer lane is disabled during a full run because the original workflows already cover those tests. The daily full audit checks the complete suite.

The report does not establish a wall-clock improvement. Measure that from GitHub runs after the cache has a complete compiler artifact for each host.

To reproduce a row:

```sh
printf '%s\n' '["coreutils/cat.fern"]' | go run ./cmd/ci-plan
```
