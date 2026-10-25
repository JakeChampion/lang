# TAP test results

This module runs tests and reports TAP results.

## Model

A test runner records outcomes and emits TAP reports. Filters select cases, while fail-fast policy stops further work after a failure.

## Contracts

A failed assertion retains diagnostic context. Test results and process exit behavior remain separate so a suite can report all selected outcomes.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct TestRunner>>

<<4 pub function test_new(suite: string): TestRunner>>

<<5 pub function (r: TestRunner) subsuite(name: string): TestRunner>>

<<6 function __append_all(dst: string[], src: string[]): string[]>>

<<7 pub function (r: TestRunner) merge(child: TestRunner): TestRunner>>

<<8 function __quoted(s: string): string>>

<<9 pub function assert_eq[T: cmp.Eq + cmp.Display](actual: T, expected: T): TestOutcome>>

<<10 pub function assert_is_nan_f32(v: f32): TestOutcome>>

<<11 pub function assert_unique[T: cmp.Eq + cmp.Display](arr: T[]): TestOutcome>>

<<12 pub function assert_array_contains[T: cmp.Eq + cmp.Display](arr: T[], needle: T): TestOutcome>>

<<13 pub function assert_eq_f32_array_near(actual: f32[], expected: f32[], epsilon: f32): TestOutcome>>

<<14 pub function assert_contains_all(haystack: string, needles: string[]): TestOutcome>>

<<15 function __diff_lines(a: string, b: string): i32>>

<<16 pub function assert_eq_string_diff(actual: string, expected: string): TestOutcome>>

<<17 pub function assert_file_not_exists(path: string): TestOutcome>>

<<18 pub function assert_file_contains(path: string, needle: string): TestOutcome>>

<<19 function __bench_sort_us(samples: i64[]): i64[]>>

