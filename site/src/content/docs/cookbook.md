---
title: Cookbook
description: Short, complete answers to the things you actually need on day one — files, stdin, flags, JSON, HTTP, tests.
---

Every recipe here is a complete program, type-checked and run before it
was written down. Copy one, change the strings, and you have a working
tool.

## Read a file

`read_file` returns a `Result`, so the failure path is a branch rather
than an exception.

```fern
function main(): i32 {
    match (read_file("notes.txt")) {
        Ok(text) => { print(text); return 0; },
        Err(_)   => { eprint("cannot read notes.txt"); return 1; }
    }
}
```

## Read standard input

```fern
import "std/string";
import "std/io";

function main(): i32 {
    var text: string = io.read_all_stdin();
    for line in text.lines() {
        print(line);
    }
    return 0;
}
```

`io.read_input(path)` is the version that takes a path and treats `"-"`
as stdin — the convention most Unix filters follow.

## Write a file

`write_file` returns `Option[IoError]`: `None` means it worked.

```fern
function main(): i32 {
    match (write_file("out.txt", "hello\n")) {
        Some(_) => { eprint("write failed"); return 1; },
        None    => { return 0; }
    }
}
```

## Read an environment variable

`env` distinguishes "unset" from "set to empty", so it hands back an
`Option`.

```fern
function main(): i32 {
    var level: string = "info";
    match (env("LOG_LEVEL")) {
        Some(v) => { level = v; },
        None    => {}
    }
    print(level);
    return 0;
}
```

## Parse command-line flags

`std/cli` builds a spec, parses `args()` against it, and can print usage
for you.

```fern
import "std/cli";

function main(): i32 {
    var spec: cli.CliSpec = cli.cli_new("greet", "Say hello")
        .option("name", "n", "who to greet")
        .flag("loud", "l", "shout it");
    var parsed: cli.CliArgs = spec.parse(args());
    if (parsed.is_error()) {
        eprint(parsed.error_text());
        return 2;
    }
    var who: string = parsed.value_or("name", "world");
    if (parsed.is_set("loud")) {
        print("HELLO, " + who + "!");
    } else {
        print("hello, " + who);
    }
    return 0;
}
```

## Split, trim and join

```fern
import "std/string";

function main(): i32 {
    var raw: string = "  frond, spore ,rhizome ";
    var cleaned: string[] = [];
    for part in raw.split(",") {
        cleaned = cleaned.append(part.trim());
    }
    print(cleaned.join(" | "));
    return 0;
}
```

`append` returns the array rather than mutating it in place — assign the
result back, as above.

## Sort and de-duplicate

```fern
import "std/sort";
import "std/set";
import "std/string";

function main(): i32 {
    var names: string[] = ["elm", "ash", "elm", "beech"];
    var unique: string[] = set.set_of(names).to_array();
    var sorted: string[] = sort.sort_by(unique, sort.string_cmp);
    print(sorted.join(", "));   // ash, beech, elm
    return 0;
}
```

## Count with a map

Map iteration order is insertion order, and that's part of the contract.

```fern
import "core/map";
import "std/string";
import "std/i32";

function main(): i32 {
    var counts: Map[string, i32] = Map {};
    for word in "the quick the lazy the dog".split(" ") {
        counts = counts.insert(word, counts.get_or(word, 0) + 1);
    }
    for (word, n) in counts {
        print(word + ": " + n.to_string());
    }
    return 0;
}
```

## Parse JSON

```fern
import "std/json";
import "std/option";
import "std/i32";

function main(): i32 {
    var text: string = "{\"name\": \"fern\", \"stars\": 42}";
    match (json.json_parse(text)) {
        Some(doc) => {
            print(json.json_get_string(doc, "name").unwrap_or("(anonymous)"));
            print(json.json_get_i32(doc, "stars").unwrap_or(0).to_string());
            return 0;
        },
        None => { eprint("malformed JSON"); return 1; }
    }
}
```

## Build JSON

`JsonValue` is a built-in enum, so a document is an ordinary value.

```fern
import "std/json";
import "core/map";

function main(): i32 {
    var doc: JsonValue = JObject(Map {
        "name": JString("fern"),
        "stars": JNumber("42"),
        "tags": JArray([JString("cli"), JString("wasm")]),
    });
    print(json.json_encode(doc));
    return 0;
}
```

## List a directory

```fern
import "std/string";

function main(): i32 {
    match (read_dir(".")) {
        Ok(names) => {
            for name in names {
                if (name.ends_with(".fern")) { print(name); }
            }
            return 0;
        },
        Err(_) => { eprint("cannot list directory"); return 1; }
    }
}
```

## Run another program

Native targets only — the wasm target rejects `subprocess` at build time
rather than at runtime.

```fern
function main(): i32 {
    var r = subprocess("git", ["rev-parse", "--short", "HEAD"], "");
    if (r.exit_code != 0) {
        eprint(r.stderr);
        return r.exit_code;
    }
    print(r.stdout);
    return 0;
}
```

The third argument is what to feed the child on stdin.

## Fetch a URL

`std/fetch` speaks HTTP/1.1 over the socket primitives. There is no DNS
resolver in the standard library yet, so the host is an address:

```fern
import "std/fetch";

function main(): i32 {
    var host: i32 = fetch.ipv4(93, 184, 216, 34);
    var resp: string = fetch.fetch_get(host, 80, "/");
    print(fetch.http_body(resp));
    return 0;
}
```

`fetch.http_status(resp)` reads the status line, and `fetch.fetch_future`
gives you a future you can hand to `async.gather` to overlap several
requests on one thread.

## Serve HTTP

A program with a `handle` function and no `main` is a server: Fern
synthesises the `main` that listens, or exports the WASI HTTP interface
when you build for `wasi-http`.

```fern
import "std/http";
import "std/tcp";

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/health") {
        return http.http_response_ok("ok");
    }
    return http.http_response_not_found();
}
```

The [HTTP tutorial](../tutorial/http-server/) covers routing, JSON bodies
and headers.

## Write a test

Tests are ordinary programs. `std/test` prints TAP-13 and exits non-zero
if anything failed.

```fern
import "std/test";
import "std/string";

function slugify(s: string): string {
    return s.trim().to_lower().replace(" ", "-");
}

function test_slugify(): test.TestOutcome {
    return test.assert_eq(slugify("  Hello World "), "hello-world");
}

function main(): i32 {
    var r: test.TestRunner = test.test_new("slug");
    r = r.it("lowercases and hyphenates", test_slugify);
    return r.finish();
}
```

```bash
$ fern -interp slug_test.fern
TAP version 13
# Suite: slug
ok 1 - lowercases and hyphenates
1..1
```

The [testing tutorial](../tutorial/testing/) has the rest of the assertion
family, skips, sub-suites and golden files.
