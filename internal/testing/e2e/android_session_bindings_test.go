package e2e

import "testing"

// TestAndroidSessionTransport checks each method that set_transport_actions
// calls against its signature in android.media.session. A wrong signature
// finds no method, and the next JNI call then ends the process on a device.
func TestAndroidSessionTransport(t *testing.T) {
	const src = `import "std/android/session";
pub function publish(ctx: usize): void {
  let rewind = session.TransportAction { id: "rewind", label: "Back 15 seconds", icon: 17301542 };
  session.set_transport_actions(ctx, session.Session { obj: 7 as usize, callback: 0 as usize }, 6, 1500i64, 1.0f32, 1823i64, [rewind]);
}
`
	const check = `#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define BUILDER "Landroid/media/session/PlaybackState$Builder;"
static const char *known[][2] = {
  {"<init>", "()V"},
  {"setActions", "(J)" BUILDER},
  {"setState", "(IJF)" BUILDER},
  {"addCustomAction", "(Ljava/lang/String;Ljava/lang/String;I)" BUILDER},
  {"build", "()Landroid/media/session/PlaybackState;"},
  {"setPlaybackState", "(Landroid/media/session/PlaybackState;)V"},
};
static int custom, published;
static uintptr_t find(uintptr_t e, const char *name) {
  assert(!strcmp(name, "android/media/session/PlaybackState$Builder")); return 1;
}
static uintptr_t exception(uintptr_t e) { return 0; }
static void clear(uintptr_t e) {}
static void drop(uintptr_t e, uintptr_t ref) {}
static uintptr_t class_of(uintptr_t e, uintptr_t obj) { assert(obj); return 1; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  for (unsigned i = 0; i < sizeof(known) / sizeof(*known); i++)
    if (!strcmp(name, known[i][0])) {
      if (strcmp(sig, known[i][1])) { fprintf(stderr, "%s has the signature %s, not %s\n", name, known[i][1], sig); assert(0); }
      return (uintptr_t)known[i][0];
    }
  fprintf(stderr, "no such method: %s\n", name); assert(0); return 0;
}
static uintptr_t create(uintptr_t e, uintptr_t cls, const char *ctor, const uintptr_t *args) { assert(ctor); return 50; }
static uintptr_t invoke(uintptr_t e, uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(obj && name);
  if (!strcmp(name, "setActions")) assert(obj == 50 && args[0] == 1823);
  if (!strcmp(name, "setState")) assert(obj == 50 && (int)args[0] == 6 && args[1] == 1500);
  if (!strcmp(name, "addCustomAction")) { assert(obj == 50 && args[0] == 200 && args[1] == 200 && (int)args[2] == 17301542); custom++; }
  if (!strcmp(name, "build")) { assert(obj == 50); return 60; }
  if (!strcmp(name, "setPlaybackState")) { assert(obj == 7 && args[0] == 60); published++; }
  return 50;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) { return 200; }
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW); assert(lib);
  void (*publish)(uintptr_t) = dlsym(lib, "publish"); assert(publish);
  uintptr_t table[240] = {[6]=(uintptr_t)find,[15]=(uintptr_t)exception,[17]=(uintptr_t)clear,[23]=(uintptr_t)drop,
    [30]=(uintptr_t)create,[31]=(uintptr_t)class_of,[33]=(uintptr_t)method,[36]=(uintptr_t)invoke,[63]=(uintptr_t)invoke,
    [163]=(uintptr_t)string};
  uintptr_t env = (uintptr_t)table, e = (uintptr_t)&env, activity[10] = {[3]=100}, ctx[40] = {[2]=(uintptr_t)activity,[4]=e,[23]=36};
  publish((uintptr_t)ctx);
  assert(custom == 1 && published == 1);
  return 0;
}
`
	androidJNIMockCheck(t, src, "publish", check)
}
