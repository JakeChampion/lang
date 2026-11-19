package e2e

import "testing"

// The ExoPlayer of std/android/exoplayer on a fake JNI. Each call of the
// loop thread sends a Message to the Handler of the player, and the harness
// gives each message to handle, as the main thread does. new makes the
// listener, its state and the Handler, and CREATE makes an ExoPlayer with
// no text track. OPEN prepares the item paused. events writes the state:
// the place, the length, the size of the video and the sound tracks.
// select_track gives the track selector the second sound group. chain adds
// the next item, and the move to it takes the first item away. A target
// goes on the main thread, after the commands before it. close releases
// the ExoPlayer, and a later command does nothing. select_text gives the
// track selector the text group and turns text on. onCues keeps the bitmap
// of the image cue and not the text cue, and image_cues gives it to the
// loop as RGBA with its box. Each call ends each
// local frame that it starts, and the player keeps three global references
// until close.
func TestAndroidExoplayer(t *testing.T) {
	const src = `import "std/android/exoplayer";
import "std/android/player";
function of(l: usize, h: usize, s: usize, slot: i32): exoplayer.Player {
  return exoplayer.Player { listener: l, handler: h, state: s, slot: slot };
}
pub function make(ctx: usize): i32 {
  if let Some(p) = exoplayer.new(ctx, "dev.fern.test.Exo") {
    return p.slot;
  }
  return -1;
}
pub function act(ctx: usize, l: usize, h: usize, s: usize, slot: i32, what: i32): i64 {
  let p = of(l, h, s, slot);
  if (what == 0) {
    return if (exoplayer.open(ctx, p, "http://x/a.mkv")) { 1i64 } else { 0i64 };
  }
  if (what == 1) {
    return if (exoplayer.select_track(ctx, p, 1)) { 1i64 } else { 0i64 };
  }
  if (what == 2) {
    return if (exoplayer.select_track(ctx, p, 2)) { 1i64 } else { 0i64 };
  }
  if (what == 3) {
    return if (exoplayer.chain(ctx, p, "http://x/b.mkv")) { 1i64 } else { 0i64 };
  }
  if (what == 4) {
    return if (exoplayer.seek(ctx, p, 90000i64)) { 1i64 } else { 0i64 };
  }
  if (what == 5) {
    return if (exoplayer.seek(ctx, p, -1i64) || exoplayer.open(ctx, p, "")) { 1i64 } else { 0i64 };
  }
  if (what == 6) {
    return exoplayer.position_ms(ctx, p);
  }
  if (what == 7) {
    return exoplayer.duration_ms(ctx, p);
  }
  if (what == 8) {
    let (w, h2) = exoplayer.video_size(ctx, p);
    return (w * 10000 + h2) as i64;
  }
  if (what == 9) {
    return exoplayer.audio_tracks(ctx, p).len() as i64 * 10i64 + if (exoplayer.has_audio(ctx, p)) { 1i64 } else { 0i64 };
  }
  if (what == 10) {
    exoplayer.close_target(ctx, p, player.Target { texture: 41 as usize, surface: 42 as usize });
    return 1i64;
  }
  if (what == 11) {
    return if (exoplayer.set_speed(ctx, p, 1.5f32) && exoplayer.start(ctx, p)) { 1i64 } else { 0i64 };
  }
  if (what == 13) {
    return if (exoplayer.select_text(ctx, p, 0)) { 1i64 } else { 0i64 };
  }
  if (what == 14) {
    return if (exoplayer.select_text(ctx, p, -2)) { 1i64 } else { 0i64 };
  }
  exoplayer.close(ctx, p);
  return 1i64;
}
pub function on_message(e: usize, l: usize, m: usize): u8 {
  return exoplayer.handle(e, l, m);
}
pub function on_events(e: usize, l: usize, x: usize, ev: usize): void {
  exoplayer.events(e, l, x, ev);
}
pub function on_cues(e: usize, l: usize, g: usize): void {
  exoplayer.cues(e, l, g);
}
// The size, the red and the alpha of the first pixel, and the box of the
// one image cue, or the number of cues.
pub function pictures(ctx: usize, l: usize, h: usize, s: usize, slot: i32): i64 {
  let got = exoplayer.image_cues(ctx, of(l, h, s, slot));
  if (got.len() != 1) {
    return got.len() as i64;
  }
  let c = got[0];
  let px = c.image.pixels;
  let size = (c.image.width * 10 + c.image.height) as i64 * 1000000000i64;
  let first = px[0] as i64 * 1000000i64 + px[3] as i64 * 1000i64;
  let box = ((c.x * 10.0f32 + 0.5f32) as i64) * 100i64 + ((c.y * 10.0f32 + 0.5f32) as i64) * 10i64 + ((c.width * 10.0f32 + 0.5f32) as i64);
  return size + first + box;
}
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

enum { LISTENER_CLASS = 900, TRACKS = 1500, GROUPS = 1600, SIZE = 1700, ERROR = 1800, GROUP = 2000, TRACK_GROUP = 3000, CUE_GROUP = 4000, CUES = 4010, CUE = 4100, BITMAP = 4200 };
static int frames, globals, next = 5000, nnames, nstrings, nclasses, queued, head;
static char log_[32768];
static const char *names[512], *sigs[512];
static int arities[512];
static struct { uintptr_t id; char text[96]; } strings[512];
static struct { uintptr_t id; char name[96]; } classes[256];
static struct { uintptr_t id; int what, arg1, arg2; uintptr_t obj; } messages[64];
static uintptr_t queue[64];
static struct { uintptr_t id; uintptr_t items[4]; } arrays[16];
static int narrays;
static uintptr_t listener, handler, state, held, exo;
static int64_t longs[15];
static struct { uintptr_t id; int32_t n, v[64]; } ints[16];
static int nints;
static int types[4] = {2, 1, 1, 3}, selected[4] = {1, 1, 0, 0}, ngroups = 4;
static int playback = 2, playing, item_index, item_count = 1, events_mask;
static int64_t position = 5000, duration = 60000;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "exoplayer: %s (frames=%d globals=%d)\nlog: %s\n", what, frames, globals, log_); exit(1); } }
static uintptr_t fresh(void) { return next++; }
static const char *string_of(uintptr_t id) {
  for (int k = 0; k < nstrings; k++) if (strings[k].id == id) return strings[k].text;
  return 0;
}
static uintptr_t text(const char *s) {
  require(nstrings < 512, "too many strings");
  uintptr_t id = fresh();
  strings[nstrings].id = id;
  strcpy(strings[nstrings++].text, s);
  return id;
}
static const char *class_of(uintptr_t id) {
  for (int k = 0; k < nclasses; k++) if (classes[k].id == id) return classes[k].name;
  return "";
}
static int32_t *ints_of(uintptr_t id, int32_t *n) {
  for (int k = 0; k < nints; k++) if (ints[k].id == id) { *n = ints[k].n; return ints[k].v; }
  return 0;
}
static uintptr_t *array_of(uintptr_t id) {
  for (int k = 0; k < narrays; k++) if (arrays[k].id == id) return arrays[k].items;
  return 0;
}
static void note_call(const char *name, const uint64_t *args, int n) {
  char line[256];
  int at = snprintf(line, sizeof line, "%s(", name);
  for (int k = 0; k < n; k++) {
    const char *s = string_of(args[k]);
    if (s) at += snprintf(line + at, sizeof line - at, "%s'%s'", k ? "," : "", s);
    else at += snprintf(line + at, sizeof line - at, "%s%ld", k ? "," : "", (long)(int32_t)args[k]);
  }
  snprintf(line + at, sizeof line - at, ");");
  require(strlen(log_) + strlen(line) < sizeof log_, "the log is full");
  strcat(log_, line);
}
static int arity(const char *sig) {
  int n = 0;
  for (const char *p = sig + 1; *p != ')'; p++) {
    while (*p == '[') p++;
    if (*p == 'L') while (*p != ';') p++;
    n++;
  }
  return n;
}
// One ID for each name and descriptor, as a JVM gives.
static uintptr_t member(const char *name, const char *sig, int n) {
  for (int k = 0; k < nnames; k++) if (!strcmp(names[k], name) && !strcmp(sigs[k], sig)) return k + 1;
  require(nnames < 512, "too many member ids");
  arities[nnames] = n;
  sigs[nnames] = strdup(sig);
  names[nnames++] = strdup(name);
  return nnames;
}
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return member(name, sig, arity(sig)); }
static uintptr_t field(uintptr_t e, uintptr_t cls, const char *name, const char *sig) { return member(name, sig, 0); }
static const char *name_of(uintptr_t id) { return names[id - 1]; }
static int is(uintptr_t id, const char *name) { return !strcmp(name_of(id), name); }
static uintptr_t find_class(uintptr_t e, const char *name) {
  for (int k = 0; k < nclasses; k++) if (!strcmp(classes[k].name, name)) return classes[k].id;
  uintptr_t id = fresh();
  require(nclasses < 256, "too many classes");
  classes[nclasses].id = id;
  strcpy(classes[nclasses++].name, name);
  return id;
}
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { return fresh(); }
static uintptr_t new_object(uintptr_t e, uintptr_t cls, uintptr_t ctor, const uint64_t *args) {
  uintptr_t id = fresh();
  if (cls == LISTENER_CLASS) { listener = id; return id; }
  const char *c = class_of(cls);
  if (!strcmp(c, "java/util/concurrent/atomic/AtomicLongArray")) { state = id; return id; }
  if (!strcmp(c, "android/os/Handler")) { handler = id; note_call("handler", args, 2); return id; }
  char line[128];
  snprintf(line, sizeof line, "new %s", strrchr(c, '/') ? strrchr(c, '/') + 1 : c);
  note_call(line, args, arities[ctor - 1]);
  return id;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (is(id, "getClassLoader")) return fresh();
  if (is(id, "loadClass")) { note_call("loadClass", args, 1); return LISTENER_CLASS; }
  if (is(id, "obtainMessage")) {
    require(obj == handler && queued < 64, "a message of the Handler");
    uintptr_t m = fresh();
    messages[queued].id = m;
    messages[queued].what = (int32_t)args[0];
    messages[queued].arg1 = (int32_t)args[1];
    messages[queued].arg2 = (int32_t)args[2];
    messages[queued].obj = args[3];
    queue[queued++] = m;
    return m;
  }
  note_call(name_of(id), args, arities[id - 1]);
  if (is(id, "get") && arities[id - 1] == 0) return obj == listener ? held : 0;
  if (is(id, "get") && obj == CUES) return CUE + (uintptr_t)args[0];
  if (is(id, "get")) return GROUP + (uintptr_t)args[0];
  if (is(id, "getCurrentTracks")) return TRACKS;
  if (is(id, "getGroups")) return GROUPS;
  if (is(id, "getMediaTrackGroup")) return TRACK_GROUP + (obj - GROUP);
  if (is(id, "getVideoSize")) return SIZE;
  if (is(id, "getPlayerError")) return ERROR;
  if (is(id, "build") && exo == 0 && strstr(log_, "setAudioAttributes(") && !strstr(log_, "addListener(")) { exo = fresh(); return exo; }
  return fresh();
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (is(id, "getProperty")) return text("");
  return fresh();
}
// A jboolean leaves the bits above the low 8 of the register undefined.
static uintptr_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  int v = 0;
  if (is(id, "isSelected")) v = selected[obj - GROUP];
  else if (is(id, "isSupported")) v = 1;
  else if (is(id, "isPlaying")) v = playing;
  else if (is(id, "contains")) v = (events_mask >> (int32_t)args[0]) & 1;
  return 0x300 | v;
}
static int32_t icall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  note_call(name_of(id), args, arities[id - 1]);
  if (is(id, "size")) return obj == CUES ? 2 : ngroups;
  if (is(id, "getWidth")) return 3;
  if (is(id, "getHeight")) return 2;
  if (is(id, "getType")) return types[obj - GROUP];
  if (is(id, "getPlaybackState")) return playback;
  if (is(id, "getCurrentMediaItemIndex")) return item_index;
  if (is(id, "getMediaItemCount")) return item_count;
  return 0;
}
static int64_t lcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (is(id, "get")) { require(obj == state && args[0] < 15, "a long of the state"); return longs[args[0]]; }
  note_call(name_of(id), args, arities[id - 1]);
  if (is(id, "getCurrentPosition")) return position;
  if (is(id, "getDuration")) return duration;
  return 0;
}
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (is(id, "set") && arities[id - 1] == 2) { require(obj == state && args[0] < 15, "a long of the state"); longs[args[0]] = (int64_t)args[1]; return 0; }
  if (is(id, "set")) { require(obj == listener, "the state of the listener"); held = args[0]; return 0; }
  if (is(id, "getPixels")) {
    int32_t n, *px = ints_of(args[0], &n);
    require(px && n == 6 && obj == BITMAP, "getPixels fills an int[] of the bitmap");
    for (int k = 0; k < n; k++) px[k] = (int32_t)0x80FF0000u;
  }
  if (is(id, "sendToTarget")) { char line[64]; snprintf(line, sizeof line, "send %d;", messages[queued - 1].what); strcat(log_, line); return 0; }
  note_call(name_of(id), args, arities[id - 1]);
  return 0;
}
static int32_t int_field(uintptr_t e, uintptr_t obj, uintptr_t id) {
  for (int k = 0; k < queued; k++) if (messages[k].id == obj) {
    if (is(id, "what")) return messages[k].what;
    if (is(id, "arg1")) return messages[k].arg1;
    if (is(id, "arg2")) return messages[k].arg2;
  }
  if (is(id, "width")) return 1920;
  if (is(id, "height")) return 1080;
  if (is(id, "errorCode")) return 2001;
  return 0;
}
static uintptr_t object_field(uintptr_t e, uintptr_t obj, uintptr_t id) {
  for (int k = 0; k < queued; k++) if (messages[k].id == obj && is(id, "obj")) return messages[k].obj;
  if (is(id, "cues") && obj == CUE_GROUP) return CUES;
  if (is(id, "bitmap")) return obj == CUE ? BITMAP : 0;
  return 0;
}
// The image cue sets its box. The text cue has no bitmap.
static float float_field(uintptr_t e, uintptr_t obj, uintptr_t id) {
  if (is(id, "position")) return 0.1f;
  if (is(id, "line")) return 0.8f;
  if (is(id, "size")) return 0.5f;
  if (is(id, "bitmapHeight")) return 0.1f;
  return 0.0f;
}
static uintptr_t new_ints(uintptr_t e, int32_t n) {
  require(nints < 16 && n <= 64, "a short int[]");
  ints[nints].id = fresh();
  ints[nints].n = n;
  memset(ints[nints].v, 0, sizeof ints[nints].v);
  return ints[nints++].id;
}
static int32_t length(uintptr_t e, uintptr_t array) { int32_t n; require(ints_of(array, &n) != 0, "the length of an int[]"); return n; }
static void get_ints(uintptr_t e, uintptr_t array, int32_t start, int32_t n, int32_t *buf) { int32_t have, *v = ints_of(array, &have); require(v && start + n <= have, "a region of an int[]"); memcpy(buf, v + start, 4 * n); }
static void set_ints(uintptr_t e, uintptr_t array, int32_t start, int32_t n, const int32_t *buf) { int32_t have, *v = ints_of(array, &have); require(v && start + n <= have, "a region of an int[]"); memcpy(v + start, buf, 4 * n); }
static uintptr_t new_array(uintptr_t e, int32_t n, uintptr_t cls, uintptr_t init) {
  require(narrays < 16 && n <= 4, "a short array");
  uintptr_t id = fresh();
  arrays[narrays].id = id;
  for (int k = 0; k < 4; k++) arrays[narrays].items[k] = init;
  narrays++;
  return id;
}
static uintptr_t get_element(uintptr_t e, uintptr_t array, int32_t index) { uintptr_t *a = array_of(array); require(a != 0, "an array"); return a[index]; }
static void set_element(uintptr_t e, uintptr_t array, int32_t index, uintptr_t value) { uintptr_t *a = array_of(array); require(a != 0, "an array"); a[index] = value; }
static uintptr_t new_global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void delete_global(uintptr_t e, uintptr_t ref) { require(globals > 0, "a global reference deleted twice"); globals--; }
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) {
  char s[96];
  require(n < 96, "a short string");
  for (uintptr_t i = 0; i < n; i++) s[i] = (char)units[i];
  s[n] = 0;
  return text(s);
}
static int32_t utf_length(uintptr_t e, uintptr_t s) { return (int32_t)strlen(string_of(s)); }
static const char *utf_chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return string_of(s); }
static void utf_release(uintptr_t e, uintptr_t s, const char *chars) {}
static int32_t push(uintptr_t e, int32_t n) { frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { frames--; return 0; }
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t ref) {}
static int has(const char *part) { return strstr(log_, part) != 0; }
static int count(const char *part) {
  int n = 0;
  for (const char *p = strstr(log_, part); p; p = strstr(p + 1, part)) n++;
  return n;
}
static uintptr_t env;
static uint8_t (*handle)(uintptr_t, uintptr_t, uintptr_t);
static void (*events)(uintptr_t, uintptr_t, uintptr_t, uintptr_t);
// The main thread: each message that waits, in its order.
static void drain(void) {
  while (head < queued) require(handle((uintptr_t)&env, listener, queue[head++]) == 1 && frames == 0, "handle takes the message");
}
static void event(int mask) {
  events_mask = mask;
  events((uintptr_t)&env, listener, exo, fresh());
  require(frames == 0, "events ends its frame");
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int32_t (*make)(uintptr_t) = dlsym(lib, "make");
  int64_t (*act)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, int32_t, int32_t) = dlsym(lib, "act");
  handle = dlsym(lib, "on_message");
  events = dlsym(lib, "on_events");
  void (*on_cues)(uintptr_t, uintptr_t, uintptr_t) = dlsym(lib, "on_cues");
  int64_t (*pictures)(uintptr_t, uintptr_t, uintptr_t, uintptr_t, int32_t) = dlsym(lib, "pictures");
  require(make && act && handle && events && on_cues && pictures, "the entries");
  static uintptr_t table[240];
  uintptr_t activity[10] = {0}, ctx[40] = {0};
  int64_t senders[8] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)release;
  table[30] = (uintptr_t)new_object;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[39] = (uintptr_t)bcall;
  table[51] = (uintptr_t)icall;
  table[54] = (uintptr_t)lcall;
  table[63] = (uintptr_t)vcall;
  table[94] = (uintptr_t)field;
  table[95] = (uintptr_t)object_field;
  table[100] = (uintptr_t)int_field;
  table[102] = (uintptr_t)float_field;
  table[171] = (uintptr_t)length;
  table[179] = (uintptr_t)new_ints;
  table[203] = (uintptr_t)get_ints;
  table[211] = (uintptr_t)set_ints;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)static_object;
  table[163] = (uintptr_t)new_string;
  table[168] = (uintptr_t)utf_length;
  table[169] = (uintptr_t)utf_chars;
  table[170] = (uintptr_t)utf_release;
  table[172] = (uintptr_t)new_array;
  table[173] = (uintptr_t)get_element;
  table[174] = (uintptr_t)set_element;
  env = (uintptr_t)table;
  activity[3] = 1234;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;
  ctx[23] = 34;
  ctx[29] = (uintptr_t)senders;
  uintptr_t c = (uintptr_t)ctx;

  int32_t slot = make(c);
  require(slot == 0 && senders[0] == (int64_t)listener && frames == 0 && globals == 3, "new takes a slot and keeps three global references");
  require(has("loadClass('dev.fern.test.Exo');") && longs[4] == 1000 && longs[10] == 0 && array_of(held) && array_of(held)[0] == state, "the listener holds the state");
  require(has("getMainLooper();handler(") && has("send 1;") && queued == 1 && messages[0].obj == 1234, "CREATE goes to the main Looper with the activity");
  drain();
  require(exo != 0 && array_of(held)[1] == exo, "CREATE makes the ExoPlayer");
  require(has("new ExoPlayer$Builder(1234);new AudioAttributes$Builder();setUsage(1);setContentType(3);build();setAudioAttributes(") && has("addListener(") && has("setTrackTypeDisabled(3,1);"), "the attributes of a film, the listener, and no text track");

  log_[0] = 0;
  require(act(c, listener, handler, state, slot, 0) == 1 && frames == 0, "open sends OPEN");
  drain();
  require(has("fromUri('http://x/a.mkv');pause();setMediaItem(") && has("prepare();") && longs[11] == 1, "OPEN prepares the item paused");
  require(act(c, listener, handler, state, slot, 5) == 0 && queued == 2, "a negative seek and an empty address are refused");

  log_[0] = 0;
  playback = 3;
  event((1 << 2) | (1 << 4));
  require(longs[11] == 2 && longs[0] == 5000 && longs[2] == 60000 && longs[5] == 1920 && longs[6] == 1080, "a ready item writes its place, its length and its size");
  require(longs[7] == 2 && longs[8] == 1 && has("getProperty("), "the state counts two sound groups, one of them selected, and the change goes to the loop");
  require(act(c, listener, handler, state, slot, 9) == 21 && act(c, listener, handler, state, slot, 7) == 60000 && act(c, listener, handler, state, slot, 8) == 19201080, "the loop thread reads the state with no command");

  log_[0] = 0;
  require(act(c, listener, handler, state, slot, 2) == 0 && act(c, listener, handler, state, slot, 1) == 1, "a third sound track is refused, and the second one goes");
  drain();
  require(has("new TrackSelectionOverride(3002,0);") && has("setOverrideForType(") && has("setTrackSelectionParameters(") && longs[9] == 0, "the second sound group is the override");

  log_[0] = 0;
  require(act(c, listener, handler, state, slot, 11) == 1, "set_speed and start");
  playing = 1;
  drain();
  require(has("setPlaybackSpeed(") && has("play();") && longs[4] == 1500 && longs[3] == 1, "the speed and the playback");
  require(act(c, listener, handler, state, slot, 6) >= 5000, "the position moves on while the player plays");

  log_[0] = 0;
  require(act(c, listener, handler, state, slot, 3) == 1, "chain sends the next item");
  drain();
  require(has("fromUri('http://x/b.mkv');addMediaItem(") && longs[13] == 1 && !has("removeMediaItems("), "CHAIN adds the next item");
  item_index = 1;
  item_count = 2;
  event(1 << 1);
  require(has("removeMediaItems(0,1);") && longs[13] == 0 && longs[11] == 2, "the move to the next item takes the first one away");

  log_[0] = 0;
  require(act(c, listener, handler, state, slot, 4) == 1, "seek sends SEEK");
  drain();
  require(has("seekTo(90000);") && count("getProperty(") >= 1, "SEEK goes to the place and tells Seeked");

  log_[0] = 0;
  require(act(c, listener, handler, state, slot, 13) == 1 && act(c, listener, handler, state, slot, 14) == 0, "select_text sends TEXT, and an index below -1 is refused");
  drain();
  require(has("new TrackSelectionOverride(3003,0);") && has("setTrackTypeDisabled(3,0);") && longs[14] == 1, "TEXT gives the text group and turns text on");
  require(pictures(c, listener, handler, state, slot) == 0 && frames == 0, "no cues before onCues");
  log_[0] = 0;
  on_cues((uintptr_t)&env, listener, CUE_GROUP);
  require(frames == 0 && count("getPixels(") == 1 && has("getProperty("), "onCues keeps the one bitmap and tells the loop");
  require(array_of(held)[2] != 0 && pictures(c, listener, handler, state, slot) == 32255128185LL && frames == 0, "image_cues gives the RGBA image and its box");

  log_[0] = 0;
  globals += 2;
  act(c, listener, handler, state, slot, 10);
  require(globals == 3 && has("send 11;") && !has("release();"), "close_target deletes the references after the command goes");
  drain();
  require(count("release();") == 2, "DROP_TARGET releases the surface and its texture on the main thread");

  log_[0] = 0;
  act(c, listener, handler, state, slot, 12);
  require(globals == 0 && senders[0] == 0 && frames == 0, "close lets the references and the slot go");
  drain();
  require(has("release();") && array_of(held)[1] == 0, "RELEASE releases the ExoPlayer");
  log_[0] = 0;
  messages[queued].id = fresh();
  messages[queued].what = 3;
  queue[queued] = messages[queued].id;
  queued++;
  drain();
  require(!has("play();"), "a command after RELEASE does nothing");
  return 0;
}
`
	androidJNIMockCheck(t, src, "make,act,on_message,on_events,on_cues,pictures", check)
}

