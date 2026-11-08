# Android GPU canvas

This module draws on the window of a NativeActivity app of `std/android` with the GPU, through EGL and OpenGL ES 2. The app draws each image once into a texture. Each frame then draws textures at their positions, which costs the CPU almost nothing.

## Model

`open_context` makes an OpenGL ES 2 context, with a surface of one pixel that keeps it current. `attach` then puts the canvas on a window, and `detach` takes it off again. The context, the program and the textures stay through `detach`, so an app keeps its images while its window is away.

`open` makes a context and attaches it to a window in one call. `attach_offscreen` puts the canvas on an image in GPU memory, `open_offscreen` does both steps, and `read` copies the pixels of the canvas into a frame.

A `Texture` holds the pixels of an offscreen frame of `std/android/draw`. Those pixels are premultiplied: each channel holds its value times the alpha, as `draw` makes them on a frame that starts transparent.

A frame of the app has these steps:

1. `clear` fills the window with one colour.
2. `draw` puts a texture at a position, and `draw_part` puts a part of it on a rectangle. Each one has an alpha and a clip to a band of rows. `draw_part` also has a `Fade`, a ramp of the alpha between two rows. A part on a larger rectangle repeats its nearest texels, so a texture of one pixel fills a rectangle with one colour.
3. `present` shows the frame. The display shows it at its next vsync. It gives `Lost` when the device lost the context, for example after a GPU reset. The textures are then gone: the app closes the canvas and opens a new one.

`begin_layer` sends the draws that follow to a layer of the size of the window, and `end_layer` sends them to the window again. `draw_layer` then draws the layer with one alpha. A group of images that overlap fades as one image in this way, with no image showing through the images over it.

Positions are in pixels, from the top left corner of the window. Each texture pixel goes on one window pixel, so an image at a whole pixel position keeps its sharp edges.

## Contracts

Call each function on the loop thread, between `open_context` and `close`. Detach the canvas from its window before the `WindowDestroyed` event of the window returns. Free each texture with `free` before `close`. A texture comes from a frame of `draw.offscreen`, whose stride is its width.

## Module assembly

The root chunk keeps the order of the declarations.

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<shaders>>

<<canvas>>

<<drawing>>

<<glyphs>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module draws on the window of a NativeActivity app with the GPU,
// through EGL and OpenGL ES 2.
import "std/android";
import "std/android/draw";
import "std/jni";
```

### `C functions`

The functions of `libEGL.so` and `libGLESv2.so` that the module calls. Each one is in every Android version that the toolchain builds for. `glTexImage2D` takes 9 integers, so its last 3 go on the stack.

```fern
<<C functions>>=
@import("libEGL.so", "eglGetDisplay")
function egl_display(native: usize): usize;

@import("libEGL.so", "eglInitialize")
function egl_initialize(display: usize, major: usize, minor: usize): i32;

@import("libEGL.so", "eglChooseConfig")
function egl_choose(display: usize, attributes: usize, configs: usize, size: i32, count: usize): i32;

@import("libEGL.so", "eglGetConfigAttrib")
function egl_config_attrib(display: usize, config: usize, attribute: i32, value: usize): i32;

@import("libEGL.so", "eglCreateContext")
function egl_create_context(display: usize, config: usize, share: usize, attributes: usize): usize;

@import("libEGL.so", "eglCreatePbufferSurface")
function egl_create_pbuffer(display: usize, config: usize, attributes: usize): usize;

@import("libEGL.so", "eglCreateWindowSurface")
function egl_create_surface(display: usize, config: usize, window: usize, attributes: usize): usize;

@import("libEGL.so", "eglMakeCurrent")
function egl_current(display: usize, draw: usize, read: usize, context: usize): i32;

@import("libEGL.so", "eglSwapInterval")
function egl_swap_interval(display: usize, interval: i32): i32;

@import("libEGL.so", "eglSwapBuffers")
function egl_swap(display: usize, surface: usize): i32;

@import("libEGL.so", "eglDestroySurface")
function egl_destroy_surface(display: usize, surface: usize): i32;

@import("libEGL.so", "eglDestroyContext")
function egl_destroy_context(display: usize, context: usize): i32;

@import("libEGL.so", "eglTerminate")
function egl_terminate(display: usize): i32;

@import("libEGL.so", "eglGetError")
function egl_error(): i32;

@import("libandroid.so", "ANativeWindow_setBuffersGeometry")
function c_window_set_geometry(window: usize, width: i32, height: i32, format: i32): i32;

@import("libGLESv2.so", "glCreateShader")
function gl_create_shader(kind: i32): i32;

@import("libGLESv2.so", "glShaderSource")
function gl_shader_source(shader: i32, count: i32, strings: usize, lengths: usize): void;

@import("libGLESv2.so", "glCompileShader")
function gl_compile_shader(shader: i32): void;

@import("libGLESv2.so", "glGetShaderiv")
function gl_shader_iv(shader: i32, name: i32, out: usize): void;

@import("libGLESv2.so", "glDeleteShader")
function gl_delete_shader(shader: i32): void;

@import("libGLESv2.so", "glCreateProgram")
function gl_create_program(): i32;

@import("libGLESv2.so", "glAttachShader")
function gl_attach_shader(program: i32, shader: i32): void;

@import("libGLESv2.so", "glBindAttribLocation")
function gl_bind_attrib(program: i32, index: i32, name: usize): void;

