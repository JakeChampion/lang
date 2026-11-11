# A painter of nodes

This module draws the nodes of `std/android/ui/scene` on the window of a NativeActivity app, with the GPU through `std/android/gl`.

## Model

A `Painter` makes an image of each node one time, and keeps it on the GPU under the `paint_key` of the node. Each frame then draws the images at the positions of their nodes. So a scroll, a fling or a slide costs the CPU almost nothing.

A label draws from a glyph atlas. A new label costs only the glyphs that the atlas does not hold. A fill with square corners draws as one pixel that the GPU stretches.

`attach` puts the painter on a window, and `detach` takes it off. The context, the images and the glyphs stay through `detach`, so an app keeps them while its window is away. `present` draws a list of passes and shows the frame. A pass with an alpha below 1 draws as one image, so a screen fades as a whole.

When the images hold more than 96 MB, `present` frees each image that the frame did not draw.

On a device that gives no OpenGL ES 2 context, `attached` stays false. `present_cpu` then draws the nodes on the pixels of the window with the CPU.

## Contracts

Call each function on the loop thread of the app. Use a window with either `present` or `present_cpu`: a window has one producer of pixels at a time. Call `close` before the app ends. When the device takes the context away, `present` makes a new one on the same window, and the next frame makes the images again.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<painter>>

<<frames>>

<<nodes>>

<<images>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module draws the nodes of std/android/ui/scene on a window with the
// GPU.
import "core/map";
import "std/android";
import "std/android/draw";
import "std/android/gl";
import "std/android/text";
import "std/android/ui/scene";
import "std/option";
```

### `painter`

```fern
<<painter>>=
// An image on the GPU, with the margin of pixels around its node.
struct Image { t: gl.Texture, m: i32 }

// `images` holds the image of each node by its key, and `bytes` their
// size. `atlas` holds the glyphs, and `slots` the slot of each glyph by its
// key. `w` and `h` are the size of the window.
pub struct Painter { canvas: Option[gl.Canvas], images: Map[string, Image], bytes: i64, atlas: gl.Atlas, slots: Map[i64, gl.Slot], cache: text.Cache, fonts: text.Fonts, w: i32, h: i32 }

// The size of the images, in bytes, above which a frame frees the images
// that it did not draw.
const BUDGET: i64 = 100663296i64;

// The side of a page of the glyph atlas, in texels.
const ATLAS_SIZE: i32 = 1024;

// A painter with no window, which draws text with `fonts`.
pub function new(fonts: text.Fonts): Painter {
  return Painter { canvas: None, images: map_new(256), bytes: 0i64, atlas: gl.atlas(ATLAS_SIZE), slots: map_new(512), cache: text.new_cache(), fonts: fonts, w: 0, h: 0 };
}

// The painter draws on a window or on an image.
pub function attached(p: Painter): boolean {
  if let Some(c) = p.canvas {
    return c.attached();
  }
  return false;
}

// The painter on the window `window`, which it draws on from now on. It
// keeps the context and the images of the window before.
pub function attach(p: Painter, window: usize): Painter {
  let w = android.Window { ptr: window };
  if let Some(c) = p.canvas {
    return Painter { ...p, canvas: Some(c.attach(w)) };
  }
  if let Some(c) = gl.open_context() {
    return Painter { ...p, canvas: Some(c.attach(w)) };
  }
  return p;
}

// The painter on the canvas `c`, which it draws on from now on.
pub function on_canvas(p: Painter, c: Option[gl.Canvas]): Painter {
  let q = close(p);
  return Painter { ...q, canvas: c };
}

// The painter off its window. Its context and its images stay.
pub function detach(p: Painter): Painter {
  if let Some(c) = p.canvas {
    return Painter { ...p, canvas: Some(c.detach()) };
  }
  return p;
}

