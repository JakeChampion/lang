# Benchmark and handshake endpoints

These routes run the native half of three labs: the JSON lab, the compression bench, and the post-quantum handshake.
The browser runs the other half from the same Fern modules compiled to WebAssembly.

```fern
<<*>>=
<<Imports and budget>>
<<JSON benchmark>>
<<Compression benchmark>>
<<Handshake exchange>>
```

## Imports and budget

One process serves every application, and a benchmark holds the event loop while it measures.
The JSON benchmark therefore parses at most 4 MiB per parser, and the compression benchmark encodes at most 1 MiB per codec.
A benchmark also waits 1.5 seconds after the previous one, so repeated requests cannot hold up the game rooms.

The server generates each corpus from the request's seed and size. The request carries no document, so the 16 KiB body limit does not apply.

```fern
<<Imports and budget>>=
import "std/http";
import "std/json";
import "std/hex";
import "std/result";
import "./common";
import "./measure";
import "./handshake";

pub struct State { last: Cell[i64] }

pub function state(): State {
  return State { last: cell_new(0i64) };
}

function admit(state: State): Result[i64, http.HttpError] {
  let now = monotonic_ns();
  let last = state.last.get();
  if (last != 0 && now - last < 1500000000) {
    return Err(http.fail(429, "Another benchmark ran a moment ago. Try again in two seconds."));
  }
  state.last.set(now);
  return Ok(now);
}

function rounds(budget: i32, size: i32, most: i32): i32 {
  return (budget / size).max(1).min(most);
}
```

## JSON benchmark

`GET /json/bench?seed=S&size=N` generates the corpus, then parses it with the tree parser and with the tape parser.
The response reports each parser's total time and rounds, the value count, and the corpus checksum.

```fern
<<JSON benchmark>>=
@derive(json.Json)
struct Timing { rounds: i32, ns: i64, values: i32 }

@derive(json.Json)
struct JsonResult { bytes: i32, checksum: i32, tree: Timing, tape: Timing }

pub function json_bench(state: State, req: HttpRequest): Result[HttpResponse, http.HttpError] {
  let seed = common.integer(req, "seed", 1, 0, 1000000)?;
  let size = common.integer(req, "size", 65536, 1024, 1048576)?;
  admit(state)?;
  let text = measure.json_document(seed, size);
  let count = rounds(4194304, text.len(), 32);
  let started = monotonic_ns();
  let tree = measure.parse_tree(text, count);
  let middle = monotonic_ns();
  let tape = measure.parse_tape(text, count);
  let ended = monotonic_ns();
  return Ok(http.json_of(JsonResult {
    bytes: text.len(),
    checksum: measure.checksum(text.bytes()),
    tree: Timing { rounds: count, ns: middle - started, values: tree },
    tape: Timing { rounds: count, ns: ended - middle, values: tape },
  }).with_header("Cache-Control", "no-store"));
}
```

## Compression benchmark

`GET /compress/bench?seed=S&size=N` generates the access log, then encodes and decodes it with each codec.
Each decode must reproduce the corpus checksum. The response reports a codec that fails the round trip.

```fern
<<Compression benchmark>>=
@derive(json.Json)
struct Codec { name: string, bytes: i32, rounds: i32, encode_ns: i64, decode_ns: i64, lossless: boolean }

@derive(json.Json)
struct CompressResult { bytes: i32, checksum: i32, codecs: Codec[] }

pub function compress_bench(state: State, req: HttpRequest): Result[HttpResponse, http.HttpError] {
  let seed = common.integer(req, "seed", 1, 0, 1000000)?;
  let size = common.integer(req, "size", 65536, 1024, 1048576)?;
  admit(state)?;
  let log = measure.access_log(seed, size);
  let sum = measure.checksum(log);
  let count = rounds(1048576, log.len(), 16);
  let names: string[] = ["gzip", "Brotli", "zstd"];
  let codecs: Codec[] = [];
  for codec in 0..3 {
    let started = monotonic_ns();
    let packed = measure.compress(codec, log, count);
    let middle = monotonic_ns();
    let decoded = measure.decompress(codec, packed, log.len(), count);
    let ended = monotonic_ns();
    codecs = codecs.append(Codec { name: names[codec], bytes: packed.len(), rounds: count, encode_ns: middle - started, decode_ns: ended - middle, lossless: decoded == sum });
  }
  return Ok(http.json_of(CompressResult { bytes: log.len(), checksum: sum, codecs: codecs }).with_header("Cache-Control", "no-store"));
}
```

## Handshake exchange

`POST /handshake/exchange` receives the browser's ML-KEM-768 encapsulation key and X25519 public key in hexadecimal.
The server draws its random values from the operating system and answers with the values the browser needs to derive the same key.
The request body is about 2.5 KiB, inside the 16 KiB limit.

```fern
<<Handshake exchange>>=
@derive(json.FromJson)
struct Offer { ek: string, x: string }

@derive(json.Json)
struct Answer { ciphertext: string, x: string, confirmation: string, nonce: string, sealed: string, server_us: i64 }

pub function exchange(req: HttpRequest): Result[HttpResponse, http.HttpError] {
  let started = monotonic_ns();
  let offer = http.body_json[Offer](req).map_err((e: http.BodyError): http.HttpError => http.fail(400, "Send the ek and x keys as JSON."))?;
  let Ok(ek) = hex.decode(offer.ek) else {
    return Err(http.fail(400, "The encapsulation key is not hexadecimal."));
  };
  let Ok(client_x) = hex.decode(offer.x) else {
    return Err(http.fail(400, "The X25519 key is not hexadecimal."));
  };
  let text = "Hello from native Fern. Only the holder of this session key can read this message.".bytes();
  let answer = handshake.reply(ek, client_x, random_bytes(32), random_bytes(32), random_bytes(12), text).map_err((e: string): http.HttpError => http.fail(400, e))?;
  return Ok(http.json_of(Answer {
    ciphertext: hex.encode(answer.ciphertext),
    x: hex.encode(answer.server_x),
    confirmation: hex.encode(answer.confirmation),
    nonce: hex.encode(answer.nonce),
    sealed: hex.encode(answer.sealed),
    server_us: (monotonic_ns() - started) / 1000,
  }).with_header("Cache-Control", "no-store"));
}
```