@import("libGLESv2.so", "glLinkProgram")
function gl_link_program(program: i32): void;

@import("libGLESv2.so", "glGetProgramiv")
function gl_program_iv(program: i32, name: i32, out: usize): void;

@import("libGLESv2.so", "glUseProgram")
function gl_use_program(program: i32): void;

@import("libGLESv2.so", "glGetUniformLocation")
function gl_uniform_location(program: i32, name: usize): i32;

@import("libGLESv2.so", "glUniform1i")
function gl_uniform1i(location: i32, v: i32): void;

@import("libGLESv2.so", "glUniform1f")
function gl_uniform1f(location: i32, v: f32): void;

@import("libGLESv2.so", "glUniform2f")
function gl_uniform2f(location: i32, a: f32, b: f32): void;

@import("libGLESv2.so", "glUniform4f")
function gl_uniform4f(location: i32, a: f32, b: f32, c: f32, d: f32): void;

@import("libGLESv2.so", "glVertexAttribPointer")
function gl_attrib_pointer(index: i32, size: i32, kind: i32, normalized: i32, stride: i32, data: usize): void;

@import("libGLESv2.so", "glEnableVertexAttribArray")
function gl_enable_attrib(index: i32): void;

@import("libGLESv2.so", "glDisableVertexAttribArray")
function gl_disable_attrib(index: i32): void;

@import("libGLESv2.so", "glGenTextures")
function gl_gen_textures(n: i32, out: usize): void;

@import("libGLESv2.so", "glDeleteTextures")
function gl_delete_textures(n: i32, ids: usize): void;

@import("libGLESv2.so", "glBindTexture")
function gl_bind_texture(target: i32, id: i32): void;

@import("libGLESv2.so", "glTexParameteri")
function gl_tex_parameter(target: i32, name: i32, value: i32): void;

@import("libGLESv2.so", "glPixelStorei")
function gl_pixel_store(name: i32, value: i32): void;

@import("libGLESv2.so", "glTexImage2D")
function gl_tex_image(target: i32, level: i32, inner: i32, width: i32, height: i32, border: i32, format: i32, kind: i32, pixels: usize): void;

@import("libGLESv2.so", "glTexSubImage2D")
function gl_tex_sub_image(target: i32, level: i32, x: i32, y: i32, width: i32, height: i32, format: i32, kind: i32, pixels: usize): void;

@import("libGLESv2.so", "glGenFramebuffers")
function gl_gen_framebuffers(n: i32, out: usize): void;

@import("libGLESv2.so", "glDeleteFramebuffers")
function gl_delete_framebuffers(n: i32, ids: usize): void;

@import("libGLESv2.so", "glBindFramebuffer")
function gl_bind_framebuffer(target: i32, id: i32): void;

@import("libGLESv2.so", "glFramebufferTexture2D")
function gl_framebuffer_texture(target: i32, attachment: i32, textarget: i32, texture: i32, level: i32): void;

@import("libGLESv2.so", "glCheckFramebufferStatus")
function gl_framebuffer_status(target: i32): i32;

@import("libGLESv2.so", "glViewport")
function gl_viewport(x: i32, y: i32, width: i32, height: i32): void;

@import("libGLESv2.so", "glScissor")
function gl_scissor(x: i32, y: i32, width: i32, height: i32): void;

@import("libGLESv2.so", "glEnable")
function gl_enable(cap: i32): void;

@import("libGLESv2.so", "glDisable")
function gl_disable(cap: i32): void;

@import("libGLESv2.so", "glBlendFunc")
function gl_blend_func(source: i32, dest: i32): void;

@import("libGLESv2.so", "glClearColor")
function gl_clear_color(r: f32, g: f32, b: f32, a: f32): void;

@import("libGLESv2.so", "glClear")
function gl_clear(mask: i32): void;

@import("libGLESv2.so", "glDrawArrays")
function gl_draw_arrays(mode: i32, first: i32, count: i32): void;

@import("libGLESv2.so", "glReadPixels")
function gl_read_pixels(x: i32, y: i32, width: i32, height: i32, format: i32, kind: i32, pixels: usize): void;

@import("libGLESv2.so", "glFinish")
function gl_finish(): void;
```

### `shaders`

One program draws each quad. The vertex shader puts the unit square on the rectangle `u_dst` of the window, and takes the texture coordinates from the rectangle `u_src`. The fragment shader multiplies the premultiplied texel by the alpha and by the ramp `u_fade`: the alpha goes from `u_fade.z` at row `u_fade.x` to `u_fade.w` at row `u_fade.y`. A row is the centre of a pixel less half a pixel, so the ramp starts on the first row. The texture coordinates need high precision, so the fragment shader asks for it where the GPU has it.

A second program draws text. Each glyph is two triangles, with a position on the window and a position in the atlas at each corner. The fragment shader multiplies the premultiplied colour `u_color` by the coverage of the glyph in the atlas.

```fern
<<shaders>>=
const VERTEX_SHADER: string = "attribute vec2 a_pos;\nuniform vec2 u_screen;\nuniform vec4 u_dst;\nuniform vec4 u_src;\nvarying vec2 v_uv;\nvarying float v_y;\nvoid main() {\n  vec2 p = u_dst.xy + a_pos * u_dst.zw;\n  v_uv = mix(u_src.xy, u_src.zw, a_pos);\n  v_y = p.y;\n  gl_Position = vec4(p.x / u_screen.x * 2.0 - 1.0, 1.0 - p.y / u_screen.y * 2.0, 0.0, 1.0);\n}\n";

