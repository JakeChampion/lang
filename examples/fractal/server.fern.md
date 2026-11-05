# Fractal: expose a numerical renderer over HTTP

This controller connects the Mandelbrot renderer to HTTP tiles and WebSocket delivery.
The numerical modules remain ordinary Fern files so benchmarks can test their kernels directly.
Follow their import links to inspect the perturbation, series, and escape-time algorithms.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Renderer state>>
<<Measure a render>>
<<Serve tiles and sockets>>
<<Handle socket requests>>
<<Parse typed requests>>
<<Cancel obsolete work>>
<<Report work and identify the binary>>
<<Mount the standalone application>>
```

## Renderer state

The state records completed tiles, active renders, connections, and elapsed work.
A render version identifies the binary so immutable tile URLs remain safe across deployments.

```fern
<<Renderer state>>=
import "std/time";
import "std/http";
import "std/http/router";
import "std/http/static";
import "std/http/websocket";
import "std/json";
import "std/platform";
import "std/serve";
import "std/i64";
import "std/number_parse";
import "std/result";
import "std/binary";
import "std/rand";
import "std/async";
import "std/clock";
import "std/array";
import "std/crypto";
import "std/hex";
import "./fractal";
import "./source";

const PORT: i32 = 8080;

pub struct Stats {
  instance: string,
  started_ms: i64,
  binary_bytes: i64,
  render_version: string,
  tiles: Cell[i64],
  iterations: Cell[i64],
  render_ns: Cell[i64],
  sockets: Cell[i64],
  active: Cell[i64],
  cancelled: Cell[string[]],
}

@derive(json.Json)
struct StatsView {
  instance: string,
  binary_bytes: i64,
  render_version: string,
  uptime_ms: i64,
  tiles: i64,
  iterations: i64,
  render_ms: i64,
  sockets: i64,
  active: i64,
  max_zoom: i32,
}

pub function stats(instance: string, started_ms: i64, binary_bytes: i64, version: string): Stats {
  return Stats {
    instance: instance,
    started_ms: started_ms,
    binary_bytes: binary_bytes,
    render_version: version,
    tiles: cell_new(0i64),
    iterations: cell_new(0i64),
    render_ns: cell_new(0i64),
    sockets: cell_new(0i64),
    active: cell_new(0i64),
    cancelled: cell_new([] as string[]),
  };
}
```

## Measure a render

A rendered tile includes elapsed time and its numerical statistics.
Track active work around the renderer call so cancellation and errors also release the active count.

```fern
<<Measure a render>>=
function (state: Stats) view(now_ms: i64): StatsView {
  return StatsView {
    instance: state.instance,
    binary_bytes: state.binary_bytes,
    render_version: state.render_version,
    uptime_ms: now_ms - state.started_ms,
    tiles: state.tiles.get(),
    iterations: state.iterations.get(),
    render_ms: state.render_ns.get() / 1000000,
    sockets: state.sockets.get(),
    active: state.active.get(),
    max_zoom: fractal.MAX_ZOOM,
  };
}

struct Rendered {
  tile: fractal.Tile,
  elapsed_ns: i64,
}

