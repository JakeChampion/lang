---
title: Cookbook
description: Short, complete answers to the things you actually need on day one — files, stdin, flags, JSON, CSV, regular expressions, paths, dates, HTTP, tests.
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
    match (io.read_all_stdin()) {
        Ok(text) => {
            for line in text.lines() { print(line); }
        },
        Err(_) => { eprint("cannot read UTF-8 stdin"); return 1; },
    }
    return 0;
}
```

`io.read_input(path)` is the version that takes a path and treats `"-"`
as stdin — the convention most Unix filters follow.
Both text readers report malformed UTF-8 as an error. Use
`io.read_all_stdin_bytes()` or `io.read_input_bytes(path)` for binary data.

## Write a file

Everything fallible returns a `Result`, so `?` propagates a failure the
same way whatever the operation is — even when success carries nothing.
`()` is that nothing.

```fern
function save(path: string, body: string): Result[(), IoError] {
    write_file(path, body)?;
    return Ok(());
}

function main(): i32 {
    match (save("out.txt", "hello\n")) {
        Ok(_)  => { return 0; },
        Err(_) => { eprint("write failed"); return 1; }
    }
}
```

`remove_file`, `remove_dir_all` and `create_dir_all` have the same shape.
`create_dir_all` is `mkdir -p`: it builds every missing parent, and a
path that is already there is `Ok`, so writing into a layout you do not
have yet is two calls rather than a walk.

```fern
function save_into(dir: string, name: string, body: string): Result[(), IoError] {
    create_dir_all(dir)?;
    write_file(dir + "/" + name, body)?;
    return Ok(());
}
```

## Read an environment variable

`env` distinguishes "unset" from "set to empty", so it hands back an
`Option`.

```fern
function main(): i32 {
    let level: string = "info";
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
    let spec: cli.CliSpec = cli.cli_new("greet", "Say hello")
        .option("name", "n", "who to greet")
        .flag("loud", "l", "shout it");
    let parsed: cli.CliArgs = spec.parse(args());
    if (parsed.is_error()) {
        eprint(parsed.error_text());
        return 2;
    }
    let who: string = parsed.value_or("name", "world");
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
    let raw: string = "  frond, spore ,rhizome ";
    let cleaned: string[] = [];
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
    let names: string[] = ["elm", "ash", "elm", "beech"];
    let unique: string[] = set.set_of(names).to_array();
    let sorted: string[] = sort.sort_by(unique, sort.string_cmp);
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
    let counts: Map[string, i32] = Map {};
    for word in "the quick the lazy the dog".split(" ") {
        counts = counts.insert(word, counts.get_or(word, 0) + 1);
    }
    for (word, n) in counts {
        print(word + ": " + n.to_string());
    }
    return 0;
}
```

## Match a regular expression

`std/regex` searches by default; anchor with `^` and `$` to match the
whole string. `regex_captures` returns the match with each group's span,
and a group can be named.

```fern
import "std/regex";
import "std/option";

function main(): i32 {
    let line: string = "2026-10-07 ERROR disk full on /dev/sda1";
    let m: regex.RCaps = regex.regex_captures("^(?<date>\\d{4}-\\d{2}-\\d{2}) (?<level>[A-Z]+) (.*)$", line);
    if (!m.found) {
        eprint("not a log line");
        return 1;
    }
    print(m.group_named("level").unwrap_or(""));   // ERROR
    print(m.group(3).unwrap_or(""));               // disk full on /dev/sda1
    let masked: string = regex.regex_replace_all("[0-9]", "call 555-0100", "#").unwrap_or("");
    print(masked);                                 // call ###-####
    return 0;
}
```

`regex_match`, `regex_find_all`, `regex_split` and `regex_count` cover
the other common jobs. Offsets are byte indices, and the functions that
return text return an `Option` because a match can split a UTF-8
character.

## Parse JSON

```fern
import "std/json";
import "std/option";
import "std/i32";

function main(): i32 {
    let text: string = "{\"name\": \"fern\", \"stars\": 42}";
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
    let doc: JsonValue = JObject(Map {
        "name": JString("fern"),
        "stars": JNumber("42"),
        "tags": JArray([JString("cli"), JString("wasm")]),
    });
    print(json.json_encode(doc));
    return 0;
}
```

## Read CSV

`csv_parse` reads a whole RFC 4180 document, including quoted fields
that contain commas or newlines, into one `string[]` per record.
`csv_join` goes the other way and quotes a field only when it has to.

```fern
import "std/csv";
import "std/string";
import "std/i32";