const FRAGMENT_SHADER: string = "#ifdef GL_FRAGMENT_PRECISION_HIGH\nprecision highp float;\n#else\nprecision mediump float;\n#endif\nuniform sampler2D u_tex;\nuniform float u_alpha;\nuniform vec4 u_fade;\nvarying vec2 v_uv;\nvarying float v_y;\nvoid main() {\n  float t = clamp((v_y - 0.5 - u_fade.x) / (u_fade.y - u_fade.x), 0.0, 1.0);\n  gl_FragColor = texture2D(u_tex, v_uv) * (u_alpha * mix(u_fade.z, u_fade.w, t));\n}\n";

const TEXT_VERTEX_SHADER: string = "attribute vec2 a_pos;\nattribute vec2 a_uv;\nuniform vec2 u_screen;\nvarying vec2 v_uv;\nvoid main() {\n  v_uv = a_uv;\n  gl_Position = vec4(a_pos.x / u_screen.x * 2.0 - 1.0, 1.0 - a_pos.y / u_screen.y * 2.0, 0.0, 1.0);\n}\n";

const TEXT_FRAGMENT_SHADER: string = "#ifdef GL_FRAGMENT_PRECISION_HIGH\nprecision highp float;\n#else\nprecision mediump float;\n#endif\nuniform sampler2D u_atlas;\nuniform vec4 u_color;\nvarying vec2 v_uv;\nvoid main() {\n  gl_FragColor = u_color * texture2D(u_atlas, v_uv).a;\n}\n";

const GL_VERTEX_SHADER: i32 = 35633;

const GL_FRAGMENT_SHADER: i32 = 35632;

const GL_COMPILE_STATUS: i32 = 35713;

const GL_LINK_STATUS: i32 = 35714;

// The shader of `kind` from `source`, or 0 when it does not compile.
function shader(kind: i32, source: string): i32 {
  let id = gl_create_shader(kind);
  if (id == 0) {
    return 0;
  }
  unsafe {
    let text = jni.cstr(source);
    let list: usize = __alloc(16);
    __store_ptr(list, text);
    gl_shader_source(id, 1, list, 0 as usize);
    gl_compile_shader(id);
    __store_i32(list + 8 as usize, 0);
    gl_shader_iv(id, GL_COMPILE_STATUS, list + 8 as usize);
    let ok = __load_i32(list + 8 as usize) != 0;
    __free(list, 16);
    jni.free_buffer(text);
    if (!ok) {
      gl_delete_shader(id);
      return 0;
    }
  }
  return id;
}

// The program of the shaders from `vertex` and `fragment`, with each
// attribute of `attributes` at its index, or 0 when it does not link.
function program(vertex: string, fragment: string, attributes: string[]): i32 {
  let vs = shader(GL_VERTEX_SHADER, vertex);
  let fs = shader(GL_FRAGMENT_SHADER, fragment);
  if (vs == 0 || fs == 0) {
    return 0;
  }
  let id = gl_create_program();
  gl_attach_shader(id, vs);
  gl_attach_shader(id, fs);
  for i in 0..attributes.len() {
    let name = jni.cstr(attributes[i]);
    gl_bind_attrib(id, i, name);
    jni.free_buffer(name);
  }
  gl_link_program(id);
  gl_delete_shader(vs);
  gl_delete_shader(fs);
  unsafe {
    let out: usize = __alloc(8);
    __store_i32(out, 0);
    gl_program_iv(id, GL_LINK_STATUS, out);
    let ok = __load_i32(out) != 0;
    __free(out, 8);
    if (!ok) {
      return 0;
    }
  }
  return id;
}

function uniform(program: i32, name: string): i32 {
  let s = jni.cstr(name);
  let at = gl_uniform_location(program, s);
  jni.free_buffer(s);
  return at;
}
```

### `canvas`

`open_context` chooses an EGL configuration that can draw on a window and on a pbuffer with OpenGL ES 2. It has 8 bits for each of red, green, blue and alpha. Where no configuration can draw on a window, as on Mesa with no display server, it takes one for pbuffers only, and `attach` then fails. `attach` gives the window the pixel format of the configuration first. The swap interval of 1 makes `present` keep to the vsync of the display.

`Places` holds the locations of the uniforms. `quad` is 8 floats in C memory, the corners of the unit square in the order of a triangle strip. The layer is a texture of the size of the window with a framebuffer that draws into it.

```fern
<<canvas>>=
struct Places { screen: i32, dst: i32, src: i32, alpha: i32, fade: i32, tex: i32 }

// The text program and the locations of its uniforms.
struct TextPlaces { program: i32, screen: i32, color: i32, atlas: i32 }

// `pbuffer` is the surface of one pixel that keeps the context current.
// `surface` is the surface that the canvas draws on: a window, an image, or
// `pbuffer` when the canvas is detached. `window` is 0 off a window.
pub struct Canvas { display: usize, config: usize, context: usize, pbuffer: usize, surface: usize, window: usize, program: i32, places: Places, text: TextPlaces, quad: usize, width: i32, height: i32, layer: Texture, framebuffer: i32 }

// What `present` did: it showed the frame, the canvas has no window, or the
// device lost the context.
pub enum Shown { Showed, NoWindow, Lost }

