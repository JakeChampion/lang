# Java Native Interface calls

This module calls JNI methods from Fern.

## Model

The JNI environment supplies a table of function pointers. Typed helpers select table entries and marshal arguments into JNI call forms.

## Contracts

Environment pointers, object references, method identifiers, and field identifiers must belong to the active JNI context. Pointer-sized integers do not validate that context.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function method_ptr(env: usize, index: i32): usize>>

<<3 pub function get_string_utf_length(env: usize, jstr: usize): i32>>

<<4 pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calls JNI methods from Fern.
```

### `2 pub function method_ptr(env: usize, index: i32): usize`

```fern
<<2 pub function method_ptr(env: usize, index: i32): usize>>=
pub function method_ptr(env: usize, index: i32): usize {
  let table: usize = __load_ptr(env);
  return __load_ptr(table + (index * 8) as usize);
}

pub function call0(env: usize, index: i32): usize {
  return __c_call1(method_ptr(env, index), env);
}

pub function call1(env: usize, index: i32, a0: usize): usize {
  return __c_call2(method_ptr(env, index), env, a0);
}

pub function call2(env: usize, index: i32, a0: usize, a1: usize): usize {
  return __c_call3(method_ptr(env, index), env, a0, a1);
}

pub function call3(env: usize, index: i32, a0: usize, a1: usize, a2: usize): usize {
  return __c_call4(method_ptr(env, index), env, a0, a1, a2);
}

pub function call1_f32(env: usize, index: i32, a0: usize): f32 {
  return __c_call2_f32(method_ptr(env, index), env, a0);
}

pub function call1_f64(env: usize, index: i32, a0: usize): f64 {
  return __c_call2_f64(method_ptr(env, index), env, a0);
}

pub function call2_f32(env: usize, index: i32, a0: usize, a1: usize): f32 {
  return __c_call3_f32(method_ptr(env, index), env, a0, a1);
}

pub function call2_f64(env: usize, index: i32, a0: usize, a1: usize): f64 {
  return __c_call3_f64(method_ptr(env, index), env, a0, a1);
}

pub function call3_f32(env: usize, index: i32, a0: usize, a1: usize, a2: usize): f32 {
  return __c_call4_f32(method_ptr(env, index), env, a0, a1, a2);
}

pub function call3_f64(env: usize, index: i32, a0: usize, a1: usize, a2: usize): f64 {
  return __c_call4_f64(method_ptr(env, index), env, a0, a1, a2);
}

pub function FIND_CLASS(): i32 {
  return 6;
}

pub function GET_METHOD_ID(): i32 {
  return 33;
}

pub function GET_FIELD_ID(): i32 {
  return 94;
}

pub function GET_STATIC_METHOD_ID(): i32 {
  return 113;
}

pub function GET_STATIC_FIELD_ID(): i32 {
  return 144;
}

pub function NEW_STRING_UTF(): i32 {
  return 167;
}

pub function GET_STRING_UTF_LENGTH(): i32 {
  return 168;
}

pub function GET_STRING_UTF_CHARS(): i32 {
  return 169;
}

pub function RELEASE_STRING_UTF_CHARS(): i32 {
  return 170;
}

pub function cstr(s: string): usize {
  let n: i32 = s.len();
  let buf: usize = __alloc(n + 4);
  let i: i32 = 0;
  while (i + 4 <= n) {
    let w: i32 = s[i] as i32 | s[i + 1] as i32 << 8 | s[i + 2] as i32 << 16 | s[i + 3] as i32 << 24;
    __store_i32(buf + i as usize, w);
    i = i + 4;
  }
  let base: i32 = i;
  let t: i32 = 0;
  let sh: i32 = 0;
  while (i < n) {
    t = t | s[i] as i32 << sh;
    sh = sh + 8;
    i = i + 1;
  }
  __store_i32(buf + base as usize, t);
  return buf;
}

pub function get_version(env: usize): i32 {
  return call0(env, 4) as i32;
}

pub function find_class(env: usize, name: usize): usize {
  return call1(env, FIND_CLASS(), name);
}

pub function get_object_class(env: usize, obj: usize): usize {
  return call1(env, 31, obj);
}

pub function get_method_id(env: usize, clazz: usize, name: usize, sig: usize): usize {
  return call3(env, GET_METHOD_ID(), clazz, name, sig);
}

pub function get_field_id(env: usize, clazz: usize, name: usize, sig: usize): usize {
  return call3(env, GET_FIELD_ID(), clazz, name, sig);
}

pub function get_static_method_id(env: usize, clazz: usize, name: usize, sig: usize): usize {
  return call3(env, GET_STATIC_METHOD_ID(), clazz, name, sig);
}

pub function get_static_field_id(env: usize, clazz: usize, name: usize, sig: usize): usize {
  return call3(env, GET_STATIC_FIELD_ID(), clazz, name, sig);
}

pub function new_string_utf(env: usize, bytes: usize): usize {
  return call1(env, NEW_STRING_UTF(), bytes);
}
```

### `3 pub function get_string_utf_length(env: usize, jstr: usize): i32`

```fern
<<3 pub function get_string_utf_length(env: usize, jstr: usize): i32>>=
pub function get_string_utf_length(env: usize, jstr: usize): i32 {
  return call1(env, GET_STRING_UTF_LENGTH(), jstr) as i32;
}

pub function get_string_utf_chars(env: usize, jstr: usize, is_copy: usize): usize {
  return call2(env, GET_STRING_UTF_CHARS(), jstr, is_copy);
}

pub function release_string_utf_chars(env: usize, jstr: usize, chars: usize): usize {
  return call2(env, RELEASE_STRING_UTF_CHARS(), jstr, chars);
}