function main(): i32 {
    let text: string = "name,qty\n\"Smith, J\",3\nLee,4\n";
    let rows: string[][] = csv.csv_parse(text);
    let total: i32 = 0;
    let i: i32 = 1;
    while (i < rows.len()) {
        let row: string[] = rows[i];
        if (row.len() == 2) {
            match (row[1].parse_int()) {
                Some(n) => { total = total + n; },
                None => { eprint("bad qty on row " + i.to_string()); return 1; },
            }
        }
        i = i + 1;
    }
    print(rows[1][0]);                               // Smith, J
    print(total.to_string());                        // 7
    print(csv.csv_join(["a,b", "say \"hi\""]));      // "a,b","say ""hi"""
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

`read_dir` leaves out `.` and `..`, and the names arrive in whatever order
the directory holds them — sort the result if that matters. `read_dir_all`
is the same call with the two dot entries kept, for a program that has to
show them where the directory put them rather than somewhere of its own
choosing. `read_dir_ino` is `read_dir` with each name's inode number beside
it, as a `DirEntry { name, ino }`, for a walk that orders or identifies
entries by inode without a `stat` per entry. `ino` is 0 where the platform
supplies none: WASI preview 2 carries no inode at all, so `lstat` reports 0
there too.

## Work with paths

`std/path` works on the string alone and never touches the filesystem.

```fern
import "std/path";

function main(): i32 {
    let p: string = path.path_join(["build", "out", "report.txt"]);
    print(p);                                   // build/out/report.txt
    print(path.path_parent(p));                 // build/out
    print(path.path_file_name(p));              // report.txt
    print(path.path_stem(p));                   // report
    print(path.path_extension(p));              // txt
    print(path.path_with_extension(p, "md"));   // build/out/report.md
    print(path.path_clean("a/./b/../c//d/"));   // a/c/d
    return 0;
}
```

## Dates and elapsed time

`Date`, `Instant` and `Duration` are separate built-in types, so a
calendar day and a point in time cannot be mixed up. `std/time` holds
the functions that make and convert them.

```fern
import "std/time";

function main(): i32 {
    let start: Instant = time.instant_now();
    let d: Date = time.date_make(2028, 2, 27);
    print(d.add_days(3).format_iso());                // 2028-03-01
    print(d.weekday_name());                          // Sunday
    print(time.days_in_month(2028, 2).to_string());   // 29
    print(time.instant_now().format_rfc3339());       // the current UTC time
    print("took " + time.instant_now().duration_since(start).to_string());
    return 0;
}
```

`std/tz` adds real time zones with their daylight-saving transitions.

## Run another program

Native targets only — the wasm target rejects `subprocess` at build time
rather than at runtime.

```fern
function main(): i32 {
    let r = subprocess("git", ["rev-parse", "--short", "HEAD"], "");
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

`std/fetch` speaks HTTP/1.1 over the socket primitives, resolving the
host through `std/dns`:

```fern
import "std/fetch";

function main(): i32 {
    match (fetch.send(fetch.get("http://example.com/"))) {
        Ok(resp) => {
            match (resp.body_text()) {
                Some(text) => { print(text); },
                None => { print("body is not valid UTF-8"); },
            }
        },
        Err(e) => { print("fetch failed: " + e.message()); },
    }
    return 0;
}
```

The status is data on the response (`resp.status`, or
`resp.ok_or_status()` to treat anything outside 2xx as an error), and
`FetchError` says which phase failed: the URL, DNS, the connect, a
timeout, the protocol. Bodies are bytes, not text: an upstream can serve
a PNG or a truncated UTF-8 sequence, so `body_text()` is a decode that
can fail. Inside an HTTP handler, send through the platform value
instead, `plat.http(req)`, so a test can pass a
`mock_platform.MockPlatform` whose `http_set` records the response to
return. `fetch.fetch_future` gives you a future you can hand to
`async.gather` to overlap several requests on one thread.

## Serve HTTP

A program with a `handle` function and no `main` is a server: Fern
synthesises the `main` that listens on `$PORT` (8080 when unset), or
exports the WASI HTTP interface when you build for `wasm32-wasi-http`.

```fern
import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/health") {
        return http.ok("ok");
    }
    return http.not_found();
}
```

```bash
fern -target x86-64-linux -o server server.fern && PORT=3000 ./server
fern -target wasm32-wasi-http -o server.wasm server.fern && wasmtime serve server.wasm
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
    let r: test.TestRunner = test.test_new("slug");
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
# tests 1
# pass 1
# fail 0
```

The [testing tutorial](../tutorial/testing/) has the rest of the assertion
family, skips, sub-suites and golden files.