// An image on the GPU. Its id is 0 when it holds no image.
pub struct Texture { id: i32, width: i32, height: i32 }

// The alpha goes from `a0` at the row `y0` to `a1` at the row `y1`, and
// stays at `a0` above `y0` and at `a1` below `y1`.
pub struct Fade { y0: i32, y1: i32, a0: f64, a1: f64 }

// No ramp: the alpha is the same on each row.
pub function no_fade(): Fade {
  return Fade { y0: 0, y1: 1, a0: 1.0, a1: 1.0 };
}

const EGL_NONE: i32 = 12344;

const EGL_NATIVE_VISUAL_ID: i32 = 12334;

const EGL_CONTEXT_CLIENT_VERSION: i32 = 12440;

const GL_TEXTURE_2D: i32 = 3553;

const GL_FLOAT: i32 = 5126;

const GL_BLEND: i32 = 3042;

const GL_DITHER: i32 = 3024;

const GL_SCISSOR_TEST: i32 = 3089;

const GL_ONE_MINUS_SRC_ALPHA: i32 = 771;

const GL_FRAMEBUFFER: i32 = 36160;

const GL_COLOR_BUFFER_BIT: i32 = 16384;

const EGL_WINDOW_BIT: i32 = 4;

const EGL_PBUFFER_BIT: i32 = 1;

// The attributes of the EGL configuration: red, green, blue and alpha of 8
// bits, the kind of surface `surface`, and OpenGL ES 2.
function config_attributes(surface: i32): i32[] {
  return [12324, 8, 12323, 8, 12322, 8, 12321, 8, 12339, surface, 12352, 4, EGL_NONE];
}

const EGL_CONTEXT_LOST: i32 = 12302;

// A canvas with no window, or None when the device cannot make an OpenGL ES
// 2 context.
pub function open_context(): Option[Canvas] {
  let d = egl_display(0 as usize);
  if (d == 0 as usize || egl_initialize(d, 0 as usize, 0 as usize) == 0) {
    return None;
  }
  let config = choose_config(d, EGL_WINDOW_BIT | EGL_PBUFFER_BIT);
  if (config == 0 as usize) {
    config = choose_config(d, EGL_PBUFFER_BIT);
  }
  let ctx = context(d, config);
  if (ctx == 0 as usize) {
    return fail(d, 0 as usize, 0 as usize);
  }
  let pbuffer = pbuffer_of(d, config, 1, 1);
  if (pbuffer == 0 as usize || egl_current(d, pbuffer, pbuffer, ctx) == 0) {
    return fail(d, pbuffer, ctx);
  }
  return start(d, config, pbuffer, ctx);
}

// A canvas on the window `w`, or None when the device cannot make one.
pub function open(w: android.Window): Option[Canvas] {
  let Some(c) = open_context() else {
    return None;
  };
  let on = c.attach(w);
  if (!on.attached()) {
    close(on);
    return None;
  }
  return Some(on);
}

// A canvas on an image of `width` by `height` in GPU memory, or None when
// the device cannot make one. `present` does nothing on it.
pub function open_offscreen(width: i32, height: i32): Option[Canvas] {
  let Some(c) = open_context() else {
    return None;
  };
  let on = c.attach_offscreen(width, height);
  if (!on.attached()) {
    close(on);
    return None;
  }
  return Some(on);
}

// The canvas on the window `w`, in place of the surface it drew on. When
// the window takes no surface, the canvas comes back detached.
pub function (c: Canvas) attach(w: android.Window): Canvas {
  let off = c.detach();
  let set = c_window_set_geometry(w.ptr, 0, 0, visual_format(off.display, off.config));
  let surface = egl_create_surface(off.display, off.config, w.ptr, 0 as usize);
  let on = off.bind(surface, w.width(), w.height());
  if (on.attached()) {
    let interval = egl_swap_interval(on.display, 1);
    return Canvas { ...on, window: w.ptr };
  }
  return on;
}

// The canvas on a new image of `width` by `height` in GPU memory, in place
// of the surface it drew on.
pub function (c: Canvas) attach_offscreen(width: i32, height: i32): Canvas {
  let off = c.detach();
  return off.bind(pbuffer_of(off.display, off.config, width, height), width, height);
}

// The canvas off its surface. Its textures stay.
pub function (c: Canvas) detach(): Canvas {
  if (!c.attached()) {
    return c;
  }
  let r = egl_current(c.display, c.pbuffer, c.pbuffer, c.context);
  let s = egl_destroy_surface(c.display, c.surface);
  return Canvas { ...c, surface: c.pbuffer, window: 0 as usize };
}

// The canvas is on a window or on an image.
pub function (c: Canvas) attached(): boolean {
  return c.surface != c.pbuffer;
}

// The canvas drawing on `surface` of `width` by `height`. It stays on its
// pbuffer when `surface` is 0 or cannot be made current.
function (c: Canvas) bind(surface: usize, width: i32, height: i32): Canvas {
  if (surface == 0 as usize) {
    return c;
  }
  if (egl_current(c.display, surface, surface, c.context) == 0) {
    let s = egl_destroy_surface(c.display, surface);
    let r = egl_current(c.display, c.pbuffer, c.pbuffer, c.context);
    return c;
  }
  return Canvas { ...c, surface: surface }.sized(width, height);
}

