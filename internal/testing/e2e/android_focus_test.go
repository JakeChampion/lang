package e2e

import "testing"

func TestAndroidAudioFocus(t *testing.T) {
	const src = `import "std/android/focus";
pub function exercise(ctx: usize, music: i32): i32 {
  let made = if (music == 1) { focus.music(ctx, "test.Listener") } else { focus.new(ctx, "test.Listener") };
  let Some(f) = made else { return -1; };
  let granted = focus.request(ctx, f);
  focus.close(ctx, f);
  return if (granted) { 1 } else { 0 };
}`
	const check = `
#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef uintptr_t ref;
enum { ACTIVITY=100, MANAGER, LOADER, LISTENER_CLASS, LISTENER, ATTR_BUILDER, ATTRS,
       REQ_BUILDER, REQUEST, AUDIO_NAME, LISTENER_NAME, EXCEPTION };
enum { SERVICE=1, GET_LOADER, LOAD, INSTANCE, ATTR_INIT, REQ_INIT, USAGE, CONTENT,
       BUILD_ATTR, BUILD_REQ, SET_ATTR, SET_LISTENER, ASK, ABANDON, TO_STRING };
static int frames, refs[256], music, granted, requests, releases, pending, missing, fail_ref, throw_on;

static ref find(ref e, const char *name) {
  assert(frames == 1);
  if (!strcmp(name,"android/media/AudioAttributes$Builder")) return 2;
  if (!strcmp(name,"android/media/AudioFocusRequest$Builder")) return 3;
  return 1;
}
static ref method(ref e, ref cls, const char *name, const char *sig) {
  if (!strcmp(name,"<init>")) return cls==2 ? ATTR_INIT : REQ_INIT;
  if (!strcmp(name,"build")) return cls==2 ? BUILD_ATTR : BUILD_REQ;
  const char *names[] = {"", "getSystemService", "getClassLoader", "loadClass", "newInstance",
      "", "", "setUsage", "setContentType", "", "", "setAudioAttributes",
      "setOnAudioFocusChangeListener", "requestAudioFocus", "abandonAudioFocusRequest", "toString"};
  for (int i=1; i<=TO_STRING; ++i) if (!strcmp(name,names[i])) return i;
  abort();
}
static ref object(ref e, ref obj, ref id, const ref *args) {
  switch (id) {
  case SERVICE: assert(obj==ACTIVITY && args[0]==AUDIO_NAME); return missing ? 0 : MANAGER;
  case GET_LOADER: assert(obj==ACTIVITY); return LOADER;
  case LOAD: assert(obj==LOADER && args[0]==LISTENER_NAME); return LISTENER_CLASS;
  case INSTANCE: assert(obj==LISTENER_CLASS); return LISTENER;
  case USAGE: assert(obj==ATTR_BUILDER && args[0]==(music?1:2)); return obj;
  case CONTENT: assert(obj==ATTR_BUILDER && args[0]==(music?2:1)); return obj;
  case BUILD_ATTR: assert(obj==ATTR_BUILDER); return ATTRS;
  case SET_ATTR: assert(obj==REQ_BUILDER && args[0]==ATTRS); return obj;
  case SET_LISTENER: assert(obj==REQ_BUILDER && args[0]==LISTENER); return obj;
  case BUILD_REQ: assert(obj==REQ_BUILDER); return REQUEST;
  case TO_STRING: assert(obj==EXCEPTION); return 0;
  default: abort();
  }
}
static ref construct(ref e, ref cls, ref id, const ref *args) {
  if (cls==2) { assert(id==ATTR_INIT); return ATTR_BUILDER; }
  assert(cls==3 && id==REQ_INIT && args[0]==(music?1:2)); return REQ_BUILDER;
}
static int integer(ref e, ref obj, ref id, const ref *args) {
  assert(frames && obj==MANAGER && args[0]==REQUEST && refs[MANAGER] && refs[REQUEST] && refs[LISTENER]);
  if (id==throw_on) pending=1;
  if (id==ASK) { ++requests; return granted; }
  assert(id==ABANDON); ++releases; return 1;
}
static ref string(ref e, const uint16_t *chars, int n) {
  char s[32]; assert(n>=0 && n<32);
  for (int i=0; i<n; ++i) s[i]=(char)chars[i];
  s[n]=0;
  if (!strcmp(s,"audio")) return AUDIO_NAME;
  assert(!strcmp(s,"test.Listener")); return LISTENER_NAME;
}
static ref global(ref e, ref obj) {
  if (obj==fail_ref) { pending=1; return 0; }
  assert(obj && obj<256); ++refs[obj]; return obj;
}
static void drop(ref e, ref obj) { assert(refs[obj]>0); --refs[obj]; }
static int push(ref e, int cap) { assert(!frames && cap>0); ++frames; return 0; }
static ref pop(ref e, ref keep) { assert(frames==1 && !keep); --frames; return 0; }
static ref exception(ref e) { return pending ? EXCEPTION : 0; }
static void clear(ref e) { pending=0; }
static ref get_class(ref e, ref obj) { assert(obj==EXCEPTION); return 1; }
static void local(ref e, ref obj) {}
static void clean(void) {
  assert(!frames && !pending);
  for (int i=0; i<256; ++i) assert(!refs[i]);
}

int main(int argc, char **argv) {
  void *lib=dlopen(argv[1],RTLD_NOW); assert(lib);
  int (*run)(ref,int)=dlsym(lib,"exercise"); assert(run);
  ref table[240] = {[6]=(ref)find,[15]=(ref)exception,[17]=(ref)clear,[19]=(ref)push,[20]=(ref)pop,
    [21]=(ref)global,[22]=(ref)drop,[23]=(ref)local,[30]=(ref)construct,[31]=(ref)get_class,
    [33]=(ref)method,[36]=(ref)object,[51]=(ref)integer,[163]=(ref)string};
  ref activity[10]={[3]=ACTIVITY}, env=(ref)table, ctx[40]={[2]=(ref)activity,[4]=(ref)&env};
  for (music=0; music<=1; ++music) for (granted=0; granted<=1; ++granted) {
    assert(run((ref)ctx,music)==granted); clean();
  }
  assert(requests==4 && releases==4);
  missing=1; assert(run((ref)ctx,0)==-1); clean(); missing=0; music=0;
  int objects[]={MANAGER,REQUEST,LISTENER};
  for (int i=0; i<3; ++i) {
    fail_ref=objects[i]; assert(run((ref)ctx,0)==-1); clean();
  }
  fail_ref=0; granted=1;
  throw_on=ASK; assert(run((ref)ctx,0)==0); clean();
  throw_on=ABANDON; assert(run((ref)ctx,0)==1); clean();
  assert(requests==6 && releases==6);
  return 0;
}`
	androidJNIMockCheck(t, src, "exercise", check)
}