function (state: Stats) render(z: i32, x: i64, y: i64, key: Option[string]): Result[Rendered, fractal.RenderError] {
  let started_ns = now_ns();
  state.active.set(state.active.get() + 1);
  let next_yield = cell_new(0i64);
  let result = fractal.render_cancellable(z, x, y, (): boolean => request_stopped(state, key, next_yield));
  state.active.set(state.active.get() - 1);
  forget_cancel(state, key);
  let tile = result?;
  let elapsed_ns = now_ns() - started_ns;
  state.tiles.set(state.tiles.get() + 1);
  state.iterations.set(state.iterations.get() + tile.iterations);
  state.render_ns.set(state.render_ns.get() + elapsed_ns);
  return Ok(Rendered { tile: tile, elapsed_ns: elapsed_ns });
}
```

## Serve tiles and sockets

HTTP provides cacheable PNG tiles. WebSocket supplies a multiplexed binary delivery path.
The controller validates coordinates before work. Responses report timing and instance identity.

```fern
<<Serve tiles and sockets>>=
pub function routes[P: platform.Platform](state: Stats, web: string, sources: source.Page[]): router.Router[P] {
  let app = router.new[P]();
  app = app.get("/tile/{version}/{z}/{x}/{y}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => tile(state, req, params));
  app = app.get("/tile/{z}/{x}/{y}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect(f"/tile/{state.render_version}/{params.param("z")?}/{params.param("x")?}/{params.param("y")?}").with_header("Cache-Control", "no-store")));
  app = app.post("/cancel/{session}/{id}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let session_id = params.param("session")?;
    let id = params.param("id")?;
    let key = request_key(session_id, id).map_err((error: TokenError): http.HttpError => http.fail(400, "invalid cancellation token"))?;
    let cancelled = state.cancelled.get();
    if (!cancelled.contains(key)) {
      if (cancelled.len() >= 256) {
        cancelled = cancelled.drop(1);
      }
      state.cancelled.set(cancelled.append(key));
    }
    return Ok(http.bytes(204, []).with_header("Cache-Control", "no-store"));
  });
  app = app.get("/ws/{version}/{session}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let version = params.param("version")?;
    let session_id = params.param("session")?;
    if (version != state.render_version || request_key(session_id, "1").is_err()) {
      return Ok(http.not_found());
    }
    return Ok(http.respond(websocket.accept(req, websocket.options(), session(state, session_id))));
  });
  app = app.get("/api/config", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.json_of(state.view(plat.now().unix_millis())).with_header("Cache-Control", "no-store")));
  app = app.get("/api/stats", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.json_of(state.view(plat.now().unix_millis())).with_header("Cache-Control", "no-store")));
  app = app.get("/healthz", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.ok("ok")));
  app = app.get("/_src", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect("/_src/")));
  app = app.get("/_src/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let path = params.param("path")?;
    return Ok(source.serve(sources, path));
  });
  return app.get("/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(static.serve(req, static.dir(web), plat)));
}

function tile(state: Stats, req: HttpRequest, params: router.Params): Result[HttpResponse, dyn http.ToResponse] {
  if (params.param("version")? != state.render_version) {
    return Ok(http.not_found().with_header("Cache-Control", "no-store"));
  }
  let z = params.int("z")?;
  let x = coordinate(params, "x")?;
  let y = coordinate(params, "y")?;
  if (!fractal.valid(z, x, y)) {
    return Ok(http.bad_request("no such tile"));
  }
  let session_id = req.headers.get("X-Fern-Session").unwrap_or("");
  let request_id = req.headers.get("X-Fern-Request").unwrap_or("");
  let key: Option[string] = None;
  if (session_id.len() > 0 || request_id.len() > 0) {
    key = Some(request_key(session_id, request_id).map_err((error: TokenError): http.HttpError => http.fail(400, "invalid cancellation token"))?);
  }
  let rendered = state.render(z, x, y, key).map_err((error: fractal.RenderError): http.HttpError => render_http_error(error))?;
  let response = http.bytes(200, rendered.tile.png).with_content_type("image/png");
  if (state.binary_bytes > 0) {
    response = response.with_header("Cache-Control", "public, max-age=31536000, immutable");
    response = response.with_header("Netlify-CDN-Cache-Control", "public, durable, max-age=31536000, immutable");
  } else {
    response = response.with_header("Cache-Control", "no-store");
  }
  response = response.with_header("Server-Timing", timing(rendered));
  response = response.with_header("X-Fern-Instance", state.instance);
  response = response.with_header("X-Fern-Renderer", state.render_version);
  return Ok(response.with_header("X-Fern-Iterations", rendered.tile.iterations.to_string()));
}
```

## Handle socket requests

Each connection owns a session identifier. The server counts connection open and close events.
Text requests carry an identifier and tile coordinates. Replies prefix PNG data with that identifier.

```fern
<<Handle socket requests>>=
function session(state: Stats, session_id: string): websocket.Handlers {
  return websocket.Handlers {
    open: (connection: websocket.Conn): void => {
      state.sockets.set(state.sockets.get() + 1);
      let sent = connection.send_text(state.view(now_unix_ms()).to_json());
    },
    message: (connection: websocket.Conn, message: websocket.Message): void => on_message(state, session_id, connection, message),
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => { state.sockets.set(state.sockets.get() - 1); },
  };
}

