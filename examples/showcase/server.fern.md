# One native server, fifteen web applications

This is the entry point for the Fern web showcase. It mounts each application under a separate URL prefix.
The homepage and browser modules are static files. Fern owns the live state, protocols, images, audio, and HTML forms.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Imports and state>>
<<Source registry>>
<<HTTP routes>>
<<Binary identity>>
<<Start the server>>
<<Prepare lesson assets>>
```

## Imports and state

The router receives one explicit state record. Each application owns bounded process-local state.
Multiple server instances require process affinity for shared rooms. Blockyard can retain block edits on a persistent volume.
Its world directory assigns separate worlds to separate server processes.

```fern
<<Imports and state>>=
// One native server hosts the complete Fern application showcase.
import "std/http";
import "std/http/router";
import "std/http/static";
import "std/http/compress";
import "std/serve";
import "std/time";
import "std/platform";
import "std/json";
import "std/result";
import "std/option";
import "std/crypto";
import "../../compiler/util";
import "../fractal/server" as fractal;
import "../fractal/source";
import "./common";
import "./rally";
import "./gather";
import "./blockyard";
import "./mosaic";
import "./wave";
import "./notes";
import "./metrics";
import "./encoding";
import "./studio";
import "./labs";

pub struct State { studios: studio.State, labs: labs.State, voxels: blockyard.State, arena: gather.State, games: rally.State, canvases: mosaic.State, guestbook: notes.State, metrics: metrics.State, fractal: fractal.Stats }

@derive(json.Json)
struct Meta { apps: i32, instance: string, binary_bytes: i64, uptime_ms: i64, requests: i64 }

