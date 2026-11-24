package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/literate"
)

// brightness sets the brightness of the window on the UI thread through
// SetFloatField, and the volume calls read and set the music stream on the
// loop thread. Each call releases the local references that it makes.
func TestAndroidBrightnessAndVolume(t *testing.T) {
	raw, err := os.ReadFile(e2eharness.RepoPath("internal", "stdlib", "std", "android.fern.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := literate.Parse(string(raw)).Tangle()
	if err != nil {
		t.Fatal(err)
	}
	src += `
pub function context_size(): i32 { return Context.c_size() as i32; }
pub function wire(ctx: usize, activity: usize, read: i32, write: i32, e: usize): void {
  unsafe {
    Context.c_store_activity(ctx, activity);
    Context.c_store_ui_read(ctx, read);
    Context.c_store_ui_write(ctx, write);
    Context.c_store_env(ctx, e);
  }
}
pub function light(ctx: usize, permille: i32): void { brightness(ctx, permille as f32 / 1000.0f32); }
pub function requests(ctx: usize, read: i32): i32 { return on_ui_request(read, 0, ctx); }
pub function level(ctx: usize): i32 { let (v, m) = volume(ctx); return v * 100 + m; }
pub function set_level(ctx: usize, v: i32): void { set_volume(ctx, v); }
pub function system_light(ctx: usize): i32 { return (system_brightness(ctx) * 1000.0f32) as i32; }
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <fcntl.h>
#include <unistd.h>

static int locals, saved_locals, frames, globals, attrs, volume_set = -1;
static float level = 9.0f;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "%s: locals=%d attrs=%d\n", what, locals, attrs); exit(1); } }

enum { WINDOW = 300, PARAMS = 400, FIELD = 500, AUDIO = 600, RESOLVER = 700, SETTINGS = 800 };
static const char *names[] = { "", "getWindow", "getAttributes", "", "", "setAttributes",
  "getSystemService", "getStreamVolume", "getStreamMaxVolume", "setStreamVolume", "getContentResolver", "getInt" };

static uintptr_t get_class(uintptr_t e, uintptr_t obj) { require(obj != 0, "class of null"); locals++; return obj + 1000; }
// The bindings look up each class by its name and keep a global reference to it.
static uintptr_t find_class(uintptr_t e, const char *name) {
  locals++;
  if (!strcmp(name, "android/provider/Settings$System")) return SETTINGS;
  if (!strcmp(name, "android/content/Context")) return SETTINGS + 1;
  require(!strcmp(name, "android/media/AudioManager"), name);
  return SETTINGS + 2;
}
static uintptr_t new_global(uintptr_t e, uintptr_t obj) { require(obj >= SETTINGS && obj <= SETTINGS + 2, "global of a class"); globals++; return obj; }
static void delete_global(uintptr_t e, uintptr_t obj) { require(globals > 0, "delete of a global"); globals--; }
static int32_t push(uintptr_t e, int32_t n) { require(frames == 0, "one frame"); frames++; saved_locals = locals; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { require(frames == 1 && keep == 0, "pop"); frames--; locals = saved_locals; return 0; }
// The bindings look up each member of a class. The calls of the test use the members of the names table.
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  for (uintptr_t i = 1; i < sizeof names / sizeof names[0]; i++) {
    if (!strcmp(name, names[i])) return i;
  }
  return 99;
}
static uintptr_t new_string(uintptr_t e, const uint16_t *units, uintptr_t n) { locals++; return 900 + n; }
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  locals++;
  if (id == 1) { require(obj == 100, "getWindow"); return WINDOW; }
  if (id == 2) { require(obj == WINDOW, "getAttributes"); return PARAMS; }
  if (id == 6) { require(obj == 100 && args[0] == 900 + 5, "getSystemService audio"); return AUDIO; }
  require(id == 10 && obj == 100, "getContentResolver");
  return RESOLVER;
}
static void call(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  if (id == 5) {
    require(obj == WINDOW && args[0] == PARAMS, "setAttributes");
    attrs++;
  } else {
    require(id == 9 && obj == AUDIO && args[0] == 3 && args[2] == 0, "setStreamVolume");
    volume_set = (int)args[1];
  }
}
static uintptr_t field_id(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(cls == PARAMS + 1000 && !strcmp(name, "screenBrightness") && !strcmp(sig, "F"), "GetFieldID screenBrightness");
  return FIELD;
}
// SetFloatField takes its value in the first vector register.
static void set_float(uintptr_t e, uintptr_t obj, uintptr_t field, float v) {
  require(obj == PARAMS && field == FIELD, "SetFloatField");
  level = v;
}
static int32_t call_int(uintptr_t e, uintptr_t obj, uintptr_t id, const uint64_t *args) {
  require(obj == AUDIO && args[0] == 3, "stream");
  return id == 7 ? 7 : 15;
}
static uintptr_t static_method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  require(cls == SETTINGS && !strcmp(name, "getInt") && !strcmp(sig, "(Landroid/content/ContentResolver;Ljava/lang/String;I)I"), "getInt");
  return 11;
}
static int32_t call_static_int(uintptr_t e, uintptr_t cls, uintptr_t id, const uint64_t *args) {
  require(cls == SETTINGS && id == 11 && args[0] == RESOLVER && args[1] == 900 + 17 && args[2] == 128, "getInt args");
  return 51;
}
static uintptr_t none(uintptr_t e) { return 0; }
static void release(uintptr_t e, uintptr_t obj) { require(obj != 0, "release of null"); locals--; }

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  int (*context_size)(void) = dlsym(lib, "context_size");
  void (*wire)(uintptr_t, uintptr_t, int, int, uintptr_t) = dlsym(lib, "wire");
  void (*light)(uintptr_t, int) = dlsym(lib, "light");
  int (*requests)(uintptr_t, int) = dlsym(lib, "requests");
  int (*get_level)(uintptr_t) = dlsym(lib, "level");
  void (*set_level)(uintptr_t, int) = dlsym(lib, "set_level");
  int (*system_light)(uintptr_t) = dlsym(lib, "system_light");
  require(context_size && wire && light && requests && get_level && set_level && system_light, "exports");
  static uintptr_t table[240];
  uintptr_t activity[8] = {0};
  table[6] = (uintptr_t)find_class;
  table[15] = (uintptr_t)none;
  table[17] = (uintptr_t)none;
  table[19] = (uintptr_t)push;
  table[20] = (uintptr_t)pop;
  table[21] = (uintptr_t)new_global;
  table[22] = (uintptr_t)delete_global;
  table[23] = (uintptr_t)release;
  table[31] = (uintptr_t)get_class;
  table[33] = (uintptr_t)method;
  table[36] = (uintptr_t)object;
  table[51] = (uintptr_t)call_int;
  table[63] = (uintptr_t)call;
  table[94] = (uintptr_t)field_id;
  table[111] = (uintptr_t)set_float;
  table[113] = (uintptr_t)static_method;
  table[131] = (uintptr_t)call_static_int;
  table[163] = (uintptr_t)new_string;
  uintptr_t env = (uintptr_t)table;
  uintptr_t *ctx = calloc(1, context_size());
  int fds[2];
  require(pipe(fds) == 0 && fcntl(fds[0], F_SETFL, O_NONBLOCK) == 0, "pipe");
  wire((uintptr_t)ctx, (uintptr_t)activity, fds[0], fds[1], (uintptr_t)&env);
  activity[2] = (uintptr_t)&env;
  activity[3] = 100;
  activity[7] = (uintptr_t)ctx;

  light((uintptr_t)ctx, 250);
  require(attrs == 0, "brightness ran on the loop thread");
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(attrs == 1 && level == 0.25f && locals == 0, "brightness 0.25");
  light((uintptr_t)ctx, -1000);
  require(requests((uintptr_t)ctx, fds[0]) == 1, "requests");
  require(attrs == 2 && level == -1.0f && locals == 0, "brightness of the system");

  require(get_level((uintptr_t)ctx) == 715 && locals == 0, "volume");
  set_level((uintptr_t)ctx, 4);
  require(volume_set == 4 && locals == 0, "set_volume");
  require(system_light((uintptr_t)ctx) == 200 && locals == 0, "system_brightness");
  require(frames == 0 && globals == 0, "frames and global references");
  return 0;
}
`
	androidJNIMockCheck(t, src, "context_size,wire,light,requests,level,set_level,system_light", check)
}

// The float and double field setters of std/jni pass their value in the
// first vector register, as the C ABI of JNI wants, with env, the object and
// the field in the first integer registers.
func TestJNIFloatFieldSetters(t *testing.T) {
	const src = `import "std/jni";
pub function set_all(env: usize): void {
  jni.set_float_field(env, 11 as usize, 21 as usize, 1.5f32);
  jni.set_double_field(env, 12 as usize, 22 as usize, -2.25);
  jni.set_static_float_field(env, 13 as usize, 23 as usize, 0.125f32);
  jni.set_static_double_field(env, 14 as usize, 24 as usize, 1e300);
}
`
	const check = `#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <dlfcn.h>

static int seen;
static void require(int ok, const char *what) { if (!ok) { fprintf(stderr, "%s\n", what); exit(1); } }
static uintptr_t env_seen;
static void setf(uintptr_t e, uintptr_t obj, uintptr_t field, float v) { require(e == env_seen && obj == 11 && field == 21 && v == 1.5f, "SetFloatField"); seen |= 1; }
static void setd(uintptr_t e, uintptr_t obj, uintptr_t field, double v) { require(e == env_seen && obj == 12 && field == 22 && v == -2.25, "SetDoubleField"); seen |= 2; }
static void setsf(uintptr_t e, uintptr_t cls, uintptr_t field, float v) { require(e == env_seen && cls == 13 && field == 23 && v == 0.125f, "SetStaticFloatField"); seen |= 4; }
static void setsd(uintptr_t e, uintptr_t cls, uintptr_t field, double v) { require(e == env_seen && cls == 14 && field == 24 && v == 1e300, "SetStaticDoubleField"); seen |= 8; }

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 2; }
  void (*set_all)(uintptr_t) = dlsym(lib, "set_all");
  require(set_all != NULL, "export");
  static uintptr_t table[240];
  table[111] = (uintptr_t)setf;
  table[112] = (uintptr_t)setd;
  table[161] = (uintptr_t)setsf;
  table[162] = (uintptr_t)setsd;
  uintptr_t env = (uintptr_t)table;
  env_seen = (uintptr_t)&env;
  set_all((uintptr_t)&env);
  require(seen == 15, "not every setter ran");
  return 0;
}
`
	androidJNIMockCheck(t, src, "set_all", check)
}