function on_message(state: Stats, session_id: string, connection: websocket.Conn, message: websocket.Message): void {
  match (message) {
    Text(request) => {
      match (answer(state, session_id, request)) {
        Ok(response) => {
          let sent = connection.send_binary(response);
        },
        Err(Rendering(fractal.RenderError.Cancelled)) => {
          if let Ok(tile) = parse_request(request) {
            let sent = connection.send_text(f"{{\"cancelled\":{tile.id}}}");
          }
        },
        Err(Rendering(_)) => {
          connection.close(1011, "tile renderer failed");
        },
        Err(_) => {
          connection.close(1008, "invalid tile request");
        }
      }
    },
    Binary(bytes) => {
      connection.close(1003, "send tile requests as text");
    }
  }
}
```

## Parse typed requests

A parse failure is a `RequestError` with a cause. No integer sentinel represents failure.
Parse into wide integers, check coordinate limits, then convert to the wire identifier type.

```fern
<<Parse typed requests>>=
pub struct TileRequest { id: u32, z: i32, x: i64, y: i64 }

pub enum RequestError {
  InvalidFields,
  InvalidToken(TokenError),
  InvalidNumber(string, number_parse.ParseNumberError),
  OutOfRange,
  Rendering(fractal.RenderError),
}

pub function parse_request(request: string): Result[TileRequest, RequestError] {
  let fields = request.split(" ");
  if (fields.len() != 4) {
    return Err(InvalidFields);
  }
  let id = request_integer(fields[0], "id")?;
  let z = request_integer(fields[1], "zoom")?;
  let x = request_integer(fields[2], "x")?;
  let y = request_integer(fields[3], "y")?;
  if (!request_range(id, z) || !fractal.valid(z as i32, x, y)) {
    return Err(RequestError.OutOfRange);
  }
  return Ok(TileRequest { id: id as u32, z: z as i32, x: x, y: y });
}

fip function request_range(id: i64, z: i64): boolean {
  return id >= 1 && id <= 4294967295 && z >= 0 && z <= fractal.MAX_ZOOM as i64;
}

function answer(state: Stats, session_id: string, request: string): Result[u8[], RequestError] {
  let tile = parse_request(request)?;
  let key = request_key(session_id, tile.id.to_string()).map_err((error: TokenError): RequestError => InvalidToken(error))?;
  let rendered = state.render(tile.z, tile.x, tile.y, Some(key)).map_err((error: fractal.RenderError): RequestError => Rendering(error))?;
  let response = binary.append_u32_be([], tile.id);
  response = binary.append_u32_be(response, (rendered.elapsed_ns / 1000) as u32);
  response = binary.append_u64_be(response, rendered.tile.iterations as u64);
  response = binary.append_u32_be(response, state.tiles.get() as u32);
  response = binary.append_u32_be(response, (rendered.tile.compute_ns / 1000) as u32);
  response = binary.append_u32_be(response, (rendered.tile.color_ns / 1000) as u32);
  response = binary.append_u32_be(response, (rendered.tile.png_ns / 1000) as u32);
  return Ok(binary.append_bytes(response, rendered.tile.png));
}

function request_integer(text: string, field: string): Result[i64, RequestError] {
  return i64.parse_i64(text).map_err((error: number_parse.ParseNumberError): RequestError => InvalidNumber(field, error));
}

function coordinate(params: router.Params, name: string): Result[i64, http.HttpError] {
  let text = params.param(name)?;
  return i64.parse_i64(text).map_err((error: number_parse.ParseNumberError): http.HttpError => http.fail(400, f"path parameter {name} is not an integer"));
}

pub enum TokenError { InvalidSession, InvalidID(number_parse.ParseNumberError), InvalidIDRange }
```

## Cancel obsolete work

Cancellation keys combine a validated session and request identifier.
The renderer periodically checks the cancellation set and task state. Yield points let cancellation requests run.
A stopped render returns a typed error and does not increase the completed tile count.

```fern
<<Cancel obsolete work>>=
pub function request_key(session_id: string, id: string): Result[string, TokenError] {
  if (session_id.len() < 16 || session_id.len() > 64) {
    return Err(InvalidSession);
  }
  for c in session_id.as_bytes() {
    if (!(c >= b'a' && c <= b'z' || c >= b'A' && c <= b'Z' || c >= b'0' && c <= b'9' || c == b'-' || c == b'_')) {
      return Err(InvalidSession);
    }
  }
  let value = i64.parse_i64(id).map_err((error: number_parse.ParseNumberError): TokenError => InvalidID(error))?;
  if (value < 1 || value > 4294967295) {
    return Err(InvalidIDRange);
  }
  return Ok(f"{session_id}:{value}");
}

