package e2e

import "testing"

func TestAndroidWidgetBindings(t *testing.T) {
	const src = `import "std/android/widget";
pub function show(e: usize): boolean {
  return widget.show(e, 100 as usize, "app.Row", widget.Look { texts: [(0, "Ready")], actions: [(1, "next")], opens: [2], starts: [(3, "open")] });
}
pub function action(e: usize): boolean {
  return widget.action(e, 101 as usize) == "test";
}
`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <string.h>

static int frames, globals, taps, updates, fail_update, pending;
static uintptr_t push(uintptr_t e, uintptr_t capacity) { assert(!frames); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames && !globals); frames--; return 0; }
static uintptr_t find(uintptr_t e, const char *name) { assert(frames); return 1; }
static uintptr_t class_of(uintptr_t e, uintptr_t obj) { return 1; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void release(uintptr_t e, uintptr_t ref) { assert(globals); globals--; }
static void drop(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending; }
static void clear(uintptr_t e) { pending = 0; }
static const char *methods[] = { "<init>", "getActivity", "getBroadcast", "getInstance", "getPackageManager",
  "getPackageName", "getAction", "setAction", "setClassName", "getLaunchIntentForPackage",
  "setOnClickPendingIntent", "setTextViewText", "updateAppWidget", "toString" };
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  for (unsigned i = 0; i < sizeof(methods)/sizeof(*methods); i++)
    if (!strcmp(name, methods[i])) return (uintptr_t)methods[i];
  assert(0); return 0;
}
static uintptr_t invoke(uintptr_t e, uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(obj && frames && globals);
  if (!strcmp(name, "getBroadcast")) assert(args[0] == 100 && args[1] == 1 && args[3] == 201326592);
  if (!strcmp(name, "getActivity")) assert(args[1] == 66 || args[1] == 131);
  if (!strcmp(name, "setOnClickPendingIntent")) { taps++; assert(args[0] == 2130968576 + taps); }
  if (!strcmp(name, "updateAppWidget")) { assert(taps == 3); updates++; pending = fail_update; }
  return 200;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) { return 200; }
static uintptr_t length(uintptr_t e, uintptr_t s) { return 4; }
static const char *chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return "test"; }
static void done(uintptr_t e, uintptr_t s, const char *p) {}

int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*show)(uintptr_t) = dlsym(lib, "show"), (*action)(uintptr_t) = dlsym(lib, "action");
  assert(show && action);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release, [23]=(uintptr_t)drop,
    [31]=(uintptr_t)class_of, [30]=(uintptr_t)invoke, [33]=(uintptr_t)method, [36]=(uintptr_t)invoke, [63]=(uintptr_t)invoke,
    [113]=(uintptr_t)method, [116]=(uintptr_t)invoke, [163]=(uintptr_t)string, [168]=(uintptr_t)length,
    [169]=(uintptr_t)chars, [170]=(uintptr_t)done};
  uintptr_t env = (uintptr_t)table;
  assert(action((uintptr_t)&env) && !frames && !globals);
  assert(show((uintptr_t)&env) && updates == 1 && !frames && !globals);
  taps = 0; fail_update = 1;
  assert(!show((uintptr_t)&env) && updates == 2 && !pending && !frames && !globals);
  return 0;
}
`
	androidJNIMockCheck(t, src, "show,action", check)
}