// A pbuffer surface of `width` by `height`, or 0.
function pbuffer_of(d: usize, config: usize, width: i32, height: i32): usize {
  unsafe {
    let attrs: usize = __alloc(24);
    // EGL_WIDTH and EGL_HEIGHT.
    __store_i32(attrs, 12375);
    __store_i32(attrs + 4 as usize, width);
    __store_i32(attrs + 8 as usize, 12374);
    __store_i32(attrs + 12 as usize, height);
    __store_i32(attrs + 16 as usize, EGL_NONE);
    let surface = egl_create_pbuffer(d, config, attrs);
    __free(attrs, 24);
    return surface;
  }
}

// An OpenGL ES 2 context of the configuration, or 0.
function context(d: usize, config: usize): usize {
  if (config == 0 as usize) {
    return 0 as usize;
  }
  unsafe {
    let attrs: usize = __alloc(16);
    __store_i32(attrs, EGL_CONTEXT_CLIENT_VERSION);
    __store_i32(attrs + 4 as usize, 2);
    __store_i32(attrs + 8 as usize, EGL_NONE);
    let ctx = egl_create_context(d, config, 0 as usize, attrs);
    __free(attrs, 16);
    return ctx;
  }
}

// The first configuration with the attributes for the kinds of surface in
// `surface`, or 0 when there is none.
function choose_config(d: usize, surface: i32): usize {
  unsafe {
    let attrs = config_attributes(surface);
    let buf: usize = __alloc(80);
    for i in 0..attrs.len() {
      __store_i32(buf + (4 * i) as usize, attrs[i]);
    }
    __store_ptr(buf + 64 as usize, 0 as usize);
    __store_i32(buf + 72 as usize, 0);
    let config = 0 as usize;
    if (egl_choose(d, buf, buf + 64 as usize, 1, buf + 72 as usize) != 0 && __load_i32(buf + 72 as usize) == 1) {
      config = __load_ptr(buf + 64 as usize);
    }
    __free(buf, 80);
    return config;
  }
}

// The pixel format of the window that the configuration draws in.
function visual_format(d: usize, config: usize): i32 {
  unsafe {
    let out: usize = __alloc(8);
    __store_i32(out, 0);
    let got = egl_config_attrib(d, config, EGL_NATIVE_VISUAL_ID, out);
    let format = __load_i32(out);
    __free(out, 8);
    return format;
  }
}

function fail(d: usize, surface: usize, ctx: usize): Option[Canvas] {
  if (surface != 0 as usize) {
    let s = egl_destroy_surface(d, surface);
  }
  if (ctx != 0 as usize) {
    let c = egl_destroy_context(d, ctx);
  }
  let t = egl_terminate(d);
  return None;
}

// The canvas with its program, its quad and its layer, once its context is
// current.
function start(d: usize, config: usize, pbuffer: usize, ctx: usize): Option[Canvas] {
  let p = program(VERTEX_SHADER, FRAGMENT_SHADER, ["a_pos"]);
  let tp = program(TEXT_VERTEX_SHADER, TEXT_FRAGMENT_SHADER, ["a_pos", "a_uv"]);
  if (p == 0 || tp == 0) {
    return fail(d, pbuffer, ctx);
  }
  let text = TextPlaces { program: tp, screen: uniform(tp, "u_screen"), color: uniform(tp, "u_color"), atlas: uniform(tp, "u_atlas") };
  gl_use_program(tp);
  gl_uniform1i(text.atlas, 0);
  gl_use_program(p);
  let places = Places { screen: uniform(p, "u_screen"), dst: uniform(p, "u_dst"), src: uniform(p, "u_src"), alpha: uniform(p, "u_alpha"), fade: uniform(p, "u_fade"), tex: uniform(p, "u_tex") };
  gl_uniform1i(places.tex, 0);
  unsafe {
    let quad: usize = __alloc(32);
    let corners = [0.0, 0.0, 1.0, 0.0, 0.0, 1.0, 1.0, 1.0];
    for i in 0..8 {
      __store_i32(quad + (4 * i) as usize, f32_bits(corners[i] as f32));
    }
    gl_attrib_pointer(0, 2, GL_FLOAT, 0, 0, quad);
    gl_enable_attrib(0);
    gl_disable(GL_DITHER);
    gl_enable(GL_BLEND);
    gl_blend_func(1, GL_ONE_MINUS_SRC_ALPHA);
    gl_enable(GL_SCISSOR_TEST);
    let c = Canvas { display: d, config: config, context: ctx, pbuffer: pbuffer, surface: pbuffer, window: 0 as usize, program: p, places: places, text: text, quad: quad, width: 0, height: 0, layer: Texture { id: 0, width: 0, height: 0 }, framebuffer: 0 };
    return Some(c.sized(1, 1));
  }
}

// The canvas for a window of `width` by `height`, with a layer of that size.
pub function (c: Canvas) sized(width: i32, height: i32): Canvas {
  if (width == c.width && height == c.height) {
    return c;
  }
  free_layer(c);
  gl_viewport(0, 0, width, height);
  gl_use_program(c.text.program);
  gl_uniform2f(c.text.screen, width as f32, height as f32);
  gl_use_program(c.program);
  gl_uniform2f(c.places.screen, width as f32, height as f32);
  let tex = blank(width, height);
  let fb = 0;
  unsafe {
    let out: usize = __alloc(8);
    gl_gen_framebuffers(1, out);
    fb = __load_i32(out);
    __free(out, 8);
  }
  gl_bind_framebuffer(GL_FRAMEBUFFER, fb);
  gl_framebuffer_texture(GL_FRAMEBUFFER, 36064, GL_TEXTURE_2D, tex.id, 0);
  if (gl_framebuffer_status(GL_FRAMEBUFFER) != 36053) {
    free(tex);
    delete_framebuffer(fb);
    tex = Texture { id: 0, width: 0, height: 0 };
    fb = 0;
  }
  gl_bind_framebuffer(GL_FRAMEBUFFER, 0);
  return Canvas { ...c, width: width, height: height, layer: tex, framebuffer: fb };
}

