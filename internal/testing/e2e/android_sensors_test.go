package e2e

import "testing"

// API 26 selects the package-specific NDK manager. The JNI lookup must
// release its class and string even when applications repeatedly call has.
func TestAndroidSensorManager(t *testing.T) {
	const src = `import "std/android/sensors";
pub function has_light(ctx: usize): i32 {
  return if (sensors.has(ctx, sensors.LIGHT)) { 1 } else { 0 };
}
`
	const ndk = `
#include <string.h>
void *ASensorManager_getInstance(void) { return (void *)1; }
void *ASensorManager_getInstanceForPackage(const char *name) {
  return strcmp(name, "dev.fern.sensors") == 0 ? (void *)2 : 0;
}
void *ASensorManager_getDefaultSensor(void *manager, int kind) {
  return kind == 5 ? manager : 0;
}
`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <string.h>

static int frames, globals, calls, strings, deny_frame, clears;
static int push(uintptr_t env, int n) { if (deny_frame) return -1; frames++; return 0; }
static uintptr_t pop(uintptr_t env, uintptr_t keep) { assert(!globals); frames--; return 0; }
static uintptr_t none(uintptr_t env) { return 0; }
static void clear(uintptr_t env) { clears++; }
static uintptr_t find(uintptr_t env, const char *name) {
  assert(frames == 1 && !strcmp(name, "android/content/Context")); return 10;
}
static uintptr_t global(uintptr_t env, uintptr_t obj) { globals++; return obj; }
static void release(uintptr_t env, uintptr_t obj) { assert(obj == 10); globals--; }
static void local(uintptr_t env, uintptr_t obj) { if (obj == 12) strings--; }
static uintptr_t method(uintptr_t env, uintptr_t cls, const char *name, const char *sig) {
  assert(cls == 10 && !strcmp(name, "getPackageName") && !strcmp(sig, "()Ljava/lang/String;")); return 11;
}
static uintptr_t object(uintptr_t env, uintptr_t obj, uintptr_t id, uintptr_t args) {
  assert(globals == 1 && obj == 1234 && id == 11); calls++; strings++; return 12;
}
static int length(uintptr_t env, uintptr_t obj) { return strlen("dev.fern.sensors"); }
static const char *chars(uintptr_t env, uintptr_t obj, uintptr_t copy) { return "dev.fern.sensors"; }
static void done(uintptr_t env, uintptr_t obj, const char *p) {}

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*has)(uintptr_t) = dlsym(lib, "has_light");
  assert(has);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)none, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release,
    [23]=(uintptr_t)local, [33]=(uintptr_t)method, [36]=(uintptr_t)object, [168]=(uintptr_t)length,
    [169]=(uintptr_t)chars, [170]=(uintptr_t)done};
  uintptr_t activity[10] = {[3]=1234}, env=(uintptr_t)table;
  uintptr_t ctx[40] = {[2]=(uintptr_t)activity, [4]=(uintptr_t)&env, [23]=25};
  assert(has((uintptr_t)ctx) && calls == 0);
  ctx[23] = 26;
  for (int i = 1; i <= 3; i++) {
    assert(has((uintptr_t)ctx) && calls == i && !frames && !globals && !strings);
  }
  deny_frame = 1;
  assert(!has((uintptr_t)ctx) && calls == 3 && clears == 1 && !frames && !globals);
  return 0;
}
`
	androidMockCheck(t, src, "has_light", check, ndk)
}
