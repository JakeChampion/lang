package e2e

// androidBindingsMock is the start of the C source of a JNI mock for a
// module that calls Java through typed jnibind wrappers. It keeps the names
// of the classes and of the members that the wrappers look up, and it
// counts the local references, the local frames and the global references.
// A test sets the on_* pointers for the calls that it expects, makes a
// context with mock_ctx, and calls clean after each call of the module.
const androidBindingsMock = `#include <assert.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

enum { ACTIVITY = 100, CLASSES = 2000, TEXTS = 5000, EXCEPTION = 9000, ARRAY = 9100 };
static char class_names[64][80], texts[128][96], array_texts[8][96];
static struct { char name[48], sig[160]; } members[256];
static int class_count, member_count, text_count, array_count, live, saved_live, frames, globals, pending;

static uintptr_t (*on_object)(uintptr_t obj, const char *name, const uintptr_t *args);
static uintptr_t (*on_static_object)(const char *cls, const char *name, const uintptr_t *args);
static int32_t (*on_int)(uintptr_t obj, const char *name, const uintptr_t *args);
static int32_t (*on_static_int)(const char *cls, const char *name, const uintptr_t *args);
static int (*on_boolean)(uintptr_t obj, const char *name, const uintptr_t *args);
static void (*on_void)(uintptr_t obj, const char *name, const uintptr_t *args);
static float (*on_float_field)(uintptr_t obj, const char *name);

static void fail(const char *what, const char *name) { fprintf(stderr, "JNI mock: %s %s\n", what, name); abort(); }
static uintptr_t local(uintptr_t ref) { if (ref) live++; return ref; }
static const char *class_name(uintptr_t cls) { assert(cls > CLASSES && cls <= CLASSES + (uintptr_t)class_count); return class_names[cls - CLASSES - 1]; }
static const char *member_name(uintptr_t id) { assert(id >= 1 && id <= (uintptr_t)member_count); return members[id - 1].name; }
static const char *text(uintptr_t ref) { assert(ref >= TEXTS && ref < TEXTS + (uintptr_t)text_count); return texts[ref - TEXTS]; }
// A Java string with the text s, for the result of an on_object call.
static uintptr_t java_text(const char *s) {
  assert(text_count < 128 && strlen(s) < 96);
  strcpy(texts[text_count], s);
  return TEXTS + text_count++;
}
static uintptr_t find(uintptr_t e, const char *name) {
  for (int i = 0; i < class_count; i++) if (!strcmp(class_names[i], name)) return local(CLASSES + i + 1);
  assert(class_count < 64 && strlen(name) < 80);
  strcpy(class_names[class_count], name);
  return local(CLASSES + ++class_count);
}
static uintptr_t class_of(uintptr_t e, uintptr_t obj) { assert(obj == EXCEPTION); return find(e, "java/lang/Throwable"); }
static uintptr_t member(uintptr_t e, uintptr_t cls, const char *name, const char *sig) {
  assert(cls > CLASSES && !pending);
  for (int i = 0; i < member_count; i++) if (!strcmp(members[i].name, name) && !strcmp(members[i].sig, sig)) return i + 1;
  assert(member_count < 256 && strlen(name) < 48 && strlen(sig) < 160);
  strcpy(members[member_count].name, name); strcpy(members[member_count].sig, sig);
  return ++member_count;
}
static uintptr_t object(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj && !pending);
  if (obj == EXCEPTION) return 0;
  if (!on_object) fail("an object call:", member_name(id));
  return local(on_object(obj, member_name(id), args));
}
static uintptr_t static_object(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  if (!on_static_object) fail("a static object call:", member_name(id));
  return local(on_static_object(class_name(cls), member_name(id), args));
}
static int32_t int_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj);
  if (!on_int) fail("an int call:", member_name(id));
  return on_int(obj, member_name(id), args);
}
static int32_t static_int(uintptr_t e, uintptr_t cls, uintptr_t id, const uintptr_t *args) {
  if (!on_static_int) fail("a static int call:", member_name(id));
  return on_static_int(class_name(cls), member_name(id), args);
}
static uint8_t boolean_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj);
  if (!on_boolean) fail("a boolean call:", member_name(id));
  return (uint8_t)on_boolean(obj, member_name(id), args);
}
static void void_call(uintptr_t e, uintptr_t obj, uintptr_t id, const uintptr_t *args) {
  assert(obj);
  if (!on_void) fail("a void call:", member_name(id));
  on_void(obj, member_name(id), args);
}
static float float_field(uintptr_t e, uintptr_t obj, uintptr_t id) {
  assert(obj);
  if (!on_float_field) fail("a float field:", member_name(id));
  return on_float_field(obj, member_name(id));
}
static uintptr_t string(uintptr_t e, const uint16_t *s, int n) {
  char buf[96];
  assert(n >= 0 && n < 96);
  for (int i = 0; i < n; i++) buf[i] = (char)s[i];
  buf[n] = 0;
  return local(java_text(buf));
}
static int utf_length(uintptr_t e, uintptr_t ref) { return (int)strlen(text(ref)); }
static const char *utf_chars(uintptr_t e, uintptr_t ref, void *copy) { return text(ref); }
static void utf_release(uintptr_t e, uintptr_t ref, const char *chars) {}
// One String[]: the texts of its elements are in array_texts.
static uintptr_t object_array(uintptr_t e, int n, uintptr_t cls, uintptr_t init) { assert(n <= 8); array_count = n; return local(ARRAY); }
static void set_element(uintptr_t e, uintptr_t array, int i, uintptr_t value) { assert(array == ARRAY && i < array_count); strcpy(array_texts[i], text(value)); }
static int push(uintptr_t e, int n) { assert(!frames && n > 0); frames++; saved_live = live; return 0; }
static uintptr_t pop(uintptr_t e, uintptr_t keep) { assert(frames == 1 && !keep); frames--; live = saved_live; return 0; }
static uintptr_t global(uintptr_t e, uintptr_t ref) { assert(ref); globals++; return ref; }
static void delete_global(uintptr_t e, uintptr_t ref) { assert(ref && globals > 0); globals--; }
// DeleteLocalRef takes a null reference and does nothing.
static void delete_local(uintptr_t e, uintptr_t ref) { if (!ref) return; assert(live > 0); live--; }
static uintptr_t exception(uintptr_t e) { return pending ? local(EXCEPTION) : 0; }
static void clear(uintptr_t e) { pending = 0; }
// Each call of a module leaves no local reference, no frame and no global reference of its own.
static void clean(void) {
  if (live || frames || globals || pending) { fprintf(stderr, "JNI mock: live=%d frames=%d globals=%d pending=%d\n", live, frames, globals, pending); abort(); }
  text_count = 0;
}
static uintptr_t table[240], env_table, activity[10], context[40];
static uintptr_t mock_ctx(void) {
  uintptr_t entries[][2] = {{6, (uintptr_t)find}, {15, (uintptr_t)exception}, {17, (uintptr_t)clear}, {19, (uintptr_t)push},
    {20, (uintptr_t)pop}, {21, (uintptr_t)global}, {22, (uintptr_t)delete_global}, {23, (uintptr_t)delete_local},
    {31, (uintptr_t)class_of}, {33, (uintptr_t)member}, {36, (uintptr_t)object}, {39, (uintptr_t)boolean_call},
    {51, (uintptr_t)int_call}, {63, (uintptr_t)void_call}, {94, (uintptr_t)member}, {102, (uintptr_t)float_field},
    {113, (uintptr_t)member}, {116, (uintptr_t)static_object}, {131, (uintptr_t)static_int}, {144, (uintptr_t)member},
    {163, (uintptr_t)string}, {168, (uintptr_t)utf_length}, {169, (uintptr_t)utf_chars}, {170, (uintptr_t)utf_release},
    {172, (uintptr_t)object_array}, {174, (uintptr_t)set_element}};
  for (unsigned i = 0; i < sizeof(entries) / sizeof(*entries); i++) table[entries[i][0]] = entries[i][1];
  env_table = (uintptr_t)table;
  activity[3] = ACTIVITY;
  context[2] = (uintptr_t)activity;
  context[4] = (uintptr_t)&env_table;
  return (uintptr_t)context;
}
`