function free_layer(c: Canvas): void {
  if (c.framebuffer != 0) {
    delete_framebuffer(c.framebuffer);
  }
  free(c.layer);
}

function delete_framebuffer(fb: i32): void {
  unsafe {
    let ids: usize = __alloc(8);
    __store_i32(ids, fb);
    gl_delete_framebuffers(1, ids);
    __free(ids, 8);
  }
}

// Shows the frame that the canvas drew on its window.
pub function (c: Canvas) present(): Shown {
  if (c.window == 0 as usize) {
    return NoWindow;
  }
  if (egl_swap(c.display, c.surface) != 0) {
    return Showed;
  }
  if (egl_error() == EGL_CONTEXT_LOST) {
    return Lost;
  }
  return NoWindow;
}

// Frees the layer, the program memory, the surfaces and the context. Free
// the textures first.
pub function close(c: Canvas): void {
  let off = c.detach();
  free_layer(off);
  unsafe {
    __free(off.quad, 32);
  }
  let r = egl_current(off.display, 0 as usize, 0 as usize, 0 as usize);
  let s = egl_destroy_surface(off.display, off.pbuffer);
  let x = egl_destroy_context(off.display, off.context);
  let t = egl_terminate(off.display);
}
```

### `drawing`

A texture keeps the nearest texel at each pixel, and its edges do not repeat. `texture` copies the frame, so the app can release the frame after the call. The scissor test keeps each draw to its band of rows. The rows of OpenGL go from the bottom, so the band is turned over first.

```fern
<<drawing>>=
const GL_RGBA: i32 = 6408;

const GL_UNSIGNED_BYTE: i32 = 5121;

// A texture of `width` by `height` with no pixels set yet.
function blank(width: i32, height: i32): Texture {
  return upload(0 as usize, width, height);
}

// A texture of the premultiplied pixels of the offscreen frame `f`.
pub function texture(f: draw.Frame): Texture {
  if (f.width <= 0 || f.height <= 0 || f.stride != f.width) {
    return Texture { id: 0, width: 0, height: 0 };
  }
  return upload(f.bits, f.width, f.height);
}

function upload(pixels: usize, width: i32, height: i32): Texture {
  let id = 0;
  unsafe {
    let out: usize = __alloc(8);
    gl_gen_textures(1, out);
    id = __load_i32(out);
    __free(out, 8);
  }
  gl_bind_texture(GL_TEXTURE_2D, id);
  // NEAREST for both filters, and CLAMP_TO_EDGE for both axes.
  gl_tex_parameter(GL_TEXTURE_2D, 10241, 9728);
  gl_tex_parameter(GL_TEXTURE_2D, 10240, 9728);
  gl_tex_parameter(GL_TEXTURE_2D, 10242, 33071);
  gl_tex_parameter(GL_TEXTURE_2D, 10243, 33071);
  gl_pixel_store(3317, 4);
  gl_tex_image(GL_TEXTURE_2D, 0, GL_RGBA, width, height, 0, GL_RGBA, GL_UNSIGNED_BYTE, pixels);
  return Texture { id: id, width: width, height: height };
}

pub function free(t: Texture): void {
  if (t.id == 0) {
    return;
  }
  unsafe {
    let ids: usize = __alloc(8);
    __store_i32(ids, t.id);
    gl_delete_textures(1, ids);
    __free(ids, 8);
  }
}

// The pixels that the canvas drew, in a frame from `draw.offscreen`, with the
// top row first. Release the frame with `draw.release`.
pub function (c: Canvas) read(): draw.Frame {
  let f = draw.offscreen(c.width, c.height);
  if (f.width == 0) {
    return f;
  }
  gl_finish();
  let up = draw.offscreen(c.width, c.height);
  gl_read_pixels(0, 0, c.width, c.height, GL_RGBA, GL_UNSIGNED_BYTE, up.bits);
  // OpenGL gives the bottom row first.
  for row in 0..c.height {
    f.blit(draw.Frame { ...up, bits: up.bits + ((c.height - 1 - row) * c.width * 4) as usize, height: 1 }, 0, row);
  }
  draw.release(up);
  return f;
}

// Fills the window, or the layer between `begin_layer` and `end_layer`,
// with `color`.
pub function (c: Canvas) clear(color: i32): void {
  gl_scissor(0, 0, c.width, c.height);
  let a = (color >> 24 & 255) as f32 / 255.0;
  gl_clear_color((color & 255) as f32 / 255.0, (color >> 8 & 255) as f32 / 255.0, (color >> 16 & 255) as f32 / 255.0, a);
  gl_clear(GL_COLOR_BUFFER_BIT);
}