// The painter with no canvas: it frees each image and closes the canvas.
pub function close(p: Painter): Painter {
  let Some(c) = p.canvas else {
    return p;
  };
  for e in p.images.entries() {
    let (_, img) = e;
    gl.free(img.t);
  }
  gl.free_atlas(p.atlas);
  gl.close(c);
  return forget(p);
}

// The painter with no canvas and no images, which it does not free: the
// device lost them with the context.
function forget(p: Painter): Painter {
  return Painter { ...p, canvas: None, images: map_new(256), bytes: 0i64, atlas: gl.atlas(ATLAS_SIZE), slots: map_new(512) };
}

// The painter for a window of `w` by `h` pixels.
pub function with_size(p: Painter, w: i32, h: i32): Painter {
  return Painter { ...p, w: w, h: h };
}
```

### `frames`

```fern
<<frames>>=
// Clears the window to `background`, draws the passes in their order, and
// shows the frame.
pub function present(own p: Painter, passes: scene.Pass[], background: i32): Painter {
  let Some(c0) = p.canvas else {
    return p;
  };
  if (!c0.attached()) {
    return p;
  }
  let c = c0.sized(p.w, p.h);
  let q = Painter { ...p, canvas: Some(c) };
  c.clear(background);
  for pass in passes {
    if (pass.alpha < 1.0) {
      c.begin_layer();
      q = draw_nodes(q, c, pass.nodes, 0);
      c.end_layer();
      c.draw_layer(0, pass.dy, pass.alpha);
    } else {
      q = draw_nodes(q, c, pass.nodes, pass.dy);
    }
  }
  match (c.present()) {
    gl.Lost => {
      gl.close(c);
      return attach(forget(q), c.window);
    },
    _ => {
      return evict(q, passes);
    }
  }
}

// Draws the nodes on the pixels of the window `window` with the CPU, over
// `background`, and shows the frame.
pub function present_cpu(own p: Painter, window: usize, nodes: scene.Node[], background: i32): Painter {
  let f = android.Window { ptr: window }.lock();
  if (f.width == 0) {
    return p;
  }
  f.fill(0, 0, f.width, f.height, background);
  let cache = scene.render(f, p.fonts, p.cache, nodes);
  android.post(f);
  return Painter { ...p, cache: cache };
}
```

### `nodes`

A node that is wholly outside its clip rows, its clip columns or the window draws nothing.

```fern
<<nodes>>=
function draw_nodes(own p: Painter, c: gl.Canvas, ns: scene.Node[], dy: i32): Painter {
  let q = p;
  for n in ns {
    q = draw_node(q, c, n, dy);
  }
  return q;
}

function draw_node(own p: Painter, c0: gl.Canvas, n: scene.Node, dy: i32): Painter {
  let clip0 = n.clip0 + dy;
  let clip1 = n.clip1 + dy;
  let pad = scene.margin(n);
  if (n.y + dy + n.h + pad <= clip0.max(0) || n.y + dy - pad >= clip1.min(p.h)) {
    return p;
  }
  if (n.x + n.w + pad <= n.left.max(0) || n.x - pad >= n.right.min(p.w)) {
    return p;
  }
  let c = c0.columns(n.left, n.right);
  match (n.paint) {
    scene.Blank => {
      return p;
    },
    scene.Fill(color, r) => {
      if (r == 0) {
        return solid(p, c, n, color, dy);
      }
      return draw_image(p, c, n, dy);
    },
    scene.Label(s, px, face, track, color) => {
      return draw_text(p, c, n, Text { s: s, px: px, face: face, track: track, color: color }, dy);
    },
    _ => {
      return draw_image(p, c, n, dy);
    }
  }
}

// A label: its text, size, face, letter spacing and color.
struct Text { s: string, px: i32, face: i32, track: i32, color: i32 }