// The sound output and the display on a fake JNI. passthrough_types asks
// AudioCapabilities of Media3 for each encoding of AudioFormat, and gives
// the MIME type of each one that the output takes. match_frame_rate gives
// the rate to the Surface of the window, with a fixed source and a change
// that always goes. A device with no setFrameRate gets no call, and a
// negative rate is refused. Each call ends its local frame and deletes each
// global reference that it made.
func TestAndroidExoplayerOutput(t *testing.T) {
	const src = `import "std/android";
import "std/android/exoplayer";
pub function types(ctx: usize): i32 {
  let t = exoplayer.passthrough_types(ctx);
  let eac3 = if (t.contains("audio/eac3")) { 10 } else { 0 };
  let truehd = if (t.contains("audio/true-hd")) { 1 } else { 0 };
  return t.len() * 100 + eac3 + truehd;
}
pub function rate(ctx: usize, w: usize, milli: i32): i32 {
  return if (exoplayer.match_frame_rate(ctx, android.Window { ptr: w }, milli as f32 / 1000.0f32)) { 1 } else { 0 };
}
`
	const defined = `#include <stdint.h>
uintptr_t ANativeWindow_toSurface(uintptr_t e, uintptr_t window) { return window + 7000; }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

static int frames, globals, nnames, asked[32], nasked, absent, rate_calls;
static const char *names[64];
static uintptr_t caps_context, rate_surface;
static float rate_fps;
static int32_t rate_compat, rate_change;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "exoplayer output: %s (frames=%d globals=%d)\n", what, frames, globals); exit(1); } }
static uintptr_t member(const char *name) {
  for (int k = 0; k < nnames; k++) if (!strcmp(names[k], name)) return k + 1;
  names[nnames++] = strdup(name);
  return nnames;
}
static int is(uintptr_t id, const char *name) { return id != 0 && !strcmp(names[id - 1], name); }
static uintptr_t find_class(uintptr_t e, const char *name) { return 100; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  if (absent && !strcmp(name, "setFrameRate")) return 0;
  return member(name);
}
static uintptr_t none(uintptr_t e) { return 0; }
static int32_t push(uintptr_t e, int32_t n) { frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { frames--; return 0; }
static uintptr_t new_global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void delete_global(uintptr_t e, uintptr_t ref) { require(globals > 0, "a global reference deleted twice"); globals--; }
static void release(uintptr_t e, uintptr_t ref) {}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  require(is(id, "getCapabilities"), "a static call of getCapabilities");
  caps_context = args[0];
  return 555;
}
// A jboolean leaves the bits above the low 8 of the register undefined.
static uintptr_t bcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(is(id, "supportsEncoding") && obj == 555 && nasked < 32, "supportsEncoding of the capabilities");
  int32_t code = (int32_t)args[0];
  asked[nasked++] = code;
  return 0x300 | (code == 6 || code == 14);
}
static uintptr_t vcall(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(is(id, "setFrameRate"), "a void call of setFrameRate");
  uint32_t bits = (uint32_t)args[0];
  memcpy(&rate_fps, &bits, 4);
  rate_surface = obj;
  rate_compat = (int32_t)args[1];
  rate_change = (int32_t)args[2];
  rate_calls++;
  return 0;
}
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int32_t (*types)(uintptr_t) = dlsym(lib, "types");
  int32_t (*rate)(uintptr_t, uintptr_t, int32_t) = dlsym(lib, "rate");
  require(types && rate, "the entries");
  static uintptr_t table[240];
  static uintptr_t env;
  uintptr_t activity[10] = {0}, ctx[40] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)release;
  table[33] = (uintptr_t)method;
  table[39] = (uintptr_t)bcall;
  table[63] = (uintptr_t)vcall;
  table[113] = (uintptr_t)method;
  table[116] = (uintptr_t)static_object;
  env = (uintptr_t)table;
  activity[3] = 1234;
  ctx[2] = (uintptr_t)activity;
  ctx[4] = (uintptr_t)&env;
  ctx[23] = 34;
  uintptr_t c = (uintptr_t)ctx;

  require(types(c) == 211 && frames == 0 && globals == 0, "E-AC3 and TrueHD go through, and the call cleans up");
  require(caps_context == 1234 && nasked == 6, "the capabilities of the activity, for six encodings");

  require(rate(c, 40, 23976) == 1 && frames == 0 && globals == 0, "a frame rate goes to the window");
  require(rate_calls == 1 && rate_surface == 7040 && rate_fps > 23.975f && rate_fps < 23.977f && rate_compat == 1 && rate_change == 1, "setFrameRate on the Surface of the window, a fixed source, always");
  require(rate(c, 40, 0) == 1 && rate_fps == 0.0f && rate_calls == 2, "a rate of 0 takes the request away");
  require(rate(c, 40, -1000) == 0 && rate_calls == 2, "a negative rate is refused");
  absent = 1;
  nnames = 0;
  require(rate(c, 40, 24000) == 0 && rate_calls == 2 && frames == 0 && globals == 0, "a device with no setFrameRate gets no call");
  return 0;
}
`
	androidMockCheck(t, src, "types,rate", check, defined)
}