// Draws `t` with its top left corner at x and y, with the alpha `alpha`,
// on the rows from clip0 up to clip1.
pub function (c: Canvas) draw(t: Texture, x: i32, y: i32, alpha: f64, clip0: i32, clip1: i32): void {
  c.draw_part(t, Part { x: 0, y: 0, w: t.width, h: t.height }, Part { x: x, y: y, w: t.width, h: t.height }, alpha, no_fade(), clip0, clip1);
}

// A rectangle of pixels.
pub struct Part { x: i32, y: i32, w: i32, h: i32 }

// Draws the part `from` of `t` on the rectangle `to` of the window, with the
// alpha `alpha` and the ramp `fade`, on the rows from clip0 up to clip1.
pub function (c: Canvas) draw_part(t: Texture, from: Part, to: Part, alpha: f64, fade: Fade, clip0: i32, clip1: i32): void {
  if (t.id == 0 || from.w <= 0 || from.h <= 0) {
    return;
  }
  let tw = t.width as f32;
  let th = t.height as f32;
  c.quad_of(t.id, to, from.x as f32 / tw, from.y as f32 / th, (from.x + from.w) as f32 / tw, (from.y + from.h) as f32 / th, alpha, fade, clip0, clip1);
}

// Draws into the layer until `end_layer`. The layer starts transparent.
pub function (c: Canvas) begin_layer(): void {
  gl_bind_framebuffer(GL_FRAMEBUFFER, c.framebuffer);
  c.clear(0);
}

pub function (c: Canvas) end_layer(): void {
  gl_bind_framebuffer(GL_FRAMEBUFFER, 0);
}

// Draws the layer with its top left corner at x and y, with the alpha
// `alpha`. The rows of the layer texture go from the bottom.
pub function (c: Canvas) draw_layer(x: i32, y: i32, alpha: f64): void {
  if (c.layer.id == 0) {
    return;
  }
  c.quad_of(c.layer.id, Part { x: x, y: y, w: c.width, h: c.height }, 0.0 as f32, 1.0 as f32, 1.0 as f32, 0.0 as f32, alpha, no_fade(), 0, c.height);
}

function (c: Canvas) quad_of(id: i32, to: Part, u0: f32, v0: f32, u1: f32, v1: f32, alpha: f64, fade: Fade, clip0: i32, clip1: i32): void {
  let top = clip0.max(0);
  let bottom = clip1.min(c.height);
  if (bottom <= top || alpha <= 0.0) {
    return;
  }
  gl_scissor(0, c.height - bottom, c.width, bottom - top);
  gl_bind_texture(GL_TEXTURE_2D, id);
  gl_uniform4f(c.places.dst, to.x as f32, to.y as f32, to.w as f32, to.h as f32);
  gl_uniform4f(c.places.src, u0, v0, u1, v1);
  gl_uniform1f(c.places.alpha, alpha as f32);
  gl_uniform4f(c.places.fade, fade.y0 as f32, fade.y1 as f32, fade.a0 as f32, fade.a1 as f32);
  gl_draw_arrays(5, 0, 4);
}
```

### `glyphs`

An `Atlas` holds the coverage of glyphs in pages of `size` by `size` texels, one byte for each texel. `add` puts a glyph on the shelf of the last page: the glyphs of a shelf go left to right, and a new shelf starts under the tallest glyph of the shelf above. When a glyph does not fit on the last page, `add` starts a new page. Each glyph has a border of one empty texel, so a glyph never shows the edge of its neighbour.

`draw_glyphs` draws the glyphs of a text in one colour. It makes one draw call for each page that the glyphs are on, and then puts the quad program back.

```fern
<<glyphs>>=
const GL_ALPHA: i32 = 6406;

const GL_UNPACK_ALIGNMENT: i32 = 3317;

const GL_TRIANGLES: i32 = 4;

// Pages of glyph coverage. The cursor is at x and y on the last page, and
// `row` is the height of the shelf there.
pub struct Atlas { pages: i32[], size: i32, x: i32, y: i32, row: i32 }

// Where a glyph of w by h is: on page `page`, at x and y.
pub struct Slot { page: i32, x: i32, y: i32, w: i32, h: i32 }

// The glyph in `slot` with its top left corner at x and y on the window.
pub struct Glyph { slot: Slot, x: i32, y: i32 }

// An atlas with no pages, which `add` makes pages of `size` by `size` for.
pub function atlas(size: i32): Atlas {
  return Atlas { pages: [], size: size, x: 0, y: 0, row: 0 };
}

// Frees each page of the atlas.
pub function free_atlas(a: Atlas): void {
  for id in a.pages {
    free(Texture { id: id, width: a.size, height: a.size });
  }
}

// The atlas with the coverage `cov` of w by h added, and its slot. A glyph
// larger than a page gets a slot of no size.
pub function (a: Atlas) add(w: i32, h: i32, cov: [u8]): (Atlas, Slot) {
  if (w <= 0 || h <= 0 || w + 2 > a.size || h + 2 > a.size || cov.len() < w * h) {
    return (a, Slot { page: 0, x: 0, y: 0, w: 0, h: 0 });
  }
  let at = a;
  if (at.x + w + 2 > at.size) {
    at = Atlas { ...at, x: 0, y: at.y + at.row, row: 0 };
  }
  if (at.pages.len() == 0 || at.y + h + 2 > at.size) {
    at = Atlas { ...at, pages: at.pages.append(page(at.size)), x: 0, y: 0, row: 0 };
  }
  upload_glyph(at.pages[at.pages.len() - 1], at.x, at.y, w, h, cov);
  let slot = Slot { page: at.pages.len() - 1, x: at.x + 1, y: at.y + 1, w: w, h: h };
  return (Atlas { ...at, x: at.x + w + 2, row: at.row.max(h + 2) }, slot);
}

