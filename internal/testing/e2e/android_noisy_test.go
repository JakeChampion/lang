package e2e

import "testing"

// registerReceiver returns the last sticky intent, which can be null on success.
// The JNI exception distinguishes a refused registration from that success.
func TestAndroidNoisyReceiver(t *testing.T) {
	const src = `import "std/android/noisy";
pub function listen(ctx: usize): i32 {
  let Some(n) = noisy.start(ctx, "dev.fern.tune.Noisy") else { return 0; };
  noisy.stop(ctx, n);
  return 1;
}`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum { ACTIVITY=100, APP, LOADER, CLASS, RECEIVER, FILTER, CLASS_NAME, ACTION, EXCEPTION };
enum { APPLICATION=1, GET_LOADER, LOAD, NEW_INSTANCE, CONSTRUCTOR, REGISTER, UNREGISTER, TO_STRING };
static int frames, globals, registrations, removals, pending, refuse, no_ref;
static uintptr_t find(uintptr_t e, const char *name) { assert(frames == 1); return 1; }
static uintptr_t get_class(uintptr_t e, uintptr_t obj) { assert(obj == EXCEPTION); return 1; }
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  const char *names[] = {"", "getApplicationContext", "getClassLoader", "loadClass", "newInstance",
    "<init>", "registerReceiver", "unregisterReceiver", "toString"};
  for (int i=1; i<=TO_STRING; i++) if (!strcmp(name,names[i])) return i;
  abort();
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  switch (id) {
  case APPLICATION: assert(obj == ACTIVITY); return APP;
  case GET_LOADER: assert(obj == ACTIVITY); return LOADER;
  case LOAD: assert(obj == LOADER && args[0] == CLASS_NAME); return CLASS;
  case NEW_INSTANCE: assert(obj == CLASS); return RECEIVER;
  case REGISTER:
    assert(obj == APP && args[0] == RECEIVER && args[1] == FILTER);
    registrations++; pending=refuse; return 0;
  case TO_STRING: assert(obj == EXCEPTION); return 0;
  default: abort();
  }
}
static uintptr_t construct(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  assert(id == CONSTRUCTOR && args[0] == ACTION); return FILTER;
}
static uintptr_t void_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(id == UNREGISTER && obj == APP && args[0] == RECEIVER);
  removals++; return 0;
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) {
  char buf[80];
  assert(n >= 0 && n < sizeof(buf));
  for (int i=0; i<n; i++) buf[i]=(char)s[i];
  buf[n]=0;
  if (!strcmp(buf,"dev.fern.tune.Noisy")) return CLASS_NAME;
  assert(!strcmp(buf,"android.media.AUDIO_BECOMING_NOISY")); return ACTION;
}
static int push(uintptr_t e, int n) { assert(!frames); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames == 1); frames--; return 0; }
static uintptr_t global(uintptr_t e, uintptr_t ref) {
  if (no_ref && ref == RECEIVER) { pending=1; return 0; }
  globals++; return ref;
}
static void release(uintptr_t e, uintptr_t ref) { assert(globals>0); globals--; }
static void local(uintptr_t e, uintptr_t ref) {}
static uintptr_t exception(uintptr_t e) { return pending ? EXCEPTION : 0; }
static void clear(uintptr_t e) { pending=0; }
static void clean(void) { assert(!frames && !globals && !pending); }

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1], RTLD_NOW);
  assert(lib);
  int (*listen_)(uintptr_t)=dlsym(lib,"listen");
  assert(listen_);
  uintptr_t table[240] = {[6]=(uintptr_t)find, [15]=(uintptr_t)exception, [17]=(uintptr_t)clear,
    [19]=(uintptr_t)push, [20]=(uintptr_t)pop, [21]=(uintptr_t)global, [22]=(uintptr_t)release,
    [23]=(uintptr_t)local, [30]=(uintptr_t)construct, [31]=(uintptr_t)get_class, [33]=(uintptr_t)method,
    [36]=(uintptr_t)object, [63]=(uintptr_t)void_call, [163]=(uintptr_t)string};
  uintptr_t activity[10] = {[3]=ACTIVITY}, env=(uintptr_t)table, ctx[40] = {[2]=(uintptr_t)activity, [4]=(uintptr_t)&env};
  refuse=1;
  assert(!listen_((uintptr_t)ctx) && registrations==1 && removals==0); clean();
  refuse=0; no_ref=1;
  assert(!listen_((uintptr_t)ctx) && registrations==1 && removals==0); clean();
  no_ref=0;
  assert(listen_((uintptr_t)ctx) && registrations==2 && removals==1); clean();
  return 0;
}
`
	androidJNIMockCheck(t, src, "listen", check)
}