function forget_cancel(state: Stats, key: Option[string]): void {
  if let Some(token) = key {
    state.cancelled.set(state.cancelled.get().filter((value: string): boolean => value != token));
  }
}

function request_stopped(state: Stats, key: Option[string], next_yield: Cell[i64]): boolean {
  if (async.in_task() && now_ns() >= next_yield.get()) {
    next_yield.set(now_ns() + 4000000);
    let ready = async.wait_any([], Some(clock.instant().add(time.duration_millis(1))));
  }
  if (async.task_cancelled()) {
    return true;
  }
  if let Some(token) = key {
    return state.cancelled.get().contains(token);
  }
  return false;
}
```

## Report work and identify the binary

Map renderer errors to HTTP status codes at the boundary. Server-Timing reports elapsed work.
The executable hash gives tile URLs a stable build identity.

```fern
<<Report work and identify the binary>>=
function render_http_error(error: fractal.RenderError): http.HttpError {
  return match (error) { fractal.RenderError.Cancelled => http.fail(499, "tile request cancelled"), _ => http.fail(500, "tile renderer failed") };
}

function timing(rendered: Rendered): string {
  return f"render;dur={millis(rendered.elapsed_ns)}, compute;dur={millis(rendered.tile.compute_ns)}, color;dur={millis(rendered.tile.color_ns)}, png;dur={millis(rendered.tile.png_ns)}";
}

function millis(nanoseconds: i64): string {
  let microseconds = nanoseconds / 1000;
  let fraction = (1000 + microseconds % 1000).to_string();
  return f"{microseconds / 1000}.{fraction.drop(1)}";
}

function hex6(value: i32): string {
  let digits = "0123456789abcdef";
  let result = "";
  for index in 0..6 {
    let digit = value >> 20 - 4 * index & 15;
    result += digits.drop(digit).take(1);
  }
  return result;
}

function own_binary(): (i64, string) {
  let paths = ["/proc/self/exe"];
  if (args().len() > 0) {
    paths = paths.append(args()[0]);
  }
  for path in paths {
    if let Ok(bytes) = read_file_bytes(path) {
      return (bytes.len() as i64, hex.encode(crypto.sha256_new().update(bytes).finish()).take(24));
    }
  }
  let text = "";
  for name in ["fractal.fern", "kernel.fern", "linear.fern", "series.fern"] {
    if let Ok(source) = read_file(name) {
      text += source;
    }
  }
  return (0, hex.encode(crypto.sha256(text.as_bytes())).take(24));
}

// The standalone entry also mounts the app at the showcase path.
// Legacy endpoints remain available for existing API clients.
```

## Mount the standalone application

The standalone entry also serves the UI at `/fractal/`. The showcase uses the inner router.
The standalone server retains unprefixed API routes for the numerical and cancellation test harnesses.

```fern
<<Mount the standalone application>>=
pub function mounted_routes[P: platform.Platform](state: Stats, web: string, sources: source.Page[]): router.Router[P] {
  let inner = routes[P](state, web, sources);
  let app = router.new[P]();
  app = app.get("/{$}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect("/fractal/")));
  app = app.get("/fractal", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect("/fractal/")));
  app = app.route("/fractal/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let response = inner.handle(HttpRequest { ...req, path: req.path.drop(8) }, plat);
    if let Some(location) = response.headers.get("Location") {
      if (location.starts_with("/tile/")) {
        response = response.with_header("Location", "/fractal" + location);
      }
    }
    return Ok(response);
  });
  return app.route("/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(inner.handle(req, plat)));
}

function main(): i32 {
  let (binary_bytes, version) = own_binary();
  let state = stats(hex6(rand.next_i32()), now_unix_ms(), binary_bytes, version);
  match (source.load(".")) {
    Ok(sources) => {
      let handler = mounted_routes[platform.Host](state, "web", sources).handler();
      return serve.exit_code(serve.run_shutdown(PORT, serve.config(), handler, (reason: string): void => eprint("server: stopped, " + reason)));
    },
    Err(message) => {
      eprint("server: " + message);
      return 1;
    }
  }
}
```