pub function state(binary_bytes: i64, version: string): State {
  let games: rally.State = rally.state();
  let canvases: mosaic.State = mosaic.state();
  let renderer: fractal.Stats = fractal.stats(common.token().take(8), now_unix_ms(), binary_bytes, version);
  let arena = gather.state(renderer.instance);
  let voxels = blockyard.state(renderer.instance);
  let studios = studio.state();
  let lab_state = labs.state();
  let telemetry: metrics.State = metrics.state(canvases, games, renderer, arena, voxels, studios, lab_state);
  return State {
    studios: studios, labs: lab_state, voxels: voxels, arena: arena, games: games, canvases: canvases, guestbook: notes.state(), metrics: telemetry,
    fractal: renderer,
  };
}
```

## Source registry

Only registered application files enter the source browser. A URL cannot read an arbitrary file.
Literate modules use their canonical `.fern.md` names. Old `.fern` source links remain valid aliases.

```fern
<<Source registry>>=
pub function files(): string[] {
  return [
    "fractal/server.fern.md", "fractal/fractal.fern", "fractal/kernel.fern", "fractal/linear.fern", "fractal/series.fern", "fractal/source.fern",
    "fractal/web/app.js", "fractal/web/delivery.mjs", "fractal/web/fullscreen.mjs", "fractal/web/style.css", "fractal/web/index.html",
    "showcase/server.fern.md", "showcase/common.fern.md", "showcase/rally.fern.md", "showcase/mosaic.fern.md", "showcase/wave.fern.md", "showcase/notes.fern.md", "showcase/metrics.fern.md", "showcase/encoding.fern.md", "showcase/live.fern.md", "showcase/swarm.fern.md", "showcase/gather.fern.md", "showcase/voxel.fern.md", "showcase/blockyard.fern.md", "showcase/blockyard_world.fern.md", "showcase/blockyard_store.fern.md",
    "showcase/foundry.fern.md", "showcase/patchbay.fern.md", "showcase/prism.fern.md", "showcase/fernbase.fern.md", "showcase/circuit.fern.md", "showcase/lantern.fern.md", "showcase/studio.fern.md", "showcase/labs.fern.md", "showcase/labs_store.fern.md",
    "showcase/web/foundry/index.html", "showcase/web/patchbay/index.html", "showcase/web/prism/index.html", "showcase/web/fernbase/index.html", "showcase/web/circuit/index.html", "showcase/web/lantern/index.html", "showcase/web/labs/lantern.mjs", "showcase/web/labs/patchbay.mjs", "showcase/web/labs/prism-worker.mjs", "showcase/web/labs/simulation-worker.mjs", "showcase/web/labs/circuit.mjs", "showcase/web/labs/prism.mjs", "showcase/web/labs/session.mjs", "showcase/web/labs/rpc.mjs", "showcase/web/labs/foundry.mjs", "showcase/web/labs/patchbay-worklet.mjs", "showcase/web/labs/style.css", "showcase/web/labs/previews.mjs", "showcase/web/labs/shell.mjs", "showcase/web/labs/fernbase.mjs",
    "showcase/web/index.html", "showcase/web/home.mjs", "showcase/web/shared/showcase.css", "showcase/web/shared/ui.mjs", "showcase/web/shared/wasm.mjs",
    "showcase/web/blockyard/index.html", "showcase/web/blockyard/style.css", "showcase/web/blockyard/app.mjs", "showcase/web/blockyard/protocol.mjs", "showcase/web/blockyard/mesh.mjs", "showcase/web/blockyard/mesh-worker.mjs", "showcase/web/blockyard/renderer.mjs",
    "showcase/web/rally/index.html", "showcase/web/rally/app.mjs", "showcase/web/rally/prediction.mjs", "showcase/web/mosaic/index.html", "showcase/web/mosaic/app.mjs",
    "showcase/web/gather/index.html", "showcase/web/gather/app.mjs", "showcase/web/gather/protocol.mjs", "showcase/web/gather/style.css",
    "showcase/web/swarm/index.html", "showcase/web/swarm/app.mjs", "showcase/web/swarm/kernel.mjs", "showcase/web/swarm/audio-worker.mjs", "showcase/web/swarm/style.css",
    "showcase/web/wave/index.html", "showcase/web/wave/app.mjs", "showcase/web/pulse/index.html", "showcase/web/pulse/app.mjs",
  ];
}
```

## HTTP routes

The fractal router receives a path with `/fractal` removed. Redirects receive the mount prefix again.
Other routes connect typed requests to application functions. The router converts typed errors into HTTP responses.

Prepared lesson files use the same static negotiation as other assets. Development falls back to live woven HTML.

The outer middleware restores a configured proxy header before route selection. It compresses eligible whole responses afterward.
Streams keep their body type. The middleware records response status and time for Pulse.

```fern
<<HTTP routes>>=
pub function routes[P: platform.Platform](state: State, root: string, pages: source.Page[]): router.Router[P] {
  let original_encoding: string = env("FERN_ORIGINAL_ACCEPT_ENCODING").unwrap_or("Netlify-Original-Accept-Encoding");
  let app: router.Router[P] = router.new[P]();
  let fractals: router.Router[P] = fractal.routes[P](state.fractal, root + "/fractal/web", pages);
  app = app.get("/fractal", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect("/fractal/")));
  app = app.route("/fractal/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let response: HttpResponse = fractals.handle(HttpRequest { ...req, path: req.path.drop(8) }, plat);
    if let Some(location) = response.headers.get("Location") {
      if (location.starts_with("/tile/")) {
        response = response.with_header("Location", "/fractal" + location);
      }
    }
    return Ok(response);
  });
  app = app.get("/api/meta", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let meta: Meta = Meta { apps: 15, instance: state.metrics.instance, binary_bytes: state.fractal.binary_bytes, uptime_ms: (now_ns() - state.metrics.started) / 1000000, requests: state.metrics.requests.get() };
    return Ok(http.json_of(meta).with_header("Cache-Control", "no-store"));
  });
  app = app.get("/healthz", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.ok("ok")));
  app = app.get("/blockyard", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let name = common.query(req, "world", "commons");
    if (!common.room_name(name)) {
      return Err(http.fail(400, "Invalid world name."));
    }
    return Ok(http.redirect("/blockyard/?world=" + name));
  });
  app = app.get("/blockyard/{$}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    if let Some(target) = blockyard.destination(state.voxels, req)? {
      return Ok(http.redirect(target));
    }
    return Ok(static.serve(HttpRequest { ...req, path: "/blockyard/index.html" }, static.dir(root + "/showcase/web"), plat));
  });
  app = app.get("/blockyard/worlds", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(blockyard.directory(state.voxels)));
  app = app.get("/blockyard/status", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(blockyard.status(state.voxels, req)?));
  app = app.get("/blockyard/chunk", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(blockyard.chunk(state.voxels, req)?));
  app = app.get("/blockyard/ws", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => blockyard.socket(state.voxels, req));
  app = app.get("/prism/preview.png", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let prepared = static.serve(req, static.dir(root + "/showcase/web"), plat);
    if (prepared.status != 404) {
      return Ok(prepared);
    }
    return Ok(labs.preview(state.labs)?);
  });
  app = app.get("/prism/reference", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(labs.reference(req)?));
  for name in ["foundry", "patchbay", "circuit"] {
    let application = name;
    app = app.post("/" + name + "/checkpoint", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(studio.checkpoint(state.studios, application, req)?));
    app = app.get("/" + name + "/ws", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => studio.socket(state.studios, application, req));
  }
  for name in ["fernbase", "lantern"] {
    let application = name;
    app = app.get("/" + name + "/ws", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => labs.socket(state.labs, application, req));
    for verb in ["state", "search", "export"] {
      let action = verb;
      app = app.get("/" + name + "/" + verb, (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(labs.handle(state.labs, application, action, req)?));
    }
    for verb in ["query", "import", "recover"] {
      let action = verb;
      app = app.post("/" + name + "/" + verb, (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(labs.handle(state.labs, application, action, req)?));
    }
  }
  app = app.get("/gather/ws", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => gather.socket(state.arena, req));
  app = app.get("/gather/status", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(gather.status(state.arena)));
  app = app.get("/rally/ws", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => rally.socket(state.games, req));
  app = app.get("/mosaic/ws", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => mosaic.socket(state.canvases, req));
  app = app.get("/mosaic/state", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let room: mosaic.Room = mosaic.room(state.canvases, common.query(req, "room", "commons"))?;
    return Ok(http.json_of(mosaic.snapshot(room)).with_header("Cache-Control", "no-store"));
  });
  app = app.get("/mosaic/events", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let room: mosaic.Room = mosaic.room(state.canvases, common.query(req, "room", "commons"))?;
    return Ok(mosaic.events(room, req));
  });
  app = app.post("/mosaic/paint", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let room: mosaic.Room = mosaic.room(state.canvases, common.query(req, "room", "commons"))?;
    let stroke: mosaic.Stroke = http.body_json[mosaic.Stroke](req)?;
    return Ok(http.json_of(mosaic.paint(room, stroke)?).with_header("Cache-Control", "no-store"));
  });
  app = app.get("/mosaic/export.png", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(mosaic.export_png(mosaic.room(state.canvases, common.query(req, "room", "commons"))?)?));
  app = app.get("/wave/render.wav", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(wave.audio(req)?));
  app = app.get("/notes/{$}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let notice: string = "";
    if (common.query(req, "posted", "") == "1") {
      notice = "Your note is on the wall. Thanks for stopping by.";
    }
    return Ok(notes.document(state.guestbook, req, notice, 200));
  });
  app = app.post("/notes/{$}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(notes.post(state.guestbook, req)));
  app = app.get("/notes/feed.xml", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(notes.feed(state.guestbook)));
  app = app.get("/pulse/snapshot", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.json_of(metrics.snapshot(state.metrics)).with_header("Cache-Control", "no-store")));
  app = app.get("/pulse/events", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(metrics.events(state.metrics)));
  app = app.get("/pulse/metrics", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(metrics.prometheus(state.metrics)));
  app = app.get("/_src", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect("/_src/")));
  app = app.get("/_src/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => {
    let name: string = params.param("path")?;
    for page in pages {
      if (page.name == name || page.name == name + ".md") {
        let prepared: HttpResponse = static.serve(HttpRequest { ...req, path: "/" + page.name + ".html" }, static.dir(root + "/showcase/web/.source"), plat);
        if (prepared.status != 404) {
          return Ok(prepared);
        }
        return Ok(http.html(page.html).with_header("Cache-Control", "no-cache"));
      }
    }
    return Ok(source.serve(pages, name));
  });
  for name in ["rally", "mosaic", "wave", "notes", "pulse", "swarm", "gather", "foundry", "patchbay", "prism", "fernbase", "circuit", "lantern"] {
    let destination: string = "/" + name + "/";
    app = app.get("/" + name, (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(http.redirect(destination)));
  }
  app = app.get("/assets/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(static.serve(HttpRequest { ...req, path: req.path.drop(7) }, static.dir(root + "/showcase/web/shared"), plat)));
  app = app.route("/{path...}", (req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] => Ok(static.serve(req, static.dir(root + "/showcase/web"), plat)));
  return app.wrap((req: HttpRequest, plat: P, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] => {
    let started: i64 = now_ns();
    let normalized: HttpRequest = encoding.request(req, original_encoding);
    let response: HttpResponse = compress.response(normalized, http.respond(next(normalized)), compress.options());
    if (original_encoding != "") {
      response = response.with_vary(original_encoding);
    }
    metrics.record(state.metrics, req, response, started);
    return Ok(response.with_header("X-Content-Type-Options", "nosniff").with_header("Referrer-Policy", "same-origin"));
  });
}
```

## Binary identity

Hash the executable of this process to identify the fractal render implementation. Linux supplies the executable through procfs.
If the executable is unavailable, use a process token so cache keys do not incorrectly identify another build.

```fern
<<Binary identity>>=
function identity(): (i64, string) {
  match (read_file_bytes("/proc/self/exe").or_else((e: IoError): Result[u8[], IoError] => read_file_bytes(args()[0]))) {
    Ok(bytes) => {
      return (bytes.len() as i64, crypto.sha256_new().update(bytes).final_hex().take(24));
    },
    Err(_) => {
      return (0, common.token());
    }
  }
}
```

## Start the server

Load source pages before the server accepts requests. A missing lesson fails startup with an explicit error.
Check route conflicts before the server starts. Limit request bodies to 16 KiB and simultaneous connections to 8192.
`serve.run_shutdown` supports the inherited listener used by container functions and the HTTP test harness.

```fern
<<Start the server>>=
function main(): i32 {
  if (args().len() == 4 && args()[1] == "--prepare") {
    match (prepare(args()[2], args()[3])) {
      Ok(_) => {
        return 0;
      },
      Err(message) => {
        eprint(message);
        return 1;
      }
    }
  }
  let root: string = "examples";
  if (args().len() > 1) {
    root = args()[1];
  }
  let identity: (i64, string) = identity();
  match (source.load_files(root, files())) {
    Ok(pages) => {
      let app: router.Router[platform.Host] = routes[platform.Host](state(identity.0, identity.1), root, pages);
      match (app.check()) {
        Ok(_) => {
          let config: serve.Config = serve.config();
          config = serve.Config { ...config, limits: http.HttpLimits { ...config.limits, body: 16384 }, max_connections: 8192, max_connections_per_ip: 8192, max_in_flight: 16384, background_deadline: time.duration_seconds(0) };
          return serve.run_shutdown(8080, config, app.handler(), (reason: string): void => eprint("showcase: " + reason));
        },
        Err(error) => {
          eprint(error.message());
          return 1;
        }
      }
    },
    Err(message) => {
      eprint(message);
      return 1;
    }
  }
}
```

## Prepare lesson assets

The build uses this mode to weave every registered source page into HTML.
A separate build script compresses these immutable bytes with three standard encoders. Node is a build dependency only.

```fern
<<Prepare lesson assets>>=
function prepare(root: string, destination: string): Result[void, string] {
  let pages: source.Page[] = source.load_files(root, files())?;
  let preview_directory = util.dir_of(destination) + "/prism";
  create_dir_all(preview_directory).map_err((e: IoError): string => "Cannot create preview directory.")?;
  let preview = labs.thumbnail().map_err((e: http.HttpError): string => e.detail)?;
  write_file_bytes(preview_directory + "/preview.png", preview).map_err((e: IoError): string => "Cannot write preview image.")?;
  for page in pages {
    let path: string = destination + "/" + page.name + ".html";
    create_dir_all(util.dir_of(path)).map_err((e: IoError): string => "cannot create lesson directory")?;
    write_file(path, page.html).map_err((e: IoError): string => "cannot write lesson " + page.name)?;
  }
  return Ok(());
}
```