// Draws the label `t` of node `n` from the glyph atlas, with its first pen
// at the left of the node and its baseline at the ascent below the top.
function draw_text(own p: Painter, c: gl.Canvas, n: scene.Node, t: Text, dy: i32): Painter {
  let (cache, placed) = text.layout(p.cache, p.fonts, t.face, t.px, t.track, t.s);
  let q = Painter { ...p, cache: cache };
  let base = n.y + dy + text.ascent(p.fonts, t.face, t.px);
  let glyphs: gl.Glyph[] = [];
  for pl in placed {
    if (pl.bm.width > 0) {
      if (q.slots.get(pl.key).is_none()) {
        let (a, slot) = q.atlas.add(pl.bm.width, pl.bm.height, pl.bm.coverage);
        q = Painter { ...q, atlas: a, slots: q.slots.insert(pl.key, slot) };
      }
      if let Some(slot) = q.slots.get(pl.key) {
        glyphs = glyphs.append(gl.Glyph { slot: slot, x: n.x + pl.x, y: base + pl.bm.top });
      }
    }
  }
  c.draw_glyphs(q.atlas, glyphs, t.color, 1.0, n.clip0 + dy, n.clip1 + dy);
  return q;
}

// A rectangle of one color: an image of one pixel, on the whole node.
function solid(own p: Painter, c: gl.Canvas, n: scene.Node, color: i32, dy: i32): Painter {
  let key = "solid|" + color.to_string();
  let q = p;
  if (q.images.get(key).is_none()) {
    let fr = draw.offscreen(1, 1);
    fr.store(0, 0, 1, 1, 0);
    fr.fill(0, 0, 1, 1, color);
    q = keep(q, key, Image { t: gl.texture(fr), m: 0 });
    draw.release(fr);
  }
  if let Some(img) = q.images.get(key) {
    c.draw_part(img.t, gl.Part { x: 0, y: 0, w: 1, h: 1 }, gl.Part { x: n.x, y: n.y + dy, w: n.w, h: n.h }, 1.0, gl.no_fade(), n.clip0 + dy, n.clip1 + dy);
  }
  return q;
}
```

### `images`

```fern
<<images>>=
// Draws the image of node `n`, which it makes the first time.
function draw_image(own p: Painter, c: gl.Canvas, n: scene.Node, dy: i32): Painter {
  let key = scene.paint_key(n);
  if (key.len() == 0 || n.w <= 0 || n.h <= 0) {
    return p;
  }
  let q = p;
  if (q.images.get(key).is_none()) {
    q = make_image(q, n, key);
  }
  if let Some(img) = q.images.get(key) {
    c.draw(img.t, n.x - img.m, n.y - img.m + dy, 1.0, n.clip0 + dy, n.clip1 + dy);
  }
  return q;
}

function make_image(own p: Painter, n: scene.Node, key: string): Painter {
  let m = scene.margin(n);
  let fr = draw.offscreen(n.w + 2 * m, n.h + 2 * m);
  let cache = scene.raster(fr, p.fonts, p.cache, n, m);
  let t = gl.texture(fr);
  draw.release(fr);
  return keep(Painter { ...p, cache: cache }, key, Image { t: t, m: m });
}

function keep(own p: Painter, key: string, img: Image): Painter {
  let size = img.t.width as i64 * (img.t.height as i64) * 4i64;
  return Painter { ...p, images: p.images.insert(key, img), bytes: p.bytes + size };
}

// When the images pass the budget, frees each one that the passes do not
// draw.
function evict(own p: Painter, passes: scene.Pass[]): Painter {
  if (p.bytes <= BUDGET) {
    return p;
  }
  let used: Map[string, boolean] = map_new(256);
  for pass in passes {
    for n in pass.nodes {
      used = used.insert(scene.paint_key(n), true);
    }
  }
  let kept: Map[string, Image] = map_new(256);
  let bytes = 0i64;
  for e in p.images.entries() {
    let (key, img) = e;
    if (used.get(key).is_some() || key.starts_with("solid|")) {
      kept = kept.insert(key, img);
      bytes += img.t.width as i64 * (img.t.height as i64) * 4i64;
    } else {
      gl.free(img.t);
    }
  }
  return Painter { ...p, images: kept, bytes: bytes };
}
```