pub function throw(env: usize, throwable: usize): i32 {
  return call1(env, 13, throwable) as i32;
}

pub function throw_new(env: usize, clazz: usize, message: usize): i32 {
  return call2(env, 14, clazz, message) as i32;
}

pub function exception_occurred(env: usize): usize {
  return call0(env, 15);
}

pub function exception_describe(env: usize): usize {
  return call0(env, 16);
}

pub function exception_clear(env: usize): usize {
  return call0(env, 17);
}

pub function new_global_ref(env: usize, obj: usize): usize {
  return call1(env, 21, obj);
}

pub function delete_global_ref(env: usize, gref: usize): usize {
  return call1(env, 22, gref);
}

pub function delete_local_ref(env: usize, lref: usize): usize {
  return call1(env, 23, lref);
}

pub function new_local_ref(env: usize, ref: usize): usize {
  return call1(env, 25, ref);
}

pub function get_superclass(env: usize, clazz: usize): usize {
  return call1(env, 10, clazz);
}

pub function is_assignable_from(env: usize, sub: usize, sup: usize): i32 {
  return call2(env, 11, sub, sup) as i32;
}

pub function is_same_object(env: usize, a: usize, b: usize): i32 {
  return call2(env, 24, a, b) as i32;
}

pub function alloc_object(env: usize, clazz: usize): usize {
  return call1(env, 27, clazz);
}

pub function is_instance_of(env: usize, obj: usize, clazz: usize): i32 {
  return call2(env, 32, obj, clazz) as i32;
}

pub function get_object_field(env: usize, obj: usize, field: usize): usize {
  return call2(env, 95, obj, field);
}

pub function get_boolean_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 96, obj, field) as i32;
}

pub function get_byte_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 97, obj, field) as i32;
}

pub function get_char_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 98, obj, field) as i32;
}

pub function get_short_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 99, obj, field) as i32;
}

pub function get_int_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 100, obj, field) as i32;
}

pub function get_long_field(env: usize, obj: usize, field: usize): usize {
  return call2(env, 101, obj, field);
}

pub function get_float_field(env: usize, obj: usize, field: usize): f32 {
  return call2_f32(env, 102, obj, field);
}

pub function get_double_field(env: usize, obj: usize, field: usize): f64 {
  return call2_f64(env, 103, obj, field);
}

pub function set_object_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 104, obj, field, value);
}

pub function set_boolean_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 105, obj, field, value);
}

pub function set_byte_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 106, obj, field, value);
}

pub function set_char_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 107, obj, field, value);
}

pub function set_short_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 108, obj, field, value);
}

pub function set_int_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 109, obj, field, value);
}

pub function set_long_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 110, obj, field, value);
}

pub function get_static_object_field(env: usize, clazz: usize, field: usize): usize {
  return call2(env, 145, clazz, field);
}
```

### `4 pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32`

```fern
<<4 pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32>>=
pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32 {
  return call2(env, 150, clazz, field) as i32;
}

pub function get_static_long_field(env: usize, clazz: usize, field: usize): usize {
  return call2(env, 151, clazz, field);
}

pub function get_static_float_field(env: usize, clazz: usize, field: usize): f32 {
  return call2_f32(env, 152, clazz, field);
}

pub function get_static_double_field(env: usize, clazz: usize, field: usize): f64 {
  return call2_f64(env, 153, clazz, field);
}

pub function get_array_length(env: usize, array: usize): i32 {
  return call1(env, 171, array) as i32;
}

pub function jvalues(args: usize[]): usize {
  let n: i32 = args.len();
  let buf: usize = __alloc(n * 8);
  let i: i32 = 0;
  while (i < n) {
    __store_i64(buf + (i * 8) as usize, args[i] as i64);
    i = i + 1;
  }
  return buf;
}

pub function jvalue_alloc(n: i32): usize {
  let buf: usize = __alloc(n * 8);
  let i: i32 = 0;
  while (i < n) {
    __store_i64(buf + (i * 8) as usize, 0 as i64);
    i = i + 1;
  }
  return buf;
}

pub function jvalue_set_int(args: usize, idx: i32, v: usize): usize {
  __store_i64(args + (idx * 8) as usize, v as i64);
  return args;
}

pub function jvalue_set_long(args: usize, idx: i32, v: i64): usize {
  __store_i64(args + (idx * 8) as usize, v);
  return args;
}

pub function jvalue_set_f32(args: usize, idx: i32, x: f32): usize {
  __store_i32(args + (idx * 8) as usize, f32_bits(x));
  return args;
}

pub function jvalue_set_f64(args: usize, idx: i32, d: f64): usize {
  __store_i64(args + (idx * 8) as usize, f64_bits(d));
  return args;
}

pub function call_object_method_a(env: usize, obj: usize, method: usize, args: usize): usize {
  return call3(env, 36, obj, method, args);
}

pub function call_boolean_method_a(env: usize, obj: usize, method: usize, args: usize): i32 {
  return call3(env, 39, obj, method, args) as i32;
}

pub function call_int_method_a(env: usize, obj: usize, method: usize, args: usize): i32 {
  return call3(env, 51, obj, method, args) as i32;
}

pub function call_long_method_a(env: usize, obj: usize, method: usize, args: usize): usize {
  return call3(env, 54, obj, method, args);
}

pub function call_float_method_a(env: usize, obj: usize, method: usize, args: usize): f32 {
  return call3_f32(env, 57, obj, method, args);
}

pub function call_double_method_a(env: usize, obj: usize, method: usize, args: usize): f64 {
  return call3_f64(env, 60, obj, method, args);
}

pub function call_void_method_a(env: usize, obj: usize, method: usize, args: usize): usize {
  return call3(env, 63, obj, method, args);
}
```