// A page of `size` by `size` texels.
function page(size: i32): i32 {
  let id = 0;
  unsafe {
    let out: usize = __alloc(8);
    gl_gen_textures(1, out);
    id = __load_i32(out);
    __free(out, 8);
  }
  gl_bind_texture(GL_TEXTURE_2D, id);
  gl_tex_parameter(GL_TEXTURE_2D, 10241, 9728);
  gl_tex_parameter(GL_TEXTURE_2D, 10240, 9728);
  gl_tex_parameter(GL_TEXTURE_2D, 10242, 33071);
  gl_tex_parameter(GL_TEXTURE_2D, 10243, 33071);
  gl_pixel_store(GL_UNPACK_ALIGNMENT, 1);
  gl_tex_image(GL_TEXTURE_2D, 0, GL_ALPHA, size, size, 0, GL_ALPHA, GL_UNSIGNED_BYTE, 0 as usize);
  gl_pixel_store(GL_UNPACK_ALIGNMENT, 4);
  return id;
}

// Writes `cov` of w by h into page `id` with its border, from x and y.
function upload_glyph(id: i32, x: i32, y: i32, w: i32, h: i32, cov: [u8]): void {
  let bw = w + 2;
  let bh = h + 2;
  unsafe {
    let buf: usize = __alloc(bw * bh);
    for i in 0..bw * bh {
      __store_u8(buf + i as usize, 0);
    }
    for row in 0..h {
      for col in 0..w {
        __store_u8(buf + ((row + 1) * bw + col + 1) as usize, cov[row * w + col] as i32);
      }
    }
    gl_bind_texture(GL_TEXTURE_2D, id);
    gl_pixel_store(GL_UNPACK_ALIGNMENT, 1);
    gl_tex_sub_image(GL_TEXTURE_2D, 0, x, y, bw, bh, GL_ALPHA, GL_UNSIGNED_BYTE, buf);
    gl_pixel_store(GL_UNPACK_ALIGNMENT, 4);
    __free(buf, bw * bh);
  }
}

// Draws the glyphs `gs` of the atlas `a` in `color`, with the alpha
// `alpha`, on the rows from clip0 up to clip1.
pub function (c: Canvas) draw_glyphs(a: Atlas, gs: Glyph[], color: i32, alpha: f64, clip0: i32, clip1: i32): void {
  let top = clip0.max(0);
  let bottom = clip1.min(c.height);
  if (bottom <= top || alpha <= 0.0 || gs.len() == 0) {
    return;
  }
  gl_scissor(0, c.height - bottom, c.width, bottom - top);
  gl_use_program(c.text.program);
  let k = (color >> 24 & 255) as f64 / 255.0 * alpha;
  gl_uniform4f(c.text.color, ((color & 255) as f64 / 255.0 * k) as f32, ((color >> 8 & 255) as f64 / 255.0 * k) as f32, ((color >> 16 & 255) as f64 / 255.0 * k) as f32, k as f32);
  gl_enable_attrib(1);
  for pg in 0..a.pages.len() {
    c.glyph_page(a, gs, pg);
  }
  gl_disable_attrib(1);
  gl_use_program(c.program);
  gl_attrib_pointer(0, 2, GL_FLOAT, 0, 0, c.quad);
}

// Draws the glyphs of `gs` on page `pg` in one draw call. Each glyph is six
// corners of four floats: x and y on the window, and u and v in the atlas.
function (c: Canvas) glyph_page(a: Atlas, gs: Glyph[], pg: i32): void {
  let n = 0;
  for g in gs {
    if (g.slot.page == pg && g.slot.w > 0) {
      n += 1;
    }
  }
  if (n == 0) {
    return;
  }
  let size = a.size as f32;
  unsafe {
    let buf: usize = __alloc(n * 96);
    let at = 0;
    for g in gs {
      if (g.slot.page == pg && g.slot.w > 0) {
        let x0 = g.x as f32;
        let y0 = g.y as f32;
        let x1 = (g.x + g.slot.w) as f32;
        let y1 = (g.y + g.slot.h) as f32;
        let u0 = g.slot.x as f32 / size;
        let v0 = g.slot.y as f32 / size;
        let u1 = (g.slot.x + g.slot.w) as f32 / size;
        let v1 = (g.slot.y + g.slot.h) as f32 / size;
        let corners = [x0, y0, u0, v0, x1, y0, u1, v0, x0, y1, u0, v1, x1, y0, u1, v0, x1, y1, u1, v1, x0, y1, u0, v1];
        for i in 0..24 {
          __store_i32(buf + (at + 4 * i) as usize, f32_bits(corners[i]));
        }
        at += 96;
      }
    }
    gl_bind_texture(GL_TEXTURE_2D, a.pages[pg]);
    gl_attrib_pointer(0, 2, GL_FLOAT, 0, 16, buf);
    gl_attrib_pointer(1, 2, GL_FLOAT, 0, 16, buf + 8 as usize);
    gl_draw_arrays(GL_TRIANGLES, 0, 6 * n);
    __free(buf, n * 96);
  }
}
```