<<20 pub function (r: TestRunner) bench(name: string, iterations: i32, fn: ()>>

<<21 function __json_value_eq(a: json.JsonValue, b: json.JsonValue): boolean>>

<<22 pub function assert_matches_golden(path: string, actual: string): TestOutcome>>

<<23 pub function assert_eq_map[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](actual: Map[K, V], expected: M...>>

<<24 pub function assert_one_of_i32(actual: i32, allowed: i32[]): TestOutcome>>

<<25 pub function assert_is_some_eq_string(opt: Option[string], expected: string): TestOutcome>>

<<26 pub function assert_array_intersects_string(a: string[], b: string[]): TestOutcome>>

<<27 function __match_subseq_i32(arr: i32[], needle: i32[]): boolean>>

<<28 pub function assert_array_contains_subseq_i32(arr: i32[], needle: i32[]): TestOutcome>>

<<29 function __match_subseq_string(arr: string[], needle: string[]): boolean>>

<<30 pub function assert_array_contains_subseq_string(arr: string[], needle: string[]): TestOutcome>>

<<31 pub function assert_json_has_key(json_text: string, key: string): TestOutcome>>

<<32 pub function assert_json_object_size(json_text: string, n: i32): TestOutcome>>

<<33 pub function assert_json_eq_field_i32(json_text: string, key: string, expected: i32): TestOutcome>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs tests and reports TAP results.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i32";
import "core/map";
import "core/cmp";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/f64";
import "std/array";
import "std/string";
import "std/headers";
import "std/http";
import "std/json";
```

### `3 pub struct TestRunner`

```fern
<<3 pub struct TestRunner>>=
pub struct TestRunner {
  suite: string,
  passed: i32,
  failed: i32,
  skipped: i32,
  failures: string[],
  skip_reasons: string[],
  verbose: boolean,
  prefix: string,
  emitted: Cell[i32],
  emitted_failed: Cell[i32],
  cleanup_paths: string[],
  filter: string,
  fail_fast: boolean,
  quiet: boolean,
}

pub enum TestOutcome {
  Pass,
  Fail(string),
}
```

### `4 pub function test_new(suite: string): TestRunner`

```fern
<<4 pub function test_new(suite: string): TestRunner>>=
pub function test_new(suite: string): TestRunner {
  print("TAP version 13");
  print("# Suite: " + suite);
  return TestRunner {
    suite: suite,
    passed: 0,
    failed: 0,
    skipped: 0,
    failures: [],
    skip_reasons: [],
    verbose: false,
    prefix: "",
    emitted: cell_new(0),
    emitted_failed: cell_new(0),
    cleanup_paths: [],
    filter: "",
    fail_fast: false,
    quiet: false,
  };
}

pub function test_new_filtered(suite: string, filter: string): TestRunner {
  let r: TestRunner = test_new(suite);
  return TestRunner { ...r, filter: filter };
}

pub function parse_filter_from_args(argv: string[]): string {
  let i: i32 = 0;
  while (i < argv.len()) {
    let a: string = argv[i];
    if (a == "--filter" && i + 1 < argv.len()) {
      return argv[i + 1];
    }
    if (a.starts_with("--filter=")) {
      return slice_unchecked(a, 9, a.len()).to_owned();
    }
    i = i + 1;
  }
  return "";
}

pub function test_new_fail_fast(suite: string): TestRunner {
  let r: TestRunner = test_new(suite);
  return r.with_fail_fast();
}

pub function (r: TestRunner) with_fail_fast(): TestRunner {
  return TestRunner { ...r, fail_fast: true };
}

pub function parse_fail_fast_from_args(argv: string[]): boolean {
  let i: i32 = 0;
  while (i < argv.len()) {
    if (argv[i] == "--fail-fast") {
      return true;
    }
    i = i + 1;
  }
  return false;
}

pub function test_new_quiet(suite: string): TestRunner {
  let r: TestRunner = test_new(suite);
  return r.with_quiet();
}

pub function (r: TestRunner) with_quiet(): TestRunner {
  return TestRunner { ...r, quiet: true };
}

pub function parse_quiet_from_args(argv: string[]): boolean {
  let i: i32 = 0;
  while (i < argv.len()) {
    if (argv[i] == "--quiet") {
      return true;
    }
    i = i + 1;
  }
  return false;
}

pub function test_new_verbose(suite: string): TestRunner {
  let r: TestRunner = test_new(suite);
  return TestRunner { ...r, verbose: true };
}

pub function (r: TestRunner) it(name: string, body: () => TestOutcome): TestRunner {
  let display: string = r.prefix + name;
  if (r.filter.len() > 0 && !display.contains(r.filter)) {
    return r.skip(name, "filtered out (--filter=" + r.filter + ")");
  }
  if (r.fail_fast && r.failed > 0) {
    return r.skip(name, "fail-fast: prior case failed");
  }
  let idx: i32 = r.emitted.get() + 1;
  r.emitted.set(idx);
  match (body()) {
    Pass => {
      if (!r.quiet) {
        print("ok " + idx.to_string() + " - " + display);
      }
      return TestRunner { ...r, passed: r.passed + 1 };
    },
    Fail(msg) => {
      r.emitted_failed.set(r.emitted_failed.get() + 1);
      print("not ok " + idx.to_string() + " - " + display);
      print("  ---");
      print("  message: " + msg);
      print("  ---");
      let fs: string[] = r.failures.append(display + ": " + msg);
      return TestRunner { ...r, failed: r.failed + 1, failures: fs };
    }
  }
}

pub function (r: TestRunner) skip(name: string, reason: string): TestRunner {
  let idx: i32 = r.emitted.get() + 1;
  r.emitted.set(idx);
  let display: string = r.prefix + name;
  if (!r.quiet) {
    print("ok " + idx.to_string() + " - " + display + " # SKIP " + reason);
  }
  let sr: string[] = r.skip_reasons.append(display + ": " + reason);
  return TestRunner { ...r, skipped: r.skipped + 1, skip_reasons: sr };
}

pub function (r: TestRunner) skip_if(cond: boolean, name: string, reason: string, body: () => TestOutcome): TestRunner {
  if (cond) {
    return r.skip(name, reason);
  }
  return r.it(name, body);
}
```

### `5 pub function (r: TestRunner) subsuite(name: string): TestRunner`

```fern
<<5 pub function (r: TestRunner) subsuite(name: string): TestRunner>>=
pub function (r: TestRunner) subsuite(name: string): TestRunner {
  return TestRunner {
    ...r,
    passed: 0,
    failed: 0,
    skipped: 0,
    failures: [],
    skip_reasons: [],
    prefix: r.prefix + name + " / ",
    cleanup_paths: [],
  };
}
```

### `6 function __append_all(dst: string[], src: string[]): string[]`

```fern
<<6 function __append_all(dst: string[], src: string[]): string[]>>=
function __append_all(dst: string[], src: string[]): string[] {
  let out: string[] = dst;
  let i: i32 = 0;
  while (i < src.len()) {
    out = out.append(src[i]);
    i = i + 1;
  }
  return out;
}
```

### `7 pub function (r: TestRunner) merge(child: TestRunner): TestRunner`

```fern
<<7 pub function (r: TestRunner) merge(child: TestRunner): TestRunner>>=
pub function (r: TestRunner) merge(child: TestRunner): TestRunner {
  let fs: string[] = __append_all(r.failures, child.failures);
  let sr: string[] = __append_all(r.skip_reasons, child.skip_reasons);
  let cl: string[] = __append_all(r.cleanup_paths, child.cleanup_paths);
  return TestRunner {
    ...r,
    passed: r.passed + child.passed,
    failed: r.failed + child.failed,
    skipped: r.skipped + child.skipped,
    failures: fs,
    skip_reasons: sr,
    cleanup_paths: cl,
  };
}

pub function (r: TestRunner) defer_cleanup(path: string): TestRunner {
  let cl: string[] = r.cleanup_paths.append(path);
  return TestRunner { ...r, cleanup_paths: cl };
}

pub function (r: TestRunner) log(msg: string): TestRunner {
  print("# " + msg);
  return r;
}

pub function (r: TestRunner) log_kv_string(key: string, value: string): TestRunner {
  print("# " + key + "=\"" + value + "\"");
  return r;
}

pub function (r: TestRunner) log_kv_i32(key: string, value: i32): TestRunner {
  print("# " + key + "=" + value.to_string());
  return r;
}

pub function (r: TestRunner) log_kv_i64(key: string, value: i64): TestRunner {
  print("# " + key + "=" + value.to_string());
  return r;
}

pub function (r: TestRunner) finish(): i32 {
  let accounted: i32 = r.passed + r.failed + r.skipped;
  let total: i32 = r.emitted.get();
  if (accounted > total) {
    total = accounted;
  }
  let failed: i32 = r.failed;
  if (r.emitted_failed.get() > failed) {
    failed = r.emitted_failed.get();
  }
  let dropped: i32 = total - accounted;
  print("1.." + total.to_string());
  print("# tests " + total.to_string());
  print("# pass " + (total - failed - r.skipped).to_string());
  print("# fail " + failed.to_string());
  if (dropped > 0) {
    print("# WARNING: " + dropped.to_string() + " result(s) were discarded — a TestRunner returned by it() / skip() / merge() was not assigned back to the runner variable");
  }
  if (r.skipped > 0) {
    print("# skip " + r.skipped.to_string());
  }
  if (r.skipped > 0) {
    print("# skipped:");
    let j: i32 = 0;
    while (j < r.skip_reasons.len()) {
      print("#   " + r.skip_reasons[j]);
      j = j + 1;
    }
  }
  let k: i32 = 0;
  let cleanup_errored: boolean = false;
  while (k < r.cleanup_paths.len()) {
    let p: string = r.cleanup_paths[k];
    match (remove_dir_all(p)) {
      Ok(_) => {},
      Err(_) => {
        print("# cleanup failed for " + p);
        cleanup_errored = true;
      }
    }
    k = k + 1;
  }
  if (failed > 0) {
    print("# failures:");
    let i: i32 = 0;
    while (i < r.failures.len()) {
      print("#   " + r.failures[i]);
      i = i + 1;
    }
    return 1;
  }
  if (dropped > 0) {
    return 1;
  }
  if (cleanup_errored) {
    return 2;
  }
  return 0;
}

pub function pass(): TestOutcome {
  return Pass;
}

pub function fail(msg: string): TestOutcome {
  return Fail(msg);
}

pub function unreachable(label: string): TestOutcome {
  return Fail("unreachable: " + label);
}

pub function assert_true(cond: boolean): TestOutcome {
  if (cond) {
    return Pass;
  }
  return Fail("assert_true: condition was false");
}

pub function assert_false(cond: boolean): TestOutcome {
  if (!cond) {
    return Pass;
  }
  return Fail("assert_false: condition was true");
}
```

### `8 function __quoted(s: string): string`

```fern
<<8 function __quoted(s: string): string>>=
function __quoted(s: string): string {
  return "\"" + s + "\"";
}
```

### `9 pub function assert_eq[T: cmp.Eq + cmp.Display](actual: T, expected: T): TestOutcome`

```fern
<<9 pub function assert_eq[T: cmp.Eq + cmp.Display](actual: T, expected: T): TestOutcome>>=
pub function assert_eq[T: cmp.Eq + cmp.Display](actual: T, expected: T): TestOutcome {
  if (actual.eq(expected)) {
    return Pass;
  }
  return Fail("assert_eq: expected " + __quoted(expected.to_string())
    + ", got " + __quoted(actual.to_string()));
}

pub function assert_neq[T: cmp.Eq + cmp.Display](actual: T, expected: T): TestOutcome {
  if (!actual.eq(expected)) {
    return Pass;
  }
  return Fail("assert_neq: expected != " + __quoted(expected.to_string())
    + ", got " + __quoted(actual.to_string()));
}

pub function assert_lt[T: cmp.Ord + cmp.Display](a: T, b: T): TestOutcome {
  if (a.cmp(b) < 0) {
    return Pass;
  }
  return Fail("assert_lt: expected " + __quoted(a.to_string())
    + " < " + __quoted(b.to_string()));
}

pub function assert_le[T: cmp.Ord + cmp.Display](a: T, b: T): TestOutcome {
  if (a.cmp(b) <= 0) {
    return Pass;
  }
  return Fail("assert_le: expected " + __quoted(a.to_string())
    + " <= " + __quoted(b.to_string()));
}

pub function assert_gt[T: cmp.Ord + cmp.Display](a: T, b: T): TestOutcome {
  if (a.cmp(b) > 0) {
    return Pass;
  }
  return Fail("assert_gt: expected " + __quoted(a.to_string())
    + " > " + __quoted(b.to_string()));
}

pub function assert_ge[T: cmp.Ord + cmp.Display](a: T, b: T): TestOutcome {
  if (a.cmp(b) >= 0) {
    return Pass;
  }
  return Fail("assert_ge: expected " + __quoted(a.to_string())
    + " >= " + __quoted(b.to_string()));
}

pub function assert_eq_f64_near(actual: f64, expected: f64, epsilon: f64): TestOutcome {
  if (actual != actual) {
    return Fail("assert_eq_f64_near: actual is NaN");
  }
  if (expected != expected) {
    return Fail("assert_eq_f64_near: expected is NaN");
  }
  if (actual == expected) {
    return Pass;
  }
  let diff: f64 = actual - expected;
  if (diff < 0.0) {
    diff = 0.0 - diff;
  }
  if (diff <= epsilon) {
    return Pass;
  }
  return Fail("assert_eq_f64_near: |" + actual.to_string()
    + " - " + expected.to_string() + "| = " + diff.to_string()
    + " > epsilon " + epsilon.to_string());
}

pub function assert_eq_f32_near(actual: f32, expected: f32, epsilon: f32): TestOutcome {
  return assert_eq_f64_near(actual as f64, expected as f64, epsilon as f64);
}

pub function assert_eq_f64_rel(actual: f64, expected: f64, rel_tol: f64): TestOutcome {
  if (actual != actual) {
    return Fail("assert_eq_f64_rel: actual is NaN");
  }
  if (expected != expected) {
    return Fail("assert_eq_f64_rel: expected is NaN");
  }
  if (actual == expected) {
    return Pass;
  }
  let diff: f64 = actual - expected;
  if (diff < 0.0) {
    diff = 0.0 - diff;
  }
  let denom: f64 = expected;
  if (denom < 0.0) {
    denom = 0.0 - denom;
  }
  if (denom == 0.0) {
    if (diff <= rel_tol) {
      return Pass;
    }
    return Fail("assert_eq_f64_rel: |" + actual.to_string()
      + " - 0| = " + diff.to_string()
      + " > rel_tol " + rel_tol.to_string());
  }
  let ratio: f64 = diff / denom;
  if (ratio <= rel_tol) {
    return Pass;
  }
  return Fail("assert_eq_f64_rel: |" + actual.to_string()
    + " - " + expected.to_string() + "| / |" + expected.to_string()
    + "| = " + ratio.to_string()
    + " > rel_tol " + rel_tol.to_string());
}

pub function assert_eq_f32_rel(actual: f32, expected: f32, rel_tol: f32): TestOutcome {
  return assert_eq_f64_rel(actual as f64, expected as f64, rel_tol as f64);
}

pub function assert_eq_f64_exact(actual: f64, expected: f64): TestOutcome {
  if (actual == expected) {
    return Pass;
  }
  if (actual != actual || expected != expected) {
    return Fail("assert_eq_f64_exact: NaN involved (actual="
      + actual.to_string() + ", expected=" + expected.to_string() + ")");
  }
  return Fail("assert_eq_f64_exact: expected " + expected.to_string()
    + ", got " + actual.to_string());
}

pub function assert_is_nan_f64(v: f64): TestOutcome {
  if (v != v) {
    return Pass;
  }
  return Fail("assert_is_nan_f64: expected NaN, got " + v.to_string());
}
```

### `10 pub function assert_is_nan_f32(v: f32): TestOutcome`

```fern
<<10 pub function assert_is_nan_f32(v: f32): TestOutcome>>=
pub function assert_is_nan_f32(v: f32): TestOutcome {
  if (v != v) {
    return Pass;
  }
  return Fail("assert_is_nan_f32: expected NaN, got " + v.to_string());
}

pub function assert_contains(haystack: string, needle: string): TestOutcome {
  if (haystack.contains(needle)) {
    return Pass;
  }
  return Fail("assert_contains: \"" + haystack + "\" does not contain \"" + needle + "\"");
}

pub function assert_not_contains(haystack: string, needle: string): TestOutcome {
  if (!haystack.contains(needle)) {
    return Pass;
  }
  return Fail("assert_not_contains: \"" + haystack + "\" contains \"" + needle + "\"");
}

pub function assert_starts_with(s: string, prefix: string): TestOutcome {
  if (s.starts_with(prefix)) {
    return Pass;
  }
  return Fail("assert_starts_with: \"" + s + "\" does not start with \"" + prefix + "\"");
}

pub function assert_ends_with(s: string, suffix: string): TestOutcome {
  if (s.ends_with(suffix)) {
    return Pass;
  }
  return Fail("assert_ends_with: \"" + s + "\" does not end with \"" + suffix + "\"");
}

pub function assert_eq_string_ci(actual: string, expected: string): TestOutcome {
  if (actual.eq_ignore_ascii_case(expected)) {
    return Pass;
  }
  return Fail("assert_eq_string_ci: expected \"" + expected
    + "\" (case-insensitive), got \"" + actual + "\"");
}

pub function assert_neq_string_ci(actual: string, expected: string): TestOutcome {
  if (!actual.eq_ignore_ascii_case(expected)) {
    return Pass;
  }
  return Fail("assert_neq_string_ci: \"" + actual
    + "\" matches \"" + expected + "\" (case-insensitive)");
}

pub function assert_contains_ci(haystack: string, needle: string): TestOutcome {
  if (haystack.contains_ci(needle)) {
    return Pass;
  }
  return Fail("assert_contains_ci: \"" + haystack
    + "\" does not contain \"" + needle + "\" (case-insensitive)");
}

pub function assert_starts_with_ci(s: string, prefix: string): TestOutcome {
  if (s.starts_with_ci(prefix)) {
    return Pass;
  }
  return Fail("assert_starts_with_ci: \"" + s
    + "\" does not start with \"" + prefix + "\" (case-insensitive)");
}

pub function assert_ends_with_ci(s: string, suffix: string): TestOutcome {
  if (s.ends_with_ci(suffix)) {
    return Pass;
  }
  return Fail("assert_ends_with_ci: \"" + s
    + "\" does not end with \"" + suffix + "\" (case-insensitive)");
}

pub function assert_empty_string(s: string): TestOutcome {
  if (s.len() == 0) {
    return Pass;
  }
  return Fail("assert_empty_string: expected \"\", got \"" + s + "\"");
}

pub function assert_non_empty_string(s: string): TestOutcome {
  if (s.len() > 0) {
    return Pass;
  }
  return Fail("assert_non_empty_string: string was empty");
}

pub function assert_string_count(haystack: string, needle: string, n: i32): TestOutcome {
  let got: i32 = haystack.count(needle);
  if (got == n) {
    return Pass;
  }
  return Fail("assert_string_count: \"" + needle
    + "\" appears " + got.to_string()
    + " times, expected " + n.to_string());
}

pub function assert_sorted_asc[T: cmp.Ord + cmp.Display](arr: T[]): TestOutcome {
  let i: i32 = 1;
  while (i < arr.len()) {
    if (arr[i - 1].cmp(arr[i]) > 0) {
      return Fail("assert_sorted_asc: inversion at index " + i.to_string()
        + ": " + arr[i - 1].to_string() + " > " + arr[i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_sorted_desc[T: cmp.Ord + cmp.Display](arr: T[]): TestOutcome {
  let i: i32 = 1;
  while (i < arr.len()) {
    if (arr[i - 1].cmp(arr[i]) < 0) {
      return Fail("assert_sorted_desc: inversion at index " + i.to_string()
        + ": " + arr[i - 1].to_string() + " < " + arr[i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_strictly_sorted_asc[T: cmp.Ord + cmp.Display](arr: T[]): TestOutcome {
  let i: i32 = 1;
  while (i < arr.len()) {
    if (arr[i - 1].cmp(arr[i]) >= 0) {
      return Fail("assert_strictly_sorted_asc: not strict at index " + i.to_string()
        + ": " + arr[i - 1].to_string() + " >= " + arr[i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}
```

### `11 pub function assert_unique[T: cmp.Eq + cmp.Display](arr: T[]): TestOutcome`

```fern
<<11 pub function assert_unique[T: cmp.Eq + cmp.Display](arr: T[]): TestOutcome>>=
pub function assert_unique[T: cmp.Eq + cmp.Display](arr: T[]): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    let j: i32 = i + 1;
    while (j < arr.len()) {
      if (arr[i].eq(arr[j])) {
        return Fail("assert_unique: duplicate value " + arr[i].to_string());
      }
      j = j + 1;
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_subset[T: cmp.Eq + cmp.Display](superset: T[], subset: T[]): TestOutcome {
  let used: boolean[] = [];
  let sn: i32 = superset.len();
  let i: i32 = 0;
  while (i < sn) {
    used = used.append(false);
    i = i + 1;
  }
  let j: i32 = 0;
  while (j < subset.len()) {
    let want: T = subset[j];
    let matched: boolean = false;
    let k: i32 = 0;
    while (k < sn && !matched) {
      if (!used[k] && superset[k].eq(want)) {
        used = used.with(k, true);
        matched = true;
      }
      k = k + 1;
    }
    if (!matched) {
      return Fail("assert_subset: " + want.to_string() + " not in superset (with required multiplicity)");
    }
    j = j + 1;
  }
  return Pass;
}

pub function assert_set_eq[T: cmp.Eq + cmp.Display](actual: T[], expected: T[]): TestOutcome {
  let an: i32 = actual.len();
  let en: i32 = expected.len();
  if (an != en) {
    return Fail("assert_set_eq: length differs - expected "
      + en.to_string() + ", got " + an.to_string());
  }
  let used: boolean[] = [];
  let i: i32 = 0;
  while (i < en) {
    used = used.append(false);
    i = i + 1;
  }
  let j: i32 = 0;
  while (j < an) {
    let want: T = actual[j];
    let matched: boolean = false;
    let k: i32 = 0;
    while (k < en && !matched) {
      if (!used[k] && expected[k].eq(want)) {
        used = used.with(k, true);
        matched = true;
      }
      k = k + 1;
    }
    if (!matched) {
      return Fail("assert_set_eq: multiset differs - extra " + want.to_string());
    }
    j = j + 1;
  }
  return Pass;
}

pub function assert_len_i32(arr: i32[], n: i32): TestOutcome {
  let got: i32 = arr.len();
  if (got == n) {
    return Pass;
  }
  return Fail("assert_len_i32: expected length " + n.to_string()
    + ", got " + got.to_string());
}

pub function assert_len_string(arr: string[], n: i32): TestOutcome {
  let got: i32 = arr.len();
  if (got == n) {
    return Pass;
  }
  return Fail("assert_len_string: expected length " + n.to_string()
    + ", got " + got.to_string());
}

pub function assert_eq_array[T: cmp.Eq + cmp.Display](actual: T[], expected: T[]): TestOutcome {
  let an: i32 = actual.len();
  let en: i32 = expected.len();
  if (an != en) {
    return Fail("assert_eq_array: length differs - expected "
      + en.to_string() + ", got " + an.to_string());
  }
  let i: i32 = 0;
  while (i < an) {
    if (!actual[i].eq(expected[i])) {
      return Fail("assert_eq_array: at index " + i.to_string()
        + " expected " + expected[i].to_string()
        + ", got " + actual[i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_at[T: cmp.Eq + cmp.Display](arr: T[], idx: i32, expected: T): TestOutcome {
  let n: i32 = arr.len();
  if (idx < 0 || idx >= n) {
    return Fail("assert_at: index " + idx.to_string()
      + " out of bounds (length " + n.to_string() + ")");
  }
  if (arr[idx].eq(expected)) {
    return Pass;
  }
  return Fail("assert_at: at index " + idx.to_string()
    + " expected " + expected.to_string()
    + ", got " + arr[idx].to_string());
}
```

### `12 pub function assert_array_contains[T: cmp.Eq + cmp.Display](arr: T[], needle: T): TestOutcome`

```fern
<<12 pub function assert_array_contains[T: cmp.Eq + cmp.Display](arr: T[], needle: T): TestOutcome>>=
pub function assert_array_contains[T: cmp.Eq + cmp.Display](arr: T[], needle: T): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (arr[i].eq(needle)) {
      return Pass;
    }
    i = i + 1;
  }
  return Fail("assert_array_contains: "
    + needle.to_string() + " not found in array of length "
    + arr.len().to_string());
}

pub function assert_array_not_contains[T: cmp.Eq + cmp.Display](arr: T[], needle: T): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (arr[i].eq(needle)) {
      return Fail("assert_array_not_contains: "
        + needle.to_string()
        + " unexpectedly present at index " + i.to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_at_f64(arr: f64[], idx: i32, expected: f64, epsilon: f64): TestOutcome {
  let n: i32 = arr.len();
  if (idx < 0 || idx >= n) {
    return Fail("assert_at_f64: index " + idx.to_string()
      + " out of bounds (length " + n.to_string() + ")");
  }
  let a: f64 = arr[idx];
  if (a != a) {
    return Fail("assert_at_f64: at index " + idx.to_string()
      + " actual is NaN");
  }
  if (expected != expected) {
    return Fail("assert_at_f64: at index " + idx.to_string()
      + " expected is NaN");
  }
  if (a == expected) {
    return Pass;
  }
  let diff: f64 = a - expected;
  if (diff < 0.0) {
    diff = 0.0 - diff;
  }
  if (diff <= epsilon) {
    return Pass;
  }
  return Fail("assert_at_f64: at index " + idx.to_string()
    + " |" + a.to_string() + " - " + expected.to_string()
    + "| = " + diff.to_string()
    + " > epsilon " + epsilon.to_string());
}

pub function assert_at_f32(arr: f32[], idx: i32, expected: f32, epsilon: f32): TestOutcome {
  let n: i32 = arr.len();
  if (idx < 0 || idx >= n) {
    return Fail("assert_at_f32: index " + idx.to_string()
      + " out of bounds (length " + n.to_string() + ")");
  }
  let a: f32 = arr[idx];
  if (a != a) {
    return Fail("assert_at_f32: at index " + idx.to_string()
      + " actual is NaN");
  }
  if (expected != expected) {
    return Fail("assert_at_f32: at index " + idx.to_string()
      + " expected is NaN");
  }
  if (a == expected) {
    return Pass;
  }
  let diff: f64 = a as f64 - expected as f64;
  if (diff < 0.0) {
    diff = 0.0 - diff;
  }
  if (diff <= epsilon as f64) {
    return Pass;
  }
  return Fail("assert_at_f32: at index " + idx.to_string()
    + " |" + a.to_string() + " - " + expected.to_string()
    + "| > epsilon " + epsilon.to_string());
}

pub function assert_eq_f64_array_near(actual: f64[], expected: f64[], epsilon: f64): TestOutcome {
  let an: i32 = actual.len();
  let en: i32 = expected.len();
  if (an != en) {
    return Fail("assert_eq_f64_array_near: length differs - expected "
      + en.to_string() + ", got " + an.to_string());
  }
  let i: i32 = 0;
  while (i < an) {
    let a: f64 = actual[i];
    let e: f64 = expected[i];
    if (a != a || e != e) {
      return Fail("assert_eq_f64_array_near: NaN at index "
        + i.to_string());
    }
    if (a != e) {
      let diff: f64 = a - e;
      if (diff < 0.0) {
        diff = 0.0 - diff;
      }
      if (diff > epsilon) {
        return Fail("assert_eq_f64_array_near: at index "
          + i.to_string() + " |" + a.to_string()
          + " - " + e.to_string() + "| = " + diff.to_string()
          + " > epsilon " + epsilon.to_string());
      }
    }
    i = i + 1;
  }
  return Pass;
}
```

### `13 pub function assert_eq_f32_array_near(actual: f32[], expected: f32[], epsilon: f32): TestOutcome`

```fern
<<13 pub function assert_eq_f32_array_near(actual: f32[], expected: f32[], epsilon: f32): TestOutcome>>=
pub function assert_eq_f32_array_near(actual: f32[], expected: f32[], epsilon: f32): TestOutcome {
  let an: i32 = actual.len();
  let en: i32 = expected.len();
  if (an != en) {
    return Fail("assert_eq_f32_array_near: length differs - expected "
      + en.to_string() + ", got " + an.to_string());
  }
  let i: i32 = 0;
  while (i < an) {
    let a: f32 = actual[i];
    let e: f32 = expected[i];
    if (a != a || e != e) {
      return Fail("assert_eq_f32_array_near: NaN at index "
        + i.to_string());
    }
    if (a != e) {
      let diff: f64 = a as f64 - e as f64;
      if (diff < 0.0) {
        diff = 0.0 - diff;
      }
      if (diff > epsilon as f64) {
        return Fail("assert_eq_f32_array_near: at index "
          + i.to_string() + " |" + a.to_string()
          + " - " + e.to_string() + "| > epsilon "
          + epsilon.to_string());
      }
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_exit(actual: ProcessResult, expected: i32): TestOutcome {
  if (actual.exit_code == expected) {
    return Pass;
  }
  let tail: string = "";
  if (actual.stdout.len() > 0) {
    tail = tail + "\n  stdout: " + actual.stdout;
  }
  if (actual.stderr.len() > 0) {
    tail = tail + "\n  stderr: " + actual.stderr;
  }
  return Fail("assert_exit: expected " + expected.to_string()
    + ", got " + actual.exit_code.to_string() + tail);
}

pub function assert_stdout_eq(actual: ProcessResult, expected: string): TestOutcome {
  if (actual.stdout == expected) {
    return Pass;
  }
  return Fail("assert_stdout_eq: expected \"" + expected
    + "\", got \"" + actual.stdout + "\"");
}

pub function assert_stderr_eq(actual: ProcessResult, expected: string): TestOutcome {
  if (actual.stderr == expected) {
    return Pass;
  }
  return Fail("assert_stderr_eq: expected \"" + expected
    + "\", got \"" + actual.stderr + "\"");
}

pub function assert_stdout_contains(actual: ProcessResult, needle: string): TestOutcome {
  if (actual.stdout.contains(needle)) {
    return Pass;
  }
  return Fail("assert_stdout_contains: stdout does not contain \""
    + needle + "\"\n  stdout: " + actual.stdout);
}

pub function assert_stderr_contains(actual: ProcessResult, needle: string): TestOutcome {
  if (actual.stderr.contains(needle)) {
    return Pass;
  }
  return Fail("assert_stderr_contains: stderr does not contain \""
    + needle + "\"\n  stderr: " + actual.stderr);
}

pub function assert_process(actual: ProcessResult, expected_exit: i32, stdout_substr: string): TestOutcome {
  if (actual.exit_code != expected_exit) {
    return assert_exit(actual, expected_exit);
  }
  if (stdout_substr.len() > 0 && !actual.stdout.contains(stdout_substr)) {
    return Fail("assert_process: exit " + expected_exit.to_string()
      + " ok, but stdout missing \"" + stdout_substr
      + "\"\n  stdout: " + actual.stdout);
  }
  return Pass;
}

pub function assert_exit_zero(actual: ProcessResult): TestOutcome {
  return assert_exit(actual, 0);
}

pub function assert_exit_nonzero(actual: ProcessResult): TestOutcome {
  if (actual.exit_code != 0) {
    return Pass;
  }
  return Fail("assert_exit_nonzero: process exited 0, expected non-zero");
}

pub function assert_stdout_lines(actual: ProcessResult, expected_lines: string[]): TestOutcome {
  return assert_lines_eq(actual.stdout, expected_lines);
}

pub function assert_stderr_lines(actual: ProcessResult, expected_lines: string[]): TestOutcome {
  return assert_lines_eq(actual.stderr, expected_lines);
}

pub function assert_stdout_line_count(actual: ProcessResult, n: i32): TestOutcome {
  let got: i32 = actual.stdout.lines().len();
  if (got == n) {
    return Pass;
  }
  return Fail("assert_stdout_line_count: got " + got.to_string()
    + " lines, expected " + n.to_string());
}

pub function assert_stderr_line_count(actual: ProcessResult, n: i32): TestOutcome {
  let got: i32 = actual.stderr.lines().len();
  if (got == n) {
    return Pass;
  }
  return Fail("assert_stderr_line_count: got " + got.to_string()
    + " lines, expected " + n.to_string());
}
```

### `14 pub function assert_contains_all(haystack: string, needles: string[]): TestOutcome`

```fern
<<14 pub function assert_contains_all(haystack: string, needles: string[]): TestOutcome>>=
pub function assert_contains_all(haystack: string, needles: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < needles.len()) {
    if (!haystack.contains(needles[i])) {
      return Fail("assert_contains_all: missing \"" + needles[i]
        + "\" in:\n  " + haystack);
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_contains_any(haystack: string, needles: string[]): TestOutcome {
  if (needles.len() == 0) {
    return Fail("assert_contains_any: empty needles list");
  }
  let i: i32 = 0;
  while (i < needles.len()) {
    if (haystack.contains(needles[i])) {
      return Pass;
    }
    i = i + 1;
  }
  return Fail("assert_contains_any: none of " + i.to_string()
    + " needles matched in:\n  " + haystack);
}

pub function assert_contains_in_order(haystack: string, needles: string[]): TestOutcome {
  let cursor: i32 = 0;
  let i: i32 = 0;
  while (i < needles.len()) {
    let rest: str = slice_unchecked(haystack, cursor, haystack.len());
    let Some(idx) = rest.index_of(needles[i]) else {
      return Fail("assert_contains_in_order: \"" + needles[i]
        + "\" not found after byte " + cursor.to_string()
        + " in:\n  " + haystack);
    };
    cursor = cursor + idx + needles[i].len();
    i = i + 1;
  }
  return Pass;
}
```

### `15 function __diff_lines(a: string, b: string): i32`

```fern
<<15 function __diff_lines(a: string, b: string): i32>>=
function __diff_lines(a: string, b: string): i32 {
  let a_lines: string[] = a.lines();
  let b_lines: string[] = b.lines();
  let n: i32 = a_lines.len();
  if (b_lines.len() < n) {
    n = b_lines.len();
  }
  let i: i32 = 0;
  while (i < n) {
    if (a_lines[i] != b_lines[i]) {
      return i + 1;
    }
    i = i + 1;
  }
  if (a_lines.len() != b_lines.len()) {
    return n + 1;
  }
  return 0;
}
```

### `16 pub function assert_eq_string_diff(actual: string, expected: string): TestOutcome`

```fern
<<16 pub function assert_eq_string_diff(actual: string, expected: string): TestOutcome>>=
pub function assert_eq_string_diff(actual: string, expected: string): TestOutcome {
  if (actual == expected) {
    return Pass;
  }
  let line_no: i32 = __diff_lines(expected, actual);
  let a_lines: string[] = actual.lines();
  let e_lines: string[] = expected.lines();
  let actual_line: string = "<past end>";
  let expected_line: string = "<past end>";
  let ix: i32 = line_no - 1;
  if (ix >= 0 && ix < a_lines.len()) {
    actual_line = a_lines[ix];
  }
  if (ix >= 0 && ix < e_lines.len()) {
    expected_line = e_lines[ix];
  }
  return Fail("assert_eq_string_diff: differ at line " + line_no.to_string()
    + "\n  expected: " + expected_line
    + "\n  actual:   " + actual_line);
}

pub function assert_lines_eq(actual: string, expected_lines: string[]): TestOutcome {
  let a_lines: string[] = actual.lines();
  let an: i32 = a_lines.len();
  let en: i32 = expected_lines.len();
  if (an != en) {
    return Fail("assert_lines_eq: line count differs - expected "
      + en.to_string() + ", got " + an.to_string());
  }
  let i: i32 = 0;
  while (i < an) {
    if (a_lines[i] != expected_lines[i]) {
      return Fail("assert_lines_eq: differ at line " + (i + 1).to_string()
        + "\n  expected: " + expected_lines[i]
        + "\n  actual:   " + a_lines[i]);
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_in_range_i32(v: i32, lo: i32, hi: i32): TestOutcome {
  if (lo > hi) {
    return Fail("assert_in_range_i32: inverted range - lower bound "
      + lo.to_string() + " > upper bound " + hi.to_string());
  }
  if (v < lo) {
    return Fail("assert_in_range_i32: " + v.to_string()
      + " < lower bound " + lo.to_string());
  }
  if (v > hi) {
    return Fail("assert_in_range_i32: " + v.to_string()
      + " > upper bound " + hi.to_string());
  }
  return Pass;
}

pub function assert_in_range_i64(v: i64, lo: i64, hi: i64): TestOutcome {
  if (lo > hi) {
    return Fail("assert_in_range_i64: inverted range - lower bound "
      + lo.to_string() + " > upper bound " + hi.to_string());
  }
  if (v < lo) {
    return Fail("assert_in_range_i64: " + v.to_string()
      + " < lower bound " + lo.to_string());
  }
  if (v > hi) {
    return Fail("assert_in_range_i64: " + v.to_string()
      + " > upper bound " + hi.to_string());
  }
  return Pass;
}

pub function assert_in_range_f64(v: f64, lo: f64, hi: f64): TestOutcome {
  if (v != v) {
    return Fail("assert_in_range_f64: value is NaN");
  }
  if (lo != lo) {
    return Fail("assert_in_range_f64: lower bound is NaN");
  }
  if (hi != hi) {
    return Fail("assert_in_range_f64: upper bound is NaN");
  }
  if (lo > hi) {
    return Fail("assert_in_range_f64: inverted range - lower bound "
      + lo.to_string() + " > upper bound " + hi.to_string());
  }
  if (v < lo) {
    return Fail("assert_in_range_f64: " + v.to_string()
      + " < lower bound " + lo.to_string());
  }
  if (v > hi) {
    return Fail("assert_in_range_f64: " + v.to_string()
      + " > upper bound " + hi.to_string());
  }
  return Pass;
}

pub function assert_in_range_f32(v: f32, lo: f32, hi: f32): TestOutcome {
  if (v != v) {
    return Fail("assert_in_range_f32: value is NaN");
  }
  if (lo != lo) {
    return Fail("assert_in_range_f32: lower bound is NaN");
  }
  if (hi != hi) {
    return Fail("assert_in_range_f32: upper bound is NaN");
  }
  if (lo > hi) {
    return Fail("assert_in_range_f32: inverted range - lower bound "
      + lo.to_string() + " > upper bound " + hi.to_string());
  }
  if (v < lo) {
    return Fail("assert_in_range_f32: " + v.to_string()
      + " < lower bound " + lo.to_string());
  }
  if (v > hi) {
    return Fail("assert_in_range_f32: " + v.to_string()
      + " > upper bound " + hi.to_string());
  }
  return Pass;
}

pub function assert_file_exists(path: string): TestOutcome {
  match (read_file(path)) {
    Ok(_) => {
      return Pass;
    },
    Err(_) => {
      return Fail("assert_file_exists: \"" + path + "\" does not exist or is unreadable");
    }
  }
  return Pass;
}
```

### `17 pub function assert_file_not_exists(path: string): TestOutcome`

```fern
<<17 pub function assert_file_not_exists(path: string): TestOutcome>>=
pub function assert_file_not_exists(path: string): TestOutcome {
  match (read_file(path)) {
    Ok(_) => {
      return Fail("assert_file_not_exists: \"" + path + "\" still exists");
    },
    Err(_) => {
      return Pass;
    }
  }
  return Pass;
}

pub function assert_file_contents(path: string, expected: string): TestOutcome {
  match (read_file(path)) {
    Ok(actual) => {
      return assert_eq_string_diff(actual, expected);
    },
    Err(_) => {
      return Fail("assert_file_contents: failed to read \"" + path + "\"");
    }
  }
  return Pass;
}

pub function assert_file_lines(path: string, expected_lines: string[]): TestOutcome {
  match (read_file(path)) {
    Ok(actual) => {
      return assert_lines_eq(actual, expected_lines);
    },
    Err(_) => {
      return Fail("assert_file_lines: failed to read \"" + path + "\"");
    }
  }
  return Pass;
}

pub function assert_file_line_count(path: string, n: i32): TestOutcome {
  match (read_file(path)) {
    Ok(actual) => {
      let got: i32 = actual.lines().len();
      if (got == n) {
        return Pass;
      }
      return Fail("assert_file_line_count: \"" + path
        + "\" has " + got.to_string()
        + " lines, expected " + n.to_string());
    },
    Err(_) => {
      return Fail("assert_file_line_count: failed to read \""
        + path + "\"");
    }
  }
  return Pass;
}

pub function assert_is_file(path: string): TestOutcome {
  match (stat(path)) {
    Ok(s) => {
      if (s.is_file) {
        return Pass;
      }
      if (s.is_dir) {
        return Fail("assert_is_file: \"" + path + "\" is a directory");
      }
      return Fail("assert_is_file: \"" + path + "\" is not a regular file");
    },
    Err(_) => {
      return Fail("assert_is_file: \"" + path + "\" does not exist");
    }
  }
  return Pass;
}

pub function assert_is_dir(path: string): TestOutcome {
  match (stat(path)) {
    Ok(s) => {
      if (s.is_dir) {
        return Pass;
      }
      if (s.is_file) {
        return Fail("assert_is_dir: \"" + path + "\" is a regular file");
      }
      return Fail("assert_is_dir: \"" + path + "\" is not a directory");
    },
    Err(_) => {
      return Fail("assert_is_dir: \"" + path + "\" does not exist");
    }
  }
  return Pass;
}

pub function assert_file_size(path: string, expected: i64): TestOutcome {
  match (stat(path)) {
    Ok(s) => {
      if (s.size == expected) {
        return Pass;
      }
      return Fail("assert_file_size: \"" + path + "\" is "
        + s.size.to_string() + " bytes, expected " + expected.to_string());
    },
    Err(_) => {
      return Fail("assert_file_size: \"" + path + "\" does not exist");
    }
  }
  return Pass;
}

pub function assert_eq_dir_listing(dir: string, expected_names: string[]): TestOutcome {
  match (read_dir(dir)) {
    Ok(names) => {
      let actual_sorted: string[] = cmp.sort(names);
      let expected_sorted: string[] = cmp.sort(expected_names);
      return assert_eq_array(actual_sorted, expected_sorted);
    },
    Err(_) => {
      return Fail("assert_eq_dir_listing: failed to read \""
        + dir + "\"");
    }
  }
  return Pass;
}
```

### `18 pub function assert_file_contains(path: string, needle: string): TestOutcome`

```fern
<<18 pub function assert_file_contains(path: string, needle: string): TestOutcome>>=
pub function assert_file_contains(path: string, needle: string): TestOutcome {
  match (read_file(path)) {
    Ok(actual) => {
      if (actual.contains(needle)) {
        return Pass;
      }
      return Fail("assert_file_contains: \"" + path
        + "\" does not contain \"" + needle
        + "\"\n  contents: " + actual);
    },
    Err(_) => {
      return Fail("assert_file_contains: failed to read \"" + path + "\"");
    }
  }
  return Pass;
}

pub function must_temp_dir(r: TestRunner, prefix: string): (string, TestRunner) {
  match (temp_dir(prefix)) {
    Ok(dir) => {
      return (dir, r.defer_cleanup(dir));
    },
    Err(_) => {
      return ("", r.skip("temp_dir " + prefix, "tempdir creation failed"));
    }
  }
  return ("", r);
}
```

### `19 function __bench_sort_us(samples: i64[]): i64[]`

```fern
<<19 function __bench_sort_us(samples: i64[]): i64[]>>=
function __bench_sort_us(samples: i64[]): i64[] {
  let n: i32 = samples.len();
  let taken: boolean[] = [];
  let i: i32 = 0;
  while (i < n) {
    taken = taken.append(false);
    i = i + 1;
  }
  let out: i64[] = [];
  i = 0;
  while (i < n) {
    let best: i32 = 0 - 1;
    let j: i32 = 0;
    while (j < n) {
      if (!taken[j]) {
        if (best < 0 || samples[j] < samples[best]) {
          best = j;
        }
      }
      j = j + 1;
    }
    out = out.append(samples[best]);
    taken = taken.append(true);
    let t2: boolean[] = [];
    let k: i32 = 0;
    while (k < n) {
      if (k == best) {
        t2 = t2.append(true);
      } else {
        t2 = t2.append(taken[k]);
      }
      k = k + 1;
    }
    taken = t2;
    i = i + 1;
  }
  return out;
}
```

### `20 pub function (r: TestRunner) bench(name: string, iterations: i32, fn: ()`

```fern
<<20 pub function (r: TestRunner) bench(name: string, iterations: i32, fn: ()>>=
pub function (r: TestRunner) bench(name: string, iterations: i32, fn: () => void): TestRunner {
  if (iterations <= 0) {
    return r.it(name, () => fail("bench: iterations must be > 0"));
  }
  let samples: i64[] = [];
  let i: i32 = 0;
  while (i < iterations) {
    let start_ns: i64 = monotonic_ns();
    fn();
    let elapsed_ns: i64 = monotonic_ns() - start_ns;
    samples = samples.append(elapsed_ns / (1000 as i64));
    i = i + 1;
  }
  let sorted: i64[] = __bench_sort_us(samples);
  let min_us: i64 = sorted[0];
  let max_us: i64 = sorted[iterations - 1];
  let median_us: i64 = sorted[iterations / 2];
  let sum_us: i64 = 0 as i64;
  let k: i32 = 0;
  while (k < iterations) {
    sum_us = sum_us + sorted[k];
    k = k + 1;
  }
  let mean_us: i64 = sum_us / (iterations as i64);
  print("# bench " + name + " (" + iterations.to_string() + " iters): "
    + "min=" + min_us.to_string() + "us "
    + "median=" + median_us.to_string() + "us "
    + "mean=" + mean_us.to_string() + "us "
    + "max=" + max_us.to_string() + "us");
  return r.it(name, () => Pass);
}

pub function (r: TestRunner) bench_max_us(name: string, iterations: i32, fn: () => void, max_us: i64): TestRunner {
  if (iterations <= 0) {
    return r.it(name, () => fail("bench: iterations must be > 0"));
  }
  let samples: i64[] = [];
  let i: i32 = 0;
  while (i < iterations) {
    let start_ns: i64 = monotonic_ns();
    fn();
    let elapsed_ns: i64 = monotonic_ns() - start_ns;
    samples = samples.append(elapsed_ns / (1000 as i64));
    i = i + 1;
  }
  let sorted: i64[] = __bench_sort_us(samples);
  let median_us: i64 = sorted[iterations / 2];
  let max_us_observed: i64 = sorted[iterations - 1];
  print("# bench " + name + " (" + iterations.to_string() + " iters): "
    + "median=" + median_us.to_string() + "us "
    + "max=" + max_us_observed.to_string() + "us "
    + "budget=" + max_us.to_string() + "us");
  if (median_us > max_us) {
    let over_us: string = "bench_max_us: median " + median_us.to_string()
      + "us > budget " + max_us.to_string() + "us";
    return r.it(name, () => Fail(over_us));
  }
  return r.it(name, () => Pass);
}

pub function (r: TestRunner) bench_max_ms(name: string, iterations: i32, fn: () => void, max_ms: i64): TestRunner {
  return r.bench_max_us(name, iterations, fn, max_ms * (1000 as i64));
}

pub function assert_elapsed_lt_ms(start_ns: i64, max_ms: i64): TestOutcome {
  let elapsed_ns: i64 = monotonic_ns() - start_ns;
  let elapsed_ms: i64 = elapsed_ns / (1000000 as i64);
  if (elapsed_ms < max_ms) {
    return Pass;
  }
  return Fail("assert_elapsed_lt_ms: " + elapsed_ms.to_string()
    + "ms elapsed, expected < " + max_ms.to_string() + "ms");
}

pub function assert_elapsed_lt_us(start_ns: i64, max_us: i64): TestOutcome {
  let elapsed_ns: i64 = monotonic_ns() - start_ns;
  let elapsed_us: i64 = elapsed_ns / (1000 as i64);
  if (elapsed_us < max_us) {
    return Pass;
  }
  return Fail("assert_elapsed_lt_us: " + elapsed_us.to_string()
    + "us elapsed, expected < " + max_us.to_string() + "us");
}

pub function assert_close_to_now_ms(actual_ms: i64, max_skew_ms: i64): TestOutcome {
  let now_ms: i64 = now_unix_ms();
  let skew_ms: i64 = now_ms - actual_ms;
  let abs_skew: i64 = skew_ms;
  if (abs_skew < 0 as i64) {
    abs_skew = 0 as i64 - abs_skew;
  }
  if (abs_skew <= max_skew_ms) {
    return Pass;
  }
  return Fail("assert_close_to_now_ms: skew " + skew_ms.to_string()
    + "ms exceeds bound " + max_skew_ms.to_string() + "ms");
}
```

### `21 function __json_value_eq(a: json.JsonValue, b: json.JsonValue): boolean`

```fern
<<21 function __json_value_eq(a: json.JsonValue, b: json.JsonValue): boolean>>=
function __json_value_eq(a: json.JsonValue, b: json.JsonValue): boolean {
  match (a) {
    JNull => {
      match (b) {
        JNull => {
          return true;
        },
        _ => {
          return false;
        }
      }
    },
    JBool(av) => {
      match (b) {
        JBool(bv) => {
          return av == bv;
        },
        _ => {
          return false;
        }
      }
    },
    JNumber(av) => {
      match (b) {
        JNumber(bv) => {
          return av == bv;
        },
        _ => {
          return false;
        }
      }
    },
    JString(av) => {
      match (b) {
        JString(bv) => {
          return av == bv;
        },
        _ => {
          return false;
        }
      }
    },
    JArray(av) => {
      match (b) {
        JArray(bv) => {
          if (av.len() != bv.len()) {
            return false;
          }
          let i: i32 = 0;
          while (i < av.len()) {
            if (!__json_value_eq(av[i], bv[i])) {
              return false;
            }
            i = i + 1;
          }
          return true;
        },
        _ => {
          return false;
        }
      }
    },
    JObject(am) => {
      match (b) {
        JObject(bm) => {
          if (am.len() != bm.len()) {
            return false;
          }
          let keys: string[] = am.keys();
          let i: i32 = 0;
          while (i < keys.len()) {
            let k: string = keys[i];
            match (am.get(k)) {
              None => {
                return false;
              },
              Some(av) => {
                match (bm.get(k)) {
                  None => {
                    return false;
                  },
                  Some(bv) => {
                    if (!__json_value_eq(av, bv)) {
                      return false;
                    }
                  }
                }
              }
            }
            i = i + 1;
          }
          return true;
        },
        _ => {
          return false;
        }
      }
    }
  }
  return false;
}
```

### `22 pub function assert_matches_golden(path: string, actual: string): TestOutcome`

```fern
<<22 pub function assert_matches_golden(path: string, actual: string): TestOutcome>>=
pub function assert_matches_golden(path: string, actual: string): TestOutcome {
  match (read_file(path)) {
    Ok(expected) => {
      return assert_eq_string_diff(actual, expected);
    },
    Err(_) => {
      match (write_file(path, actual)) {
        Ok(_) => {
          print("# golden file bootstrapped at " + path);
          return Pass;
        },
        Err(_) => {
          return Fail("assert_matches_golden: could not write " + path);
        }
      }
    }
  }
  return Pass;
}

pub function assert_matches_golden_strict(path: string, actual: string): TestOutcome {
  match (read_file(path)) {
    Ok(expected) => {
      return assert_eq_string_diff(actual, expected);
    },
    Err(_) => {
      return Fail("assert_matches_golden_strict: golden file missing: " + path);
    }
  }
  return Pass;
}

pub function assert_env_set(name: string): TestOutcome {
  match (env(name)) {
    Some(_) => {
      return Pass;
    },
    None => {
      return Fail("assert_env_set: $" + name + " is not set");
    }
  }
  return Pass;
}

pub function assert_env_unset(name: string): TestOutcome {
  match (env(name)) {
    Some(v) => {
      return Fail("assert_env_unset: $" + name + " is set to \"" + v + "\"");
    },
    None => {
      return Pass;
    }
  }
  return Pass;
}

pub function assert_env_eq(name: string, expected: string): TestOutcome {
  match (env(name)) {
    Some(v) => {
      if (v == expected) {
        return Pass;
      }
      return Fail("assert_env_eq: $" + name + " = \"" + v
        + "\", expected \"" + expected + "\"");
    },
    None => {
      return Fail("assert_env_eq: $" + name + " is not set");
    }
  }
  return Pass;
}

pub function assert_map_len[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](m: Map[K, V], n: i32): TestOutcome {
  let got: i32 = m.len();
  if (got == n) {
    return Pass;
  }
  return Fail("assert_map_len: expected " + n.to_string()
    + " entries, got " + got.to_string());
}

pub function assert_map_has[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](m: Map[K, V], k: K, v: V): TestOutcome {
  match (m.get(k)) {
    Some(got) => {
      if (got.eq(v)) {
        return Pass;
      }
      return Fail("assert_map_has: key " + k.to_string()
        + " -> " + got.to_string() + ", expected " + v.to_string());
    },
    None => {
      return Fail("assert_map_has: key " + k.to_string() + " missing");
    }
  }
  return Pass;
}

pub function assert_map_lacks[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](m: Map[K, V], k: K): TestOutcome {
  if (m.contains(k)) {
    return Fail("assert_map_lacks: key " + k.to_string() + " unexpectedly present");
  }
  return Pass;
}
```

### `23 pub function assert_eq_map[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](actual: Map[K, V], expected: M...`

```fern
<<23 pub function assert_eq_map[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](actual: Map[K, V], expected: M...>>=
pub function assert_eq_map[K: cmp.Eq + cmp.Display, V: cmp.Eq + cmp.Display](actual: Map[K, V], expected: Map[K, V]): TestOutcome {
  let an: i32 = actual.len();
  let en: i32 = expected.len();
  if (an != en) {
    return Fail("assert_eq_map: length differs - expected "
      + en.to_string() + ", got " + an.to_string());
  }
  let ks: K[] = actual.keys();
  let i: i32 = 0;
  while (i < ks.len()) {
    let k: K = ks[i];
    match (expected.get(k)) {
      None => {
        return Fail("assert_eq_map: key " + k.to_string()
          + " missing from expected");
      },
      Some(ev) => {
        match (actual.get(k)) {
          Some(av) => {
            if (!av.eq(ev)) {
              return Fail("assert_eq_map: at key " + k.to_string()
                + " expected " + ev.to_string()
                + ", got " + av.to_string());
            }
          },
          None => {}
        }
      }
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_all_i32(arr: i32[], pred: (i32) => boolean): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (!pred(arr[i])) {
      return Fail("assert_all_i32: predicate failed at index "
        + i.to_string() + ": value " + arr[i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_all_string(arr: string[], pred: (string) => boolean): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (!pred(arr[i])) {
      return Fail("assert_all_string: predicate failed at index "
        + i.to_string() + ": value \"" + arr[i] + "\"");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_any_i32(arr: i32[], pred: (i32) => boolean): TestOutcome {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (pred(arr[i])) {
      return Pass;
    }
    i = i + 1;
  }
  return Fail("assert_any_i32: no element of "
    + n.to_string() + " matched");
}

pub function assert_any_string(arr: string[], pred: (string) => boolean): TestOutcome {
  let n: i32 = arr.len();
  let i: i32 = 0;
  while (i < n) {
    if (pred(arr[i])) {
      return Pass;
    }
    i = i + 1;
  }
  return Fail("assert_any_string: no element of "
    + n.to_string() + " matched");
}

pub function assert_count_i32(arr: i32[], pred: (i32) => boolean, expected_count: i32): TestOutcome {
  let hits: i32 = 0;
  let i: i32 = 0;
  while (i < arr.len()) {
    if (pred(arr[i])) {
      hits = hits + 1;
    }
    i = i + 1;
  }
  if (hits == expected_count) {
    return Pass;
  }
  return Fail("assert_count_i32: expected "
    + expected_count.to_string() + " matches, got "
    + hits.to_string());
}

pub function assert_count_string(arr: string[], pred: (string) => boolean, expected_count: i32): TestOutcome {
  let hits: i32 = 0;
  let i: i32 = 0;
  while (i < arr.len()) {
    if (pred(arr[i])) {
      hits = hits + 1;
    }
    i = i + 1;
  }
  if (hits == expected_count) {
    return Pass;
  }
  return Fail("assert_count_string: expected "
    + expected_count.to_string() + " matches, got "
    + hits.to_string());
}
```

### `24 pub function assert_one_of_i32(actual: i32, allowed: i32[]): TestOutcome`

```fern
<<24 pub function assert_one_of_i32(actual: i32, allowed: i32[]): TestOutcome>>=
pub function assert_one_of_i32(actual: i32, allowed: i32[]): TestOutcome {
  let i: i32 = 0;
  while (i < allowed.len()) {
    if (allowed[i] == actual) {
      return Pass;
    }
    i = i + 1;
  }
  let rendered: string = "[";
  let j: i32 = 0;
  while (j < allowed.len()) {
    if (j > 0) {
      rendered = rendered + ", ";
    }
    rendered = rendered + allowed[j].to_string();
    j = j + 1;
  }
  rendered = rendered + "]";
  return Fail("assert_one_of_i32: " + actual.to_string()
    + " not in allowed set " + rendered);
}

pub function assert_one_of_string(actual: string, allowed: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < allowed.len()) {
    if (allowed[i] == actual) {
      return Pass;
    }
    i = i + 1;
  }
  let rendered: string = "[";
  let j: i32 = 0;
  while (j < allowed.len()) {
    if (j > 0) {
      rendered = rendered + ", ";
    }
    rendered = rendered + "\"" + allowed[j] + "\"";
    j = j + 1;
  }
  rendered = rendered + "]";
  return Fail("assert_one_of_string: \"" + actual
    + "\" not in allowed set " + rendered);
}

pub function assert_none_of_i32(actual: i32, forbidden: i32[]): TestOutcome {
  let i: i32 = 0;
  while (i < forbidden.len()) {
    if (forbidden[i] == actual) {
      return Fail("assert_none_of_i32: " + actual.to_string()
        + " matched forbidden value");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_none_of_string(actual: string, forbidden: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < forbidden.len()) {
    if (forbidden[i] == actual) {
      return Fail("assert_none_of_string: \"" + actual
        + "\" matched forbidden value");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_is_some_i32(opt: Option[i32]): TestOutcome {
  match (opt) {
    Some(_) => {
      return Pass;
    },
    None => {
      return Fail("assert_is_some_i32: got None");
    }
  }
}

pub function assert_is_some_string(opt: Option[string]): TestOutcome {
  match (opt) {
    Some(_) => {
      return Pass;
    },
    None => {
      return Fail("assert_is_some_string: got None");
    }
  }
}

pub function assert_is_none_i32(opt: Option[i32]): TestOutcome {
  match (opt) {
    Some(v) => {
      return Fail("assert_is_none_i32: got Some("
        + v.to_string() + ")");
    },
    None => {
      return Pass;
    }
  }
}

pub function assert_is_none_string(opt: Option[string]): TestOutcome {
  match (opt) {
    Some(v) => {
      return Fail("assert_is_none_string: got Some(\""
        + v + "\")");
    },
    None => {
      return Pass;
    }
  }
}

pub function assert_is_some_eq_i32(opt: Option[i32], expected: i32): TestOutcome {
  match (opt) {
    Some(v) => {
      if (v == expected) {
        return Pass;
      }
      return Fail("assert_is_some_eq_i32: got Some("
        + v.to_string() + "), expected Some("
        + expected.to_string() + ")");
    },
    None => {
      return Fail("assert_is_some_eq_i32: got None, expected Some("
        + expected.to_string() + ")");
    }
  }
}
```

### `25 pub function assert_is_some_eq_string(opt: Option[string], expected: string): TestOutcome`

```fern
<<25 pub function assert_is_some_eq_string(opt: Option[string], expected: string): TestOutcome>>=
pub function assert_is_some_eq_string(opt: Option[string], expected: string): TestOutcome {
  match (opt) {
    Some(v) => {
      if (v == expected) {
        return Pass;
      }
      return Fail("assert_is_some_eq_string: got Some(\""
        + v + "\"), expected Some(\""
        + expected + "\")");
    },
    None => {
      return Fail("assert_is_some_eq_string: got None, expected Some(\""
        + expected + "\")");
    }
  }
}

pub function assert_status(resp: HttpResponse, expected: i32): TestOutcome {
  if (resp.status == expected) {
    return Pass;
  }
  return Fail("assert_status: got " + resp.status.to_string() + ", expected " + expected.to_string());
}

pub function assert_header(resp: HttpResponse, name: string, expected: string): TestOutcome {
  match (resp.headers.get(name)) {
    Some(v) => {
      if (v == expected) {
        return Pass;
      }
      return Fail("assert_header: " + name + " is \"" + v + "\", expected \"" + expected + "\"");
    },
    None => {
      return Fail("assert_header: no " + name + " header, expected \"" + expected + "\"");
    }
  }
}

pub function assert_no_header(resp: HttpResponse, name: string): TestOutcome {
  match (resp.headers.get(name)) {
    Some(v) => {
      return Fail("assert_no_header: " + name + " is \"" + v + "\", expected no " + name + " header");
    },
    None => {
      return Pass;
    }
  }
}

pub function assert_body(resp: HttpResponse, expected: string): TestOutcome {
  let got: string = resp.body_string();
  if (got == expected) {
    return Pass;
  }
  return Fail("assert_body: got \"" + got + "\", expected \"" + expected + "\"");
}

pub function assert_is_ok_string(res: Result[string, IoError]): TestOutcome {
  match (res) {
    Ok(_) => {
      return Pass;
    },
    Err(_) => {
      return Fail("assert_is_ok_string: got Err");
    }
  }
}

pub function assert_is_err_string(res: Result[string, IoError]): TestOutcome {
  match (res) {
    Ok(v) => {
      return Fail("assert_is_err_string: got Ok(\"" + v + "\")");
    },
    Err(_) => {
      return Pass;
    }
  }
}

pub function assert_is_ok_eq_string(res: Result[string, IoError], expected: string): TestOutcome {
  match (res) {
    Ok(v) => {
      if (v == expected) {
        return Pass;
      }
      return Fail("assert_is_ok_eq_string: got Ok(\""
        + v + "\"), expected Ok(\""
        + expected + "\")");
    },
    Err(_) => {
      return Fail("assert_is_ok_eq_string: got Err, expected Ok(\""
        + expected + "\")");
    }
  }
}

pub function assert_is_ok_string_array(res: Result[string[], IoError]): TestOutcome {
  match (res) {
    Ok(_) => {
      return Pass;
    },
    Err(_) => {
      return Fail("assert_is_ok_string_array: got Err");
    }
  }
}

pub function assert_is_err_string_array(res: Result[string[], IoError]): TestOutcome {
  match (res) {
    Ok(v) => {
      return Fail("assert_is_err_string_array: got Ok of length "
        + v.len().to_string());
    },
    Err(_) => {
      return Pass;
    }
  }
}

pub function assert_array_intersects_i32(a: i32[], b: i32[]): TestOutcome {
  let i: i32 = 0;
  while (i < a.len()) {
    let j: i32 = 0;
    while (j < b.len()) {
      if (a[i] == b[j]) {
        return Pass;
      }
      j = j + 1;
    }
    i = i + 1;
  }
  return Fail("assert_array_intersects_i32: no shared element between arrays of length "
    + a.len().to_string() + " and " + b.len().to_string());
}
```

### `26 pub function assert_array_intersects_string(a: string[], b: string[]): TestOutcome`

```fern
<<26 pub function assert_array_intersects_string(a: string[], b: string[]): TestOutcome>>=
pub function assert_array_intersects_string(a: string[], b: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < a.len()) {
    let j: i32 = 0;
    while (j < b.len()) {
      if (a[i] == b[j]) {
        return Pass;
      }
      j = j + 1;
    }
    i = i + 1;
  }
  return Fail("assert_array_intersects_string: no shared element between arrays of length "
    + a.len().to_string() + " and " + b.len().to_string());
}

pub function assert_array_disjoint_i32(a: i32[], b: i32[]): TestOutcome {
  let i: i32 = 0;
  while (i < a.len()) {
    let j: i32 = 0;
    while (j < b.len()) {
      if (a[i] == b[j]) {
        return Fail("assert_array_disjoint_i32: shared element "
          + a[i].to_string());
      }
      j = j + 1;
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_array_disjoint_string(a: string[], b: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < a.len()) {
    let j: i32 = 0;
    while (j < b.len()) {
      if (a[i] == b[j]) {
        return Fail("assert_array_disjoint_string: shared element \""
          + a[i] + "\"");
      }
      j = j + 1;
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_array_starts_with_i32(arr: i32[], prefix: i32[]): TestOutcome {
  let pn: i32 = prefix.len();
  if (pn > arr.len()) {
    return Fail("assert_array_starts_with_i32: array length "
      + arr.len().to_string() + " < prefix length "
      + pn.to_string());
  }
  let i: i32 = 0;
  while (i < pn) {
    if (arr[i] != prefix[i]) {
      return Fail("assert_array_starts_with_i32: at index "
        + i.to_string() + " expected "
        + prefix[i].to_string() + ", got "
        + arr[i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_array_starts_with_string(arr: string[], prefix: string[]): TestOutcome {
  let pn: i32 = prefix.len();
  if (pn > arr.len()) {
    return Fail("assert_array_starts_with_string: array length "
      + arr.len().to_string() + " < prefix length "
      + pn.to_string());
  }
  let i: i32 = 0;
  while (i < pn) {
    if (arr[i] != prefix[i]) {
      return Fail("assert_array_starts_with_string: at index "
        + i.to_string() + " expected \""
        + prefix[i] + "\", got \"" + arr[i] + "\"");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_array_ends_with_i32(arr: i32[], suffix: i32[]): TestOutcome {
  let sn: i32 = suffix.len();
  let an: i32 = arr.len();
  if (sn > an) {
    return Fail("assert_array_ends_with_i32: array length "
      + an.to_string() + " < suffix length "
      + sn.to_string());
  }
  let off: i32 = an - sn;
  let i: i32 = 0;
  while (i < sn) {
    if (arr[off + i] != suffix[i]) {
      return Fail("assert_array_ends_with_i32: at index "
        + (off + i).to_string() + " expected "
        + suffix[i].to_string() + ", got "
        + arr[off + i].to_string());
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_array_ends_with_string(arr: string[], suffix: string[]): TestOutcome {
  let sn: i32 = suffix.len();
  let an: i32 = arr.len();
  if (sn > an) {
    return Fail("assert_array_ends_with_string: array length "
      + an.to_string() + " < suffix length "
      + sn.to_string());
  }
  let off: i32 = an - sn;
  let i: i32 = 0;
  while (i < sn) {
    if (arr[off + i] != suffix[i]) {
      return Fail("assert_array_ends_with_string: at index "
        + (off + i).to_string() + " expected \""
        + suffix[i] + "\", got \"" + arr[off + i] + "\"");
    }
    i = i + 1;
  }
  return Pass;
}
```

### `27 function __match_subseq_i32(arr: i32[], needle: i32[]): boolean`

```fern
<<27 function __match_subseq_i32(arr: i32[], needle: i32[]): boolean>>=
function __match_subseq_i32(arr: i32[], needle: i32[]): boolean {
  let nn: i32 = needle.len();
  if (nn == 0) {
    return true;
  }
  let an: i32 = arr.len();
  if (nn > an) {
    return false;
  }
  let start: i32 = 0;
  while (start <= an - nn) {
    let matched: boolean = true;
    let i: i32 = 0;
    while (i < nn) {
      if (arr[start + i] != needle[i]) {
        matched = false;
        i = nn;
      } else {
        i = i + 1;
      }
    }
    if (matched) {
      return true;
    }
    start = start + 1;
  }
  return false;
}
```

### `28 pub function assert_array_contains_subseq_i32(arr: i32[], needle: i32[]): TestOutcome`

```fern
<<28 pub function assert_array_contains_subseq_i32(arr: i32[], needle: i32[]): TestOutcome>>=
pub function assert_array_contains_subseq_i32(arr: i32[], needle: i32[]): TestOutcome {
  if (__match_subseq_i32(arr, needle)) {
    return Pass;
  }
  return Fail("assert_array_contains_subseq_i32: needle of length "
    + needle.len().to_string()
    + " not found in array of length "
    + arr.len().to_string());
}
```

### `29 function __match_subseq_string(arr: string[], needle: string[]): boolean`

```fern
<<29 function __match_subseq_string(arr: string[], needle: string[]): boolean>>=
function __match_subseq_string(arr: string[], needle: string[]): boolean {
  let nn: i32 = needle.len();
  if (nn == 0) {
    return true;
  }
  let an: i32 = arr.len();
  if (nn > an) {
    return false;
  }
  let start: i32 = 0;
  while (start <= an - nn) {
    let matched: boolean = true;
    let i: i32 = 0;
    while (i < nn) {
      if (arr[start + i] != needle[i]) {
        matched = false;
        i = nn;
      } else {
        i = i + 1;
      }
    }
    if (matched) {
      return true;
    }
    start = start + 1;
  }
  return false;
}
```

### `30 pub function assert_array_contains_subseq_string(arr: string[], needle: string[]): TestOutcome`

```fern
<<30 pub function assert_array_contains_subseq_string(arr: string[], needle: string[]): TestOutcome>>=
pub function assert_array_contains_subseq_string(arr: string[], needle: string[]): TestOutcome {
  if (__match_subseq_string(arr, needle)) {
    return Pass;
  }
  return Fail("assert_array_contains_subseq_string: needle of length "
    + needle.len().to_string()
    + " not found in array of length "
    + arr.len().to_string());
}

pub function assert_all_starts_with(arr: string[], prefix: string): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (!arr[i].starts_with(prefix)) {
      return Fail("assert_all_starts_with: at index "
        + i.to_string() + " value \"" + arr[i]
        + "\" does not start with \"" + prefix + "\"");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_all_ends_with(arr: string[], suffix: string): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (!arr[i].ends_with(suffix)) {
      return Fail("assert_all_ends_with: at index "
        + i.to_string() + " value \"" + arr[i]
        + "\" does not end with \"" + suffix + "\"");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_all_contain(arr: string[], needle: string): TestOutcome {
  let i: i32 = 0;
  while (i < arr.len()) {
    if (!arr[i].contains(needle)) {
      return Fail("assert_all_contain: at index "
        + i.to_string() + " value \"" + arr[i]
        + "\" does not contain \"" + needle + "\"");
    }
    i = i + 1;
  }
  return Pass;
}

pub function assert_starts_with_any(s: string, prefixes: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < prefixes.len()) {
    if (s.starts_with(prefixes[i])) {
      return Pass;
    }
    i = i + 1;
  }
  return Fail("assert_starts_with_any: \"" + s
    + "\" matches none of " + prefixes.len().to_string()
    + " prefixes");
}

pub function assert_ends_with_any(s: string, suffixes: string[]): TestOutcome {
  let i: i32 = 0;
  while (i < suffixes.len()) {
    if (s.ends_with(suffixes[i])) {
      return Pass;
    }
    i = i + 1;
  }
  return Fail("assert_ends_with_any: \"" + s
    + "\" matches none of " + suffixes.len().to_string()
    + " suffixes");
}

pub function assert_json_eq(actual: string, expected: string): TestOutcome {
  let av: Option[json.JsonValue] = json.json_parse(actual);
  let ev: Option[json.JsonValue] = json.json_parse(expected);
  match (av) {
    None => {
      return Fail("assert_json_eq: actual is not valid JSON: " + actual);
    },
    Some(a) => {
      match (ev) {
        None => {
          return Fail("assert_json_eq: expected is not valid JSON: " + expected);
        },
        Some(e) => {
          if (__json_value_eq(a, e)) {
            return Pass;
          }
          return Fail("assert_json_eq: documents differ\n"
            + "  expected: " + expected + "\n"
            + "  actual:   " + actual);
        }
      }
    }
  }
  return Pass;
}
```

### `31 pub function assert_json_has_key(json_text: string, key: string): TestOutcome`

```fern
<<31 pub function assert_json_has_key(json_text: string, key: string): TestOutcome>>=
pub function assert_json_has_key(json_text: string, key: string): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_has_key: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JObject(m) => {
          if (m.contains(key)) {
            return Pass;
          }
          return Fail("assert_json_has_key: key \"" + key
            + "\" not present in object of size "
            + m.len().to_string());
        },
        JNull => {
          return Fail("assert_json_has_key: top-level value is null, not object");
        },
        JBool(_) => {
          return Fail("assert_json_has_key: top-level value is bool, not object");
        },
        JNumber(_) => {
          return Fail("assert_json_has_key: top-level value is number, not object");
        },
        JString(_) => {
          return Fail("assert_json_has_key: top-level value is string, not object");
        },
        JArray(_) => {
          return Fail("assert_json_has_key: top-level value is array, not object");
        }
      }
    }
  }
  return Pass;
}

pub function assert_json_lacks_key(json_text: string, key: string): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_lacks_key: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JObject(m) => {
          if (!m.contains(key)) {
            return Pass;
          }
          return Fail("assert_json_lacks_key: key \"" + key
            + "\" unexpectedly present");
        },
        JNull => {
          return Fail("assert_json_lacks_key: top-level value is null, not object");
        },
        JBool(_) => {
          return Fail("assert_json_lacks_key: top-level value is bool, not object");
        },
        JNumber(_) => {
          return Fail("assert_json_lacks_key: top-level value is number, not object");
        },
        JString(_) => {
          return Fail("assert_json_lacks_key: top-level value is string, not object");
        },
        JArray(_) => {
          return Fail("assert_json_lacks_key: top-level value is array, not object");
        }
      }
    }
  }
  return Pass;
}

pub function assert_json_array_len(json_text: string, n: i32): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_array_len: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JArray(arr) => {
          let got: i32 = arr.len();
          if (got == n) {
            return Pass;
          }
          return Fail("assert_json_array_len: expected length "
            + n.to_string() + ", got " + got.to_string());
        },
        JNull => {
          return Fail("assert_json_array_len: top-level value is null, not array");
        },
        JBool(_) => {
          return Fail("assert_json_array_len: top-level value is bool, not array");
        },
        JNumber(_) => {
          return Fail("assert_json_array_len: top-level value is number, not array");
        },
        JString(_) => {
          return Fail("assert_json_array_len: top-level value is string, not array");
        },
        JObject(_) => {
          return Fail("assert_json_array_len: top-level value is object, not array");
        }
      }
    }
  }
  return Pass;
}
```

### `32 pub function assert_json_object_size(json_text: string, n: i32): TestOutcome`

```fern
<<32 pub function assert_json_object_size(json_text: string, n: i32): TestOutcome>>=
pub function assert_json_object_size(json_text: string, n: i32): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_object_size: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JObject(m) => {
          let got: i32 = m.len();
          if (got == n) {
            return Pass;
          }
          return Fail("assert_json_object_size: expected "
            + n.to_string() + " keys, got " + got.to_string());
        },
        JNull => {
          return Fail("assert_json_object_size: top-level value is null, not object");
        },
        JBool(_) => {
          return Fail("assert_json_object_size: top-level value is bool, not object");
        },
        JNumber(_) => {
          return Fail("assert_json_object_size: top-level value is number, not object");
        },
        JString(_) => {
          return Fail("assert_json_object_size: top-level value is string, not object");
        },
        JArray(_) => {
          return Fail("assert_json_object_size: top-level value is array, not object");
        }
      }
    }
  }
  return Pass;
}

pub function assert_json_eq_field_string(json_text: string, key: string, expected: string): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_eq_field_string: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JObject(m) => {
          if (!m.contains(key)) {
            return Fail("assert_json_eq_field_string: key \"" + key
              + "\" not present");
          }
          match (m.get_or(key, JNull)) {
            JString(s) => {
              if (s == expected) {
                return Pass;
              }
              return Fail("assert_json_eq_field_string: at \"" + key
                + "\" expected \"" + expected
                + "\", got \"" + s + "\"");
            },
            JNull => {
              return Fail("assert_json_eq_field_string: at \"" + key + "\" value is null, not string");
            },
            JBool(_) => {
              return Fail("assert_json_eq_field_string: at \"" + key + "\" value is bool, not string");
            },
            JNumber(_) => {
              return Fail("assert_json_eq_field_string: at \"" + key + "\" value is number, not string");
            },
            JArray(_) => {
              return Fail("assert_json_eq_field_string: at \"" + key + "\" value is array, not string");
            },
            JObject(_) => {
              return Fail("assert_json_eq_field_string: at \"" + key + "\" value is object, not string");
            }
          }
        },
        JNull => {
          return Fail("assert_json_eq_field_string: top-level value is null, not object");
        },
        JBool(_) => {
          return Fail("assert_json_eq_field_string: top-level value is bool, not object");
        },
        JNumber(_) => {
          return Fail("assert_json_eq_field_string: top-level value is number, not object");
        },
        JString(_) => {
          return Fail("assert_json_eq_field_string: top-level value is string, not object");
        },
        JArray(_) => {
          return Fail("assert_json_eq_field_string: top-level value is array, not object");
        }
      }
    }
  }
  return Pass;
}
```

### `33 pub function assert_json_eq_field_i32(json_text: string, key: string, expected: i32): TestOutcome`

```fern
<<33 pub function assert_json_eq_field_i32(json_text: string, key: string, expected: i32): TestOutcome>>=
pub function assert_json_eq_field_i32(json_text: string, key: string, expected: i32): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_eq_field_i32: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JObject(m) => {
          if (!m.contains(key)) {
            return Fail("assert_json_eq_field_i32: key \"" + key
              + "\" not present");
          }
          match (m.get_or(key, JNull)) {
            JNumber(s) => {
              match (s.parse_int()) {
                None => {
                  return Fail("assert_json_eq_field_i32: at \"" + key
                    + "\" value \"" + s + "\" not i32-parseable");
                },
                Some(got) => {
                  if (got == expected) {
                    return Pass;
                  }
                  return Fail("assert_json_eq_field_i32: at \"" + key
                    + "\" expected " + expected.to_string()
                    + ", got " + got.to_string());
                }
              }
            },
            JNull => {
              return Fail("assert_json_eq_field_i32: at \"" + key + "\" value is null, not number");
            },
            JBool(_) => {
              return Fail("assert_json_eq_field_i32: at \"" + key + "\" value is bool, not number");
            },
            JString(_) => {
              return Fail("assert_json_eq_field_i32: at \"" + key + "\" value is string, not number");
            },
            JArray(_) => {
              return Fail("assert_json_eq_field_i32: at \"" + key + "\" value is array, not number");
            },
            JObject(_) => {
              return Fail("assert_json_eq_field_i32: at \"" + key + "\" value is object, not number");
            }
          }
        },
        JNull => {
          return Fail("assert_json_eq_field_i32: top-level value is null, not object");
        },
        JBool(_) => {
          return Fail("assert_json_eq_field_i32: top-level value is bool, not object");
        },
        JNumber(_) => {
          return Fail("assert_json_eq_field_i32: top-level value is number, not object");
        },
        JString(_) => {
          return Fail("assert_json_eq_field_i32: top-level value is string, not object");
        },
        JArray(_) => {
          return Fail("assert_json_eq_field_i32: top-level value is array, not object");
        }
      }
    }
  }
  return Pass;
}

pub function assert_json_eq_field_bool(json_text: string, key: string, expected: boolean): TestOutcome {
  match (json.json_parse(json_text)) {
    None => {
      return Fail("assert_json_eq_field_bool: not valid JSON: " + json_text);
    },
    Some(v) => {
      match (v) {
        JObject(m) => {
          if (!m.contains(key)) {
            return Fail("assert_json_eq_field_bool: key \"" + key
              + "\" not present");
          }
          match (m.get_or(key, JNull)) {
            JBool(b) => {
              if (b == expected) {
                return Pass;
              }
              let ebs: string = "false";
              if (expected) {
                ebs = "true";
              }
              let gbs: string = "false";
              if (b) {
                gbs = "true";
              }
              return Fail("assert_json_eq_field_bool: at \"" + key
                + "\" expected " + ebs + ", got " + gbs);
            },
            JNull => {
              return Fail("assert_json_eq_field_bool: at \"" + key + "\" value is null, not bool");
            },
            JNumber(_) => {
              return Fail("assert_json_eq_field_bool: at \"" + key + "\" value is number, not bool");
            },
            JString(_) => {
              return Fail("assert_json_eq_field_bool: at \"" + key + "\" value is string, not bool");
            },
            JArray(_) => {
              return Fail("assert_json_eq_field_bool: at \"" + key + "\" value is array, not bool");
            },
            JObject(_) => {
              return Fail("assert_json_eq_field_bool: at \"" + key + "\" value is object, not bool");
            }
          }
        },
        JNull => {
          return Fail("assert_json_eq_field_bool: top-level value is null, not object");
        },
        JBool(_) => {
          return Fail("assert_json_eq_field_bool: top-level value is bool, not object");
        },
        JNumber(_) => {
          return Fail("assert_json_eq_field_bool: top-level value is number, not object");
        },
        JString(_) => {
          return Fail("assert_json_eq_field_bool: top-level value is string, not object");
        },
        JArray(_) => {
          return Fail("assert_json_eq_field_bool: top-level value is array, not object");
        }
      }
    }
  }
  return Pass;
}
```
