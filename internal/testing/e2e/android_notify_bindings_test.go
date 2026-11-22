package e2e

import "testing"

func TestAndroidNotifyBindings(t *testing.T) {
	const src = `import "std/android/notify";
pub function send(ctx: usize): boolean {
  let n = notify.note(42, "news", "Title", "Body");
  return notify.post(ctx, notify.Note { ...n, progress: 110 });
}
pub function channel(ctx: usize): void {
  notify.channel(ctx, notify.Channel { id: "news", name: "News", importance: notify.DEFAULT });
}
pub function cancel(ctx: usize): void { notify.cancel(ctx, 42); }
`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <string.h>

static int frames, globals, posted, cancelled, channels, builders, legacy, enabled = 1, pending;
static const char *fail;
static uintptr_t push(uintptr_t e, int capacity) { assert(!frames); frames++; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames && !globals && !pending); frames--; return 0; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { globals++; return ref; }
static void release(uintptr_t e, uintptr_t ref) { assert(globals); globals--; }
static void drop(uintptr_t e, uintptr_t ref) {}
static uintptr_t find(uintptr_t e, const char *name) {
  assert(frames);
  if (!strcmp(name, "android/app/NotificationChannel")) { assert(!legacy); return 2; }
  if (!strcmp(name, "android/app/Notification$Builder")) return 3;
  return 1;
}
static const char *methods[] = { "getSystemService", "getPackageManager", "getPackageName", "cancel",
  "notify", "createNotificationChannel", "areNotificationsEnabled", "setContentTitle", "setContentText",
  "setSmallIcon", "setAutoCancel", "setOngoing", "setProgress", "setContentIntent", "getLaunchIntentForPackage",
  "getActivity", "build", "toString" };
static uintptr_t method(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  if (!strcmp(name, "<init>")) {
    if (cls == 2) return (uintptr_t)"channel";
    if (!strcmp(sig, "(Landroid/content/Context;)V")) return (uintptr_t)"old";
    if (legacy) { pending = 1; return 0; }
    return (uintptr_t)"modern";
  }
  if (!strcmp(name, "createNotificationChannel") && legacy) { pending = 1; return 0; }
  for (unsigned i=0; i<sizeof(methods)/sizeof(*methods); i++)
    if (!strcmp(name, methods[i])) return (uintptr_t)methods[i];
  assert(0); return 0;
}
static uintptr_t invoke(uintptr_t e, uintptr_t obj, const char *name, const uintptr_t *args) {
  assert(frames && globals && obj);
  if (!strcmp(name,"old") || !strcmp(name,"modern")) { builders++; assert(!strcmp(name,legacy ? "old" : "modern")); }
  if (!strcmp(name,"setProgress")) assert(args[0] == 100 && args[1] == 100 && !args[2]);
  if (!strcmp(name,"notify")) { posted++; assert(args[0] == 42 && args[1]); }
  if (!strcmp(name,"cancel")) { cancelled++; assert(args[0] == 42); }
  if (!strcmp(name,"createNotificationChannel")) { channels++; assert(args[0]); }
  if (!strcmp(name,"areNotificationsEnabled")) return 0x100 | enabled;
  if (fail && !strcmp(name,fail)) pending = 1;
  return 200;
}
static uintptr_t class_of(uintptr_t e, uintptr_t obj) { return 1; }
static uintptr_t exception(uintptr_t e) { return pending; }
static void clear(uintptr_t e) { pending = 0; }
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) { return 200; }
static int length(uintptr_t e, uintptr_t s) { return 4; }
static const char *chars(uintptr_t e, uintptr_t s, uintptr_t copy) { return "test"; }
static void done(uintptr_t e, uintptr_t s, const char *p) {}

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1],RTLD_NOW); assert(lib);
  int (*send)(uintptr_t)=dlsym(lib,"send");
  void (*channel)(uintptr_t)=dlsym(lib,"channel"), (*cancel)(uintptr_t)=dlsym(lib,"cancel");
  assert(send && channel && cancel);
  uintptr_t table[240] = {[6]=(uintptr_t)find,[15]=(uintptr_t)exception,[17]=(uintptr_t)clear,[19]=(uintptr_t)push,
    [20]=(uintptr_t)pop,[21]=(uintptr_t)global,[22]=(uintptr_t)release,[23]=(uintptr_t)drop,[30]=(uintptr_t)invoke,
    [31]=(uintptr_t)class_of,[33]=(uintptr_t)method,[36]=(uintptr_t)invoke,[39]=(uintptr_t)invoke,[63]=(uintptr_t)invoke,
    [113]=(uintptr_t)method,[116]=(uintptr_t)invoke,[163]=(uintptr_t)string,[168]=(uintptr_t)length,
    [169]=(uintptr_t)chars,[170]=(uintptr_t)done};
  uintptr_t env=(uintptr_t)table, activity[10]={[3]=100}, ctx[40]={[2]=(uintptr_t)activity,[4]=(uintptr_t)&env,[23]=26};
  uintptr_t context=(uintptr_t)ctx;
  channel(context); assert(channels == 1);
  assert(send(context) && posted == 1 && builders == 1);
  fail="setContentTitle"; assert(!send(context) && posted == 1);
  fail="notify"; assert(!send(context) && posted == 2);
  fail=0; enabled=0; assert(!send(context) && builders == 3);
  legacy=1; enabled=1; ctx[23]=24;
  channel(context); assert(channels == 1);
  assert(send(context) && posted == 3 && builders == 4);
  cancel(context); assert(cancelled == 1 && !frames && !globals && !pending);
  return 0;
}
`
	androidJNIMockCheck(t, src, "send,channel,cancel", check)
}
