# Sealing small values with Android Keystore

On Android API 23 and later, call `ensure_key(e, alias)` on the app's loop thread to provision an AES-256 key under an app-private alias. Pass the current thread's JNI environment (for example, `android.env(ctx)`). Reserve the alias for this module and have one app process manage it. Concurrent threads in that process serialize key creation. The key stays in the AndroidKeyStore provider. Hardware backing depends on the device.

`seal(e, alias, value)` accepts 1 to 4096 bytes. Store its result as bytes: a fresh 12-byte provider-generated IV followed by the ciphertext and the 16-byte GCM authentication tag. `open(e, alias, sealed)` releases plaintext only after authentication. Neither operation creates a missing key. Uninstalling the app or losing the keystore entry can make stored values unrecoverable.

The Android JNI test app provisions the same alias twice, seals and opens a value, checks that repeated seals differ and refuses a changed tag. Run its emulator check for provider behavior. Hardware backing requires testing on the intended physical device.

```fern
<<*>>=
<<session>>

<<key provision>>

<<byte arrays>>

<<sealed values>>
```

### Session

```fern
<<session>>=
// Seal and open small values with an AES-GCM key held in Android Keystore.
import "std/android/keystore/bindings";
import "std/jni";
import "std/result";

const PROVIDER: string = "AndroidKeyStore";

const TRANSFORM: string = "AES/GCM/NoPadding";

const IV: i32 = 12;

const TAG: i32 = 16;

const MAX_VALUE: i32 = 4096;

function failure(reason: string): jni.JavaError {
  return jni.JavaError { text: "keystore: " + reason };
}

function session(e: usize, alias: string): Result[bindings.Classes, jni.JavaError] {
  if (alias.len() == 0 || e == 0 as usize) {
    return Err(failure("expected an alias and a JNI environment"));
  }
  if (!jni.push_local_frame(e, 64)) {
    return Err(failure("cannot create JNI local frame"));
  }
  let classes = bindings.classes(e);
  if let Err(err) = classes {
    jni.pop_local_frame(e, 0 as usize);
    return Err(err);
  }
  return classes;
}

function close(e: usize, c: bindings.Classes): void {
  c.release(e);
  jni.pop_local_frame(e, 0 as usize);
}

function lookup(e: usize, c: bindings.Classes, alias: string): Result[jni.Object, jni.JavaError] {
  let store = c.key_store.get_instance(e, PROVIDER)?;
  let none = jni.Object { ref: 0 as usize };
  c.key_store.load(e, store, none)?;
  return c.key_store.get_key(e, store, alias, none);
}
```

### Key provision

```fern
<<key provision>>=
pub function ensure_key(e: usize, alias: string): Result[void, jni.JavaError] {
  let c = session(e, alias)?;
  defer close(e, c);
  // MonitorEnter and MonitorExit on the shared Java class cover lookup and generation.
  if (jni.call1(e, 217, c.key_store.class) as i32 != 0) {
    jni.check(e)?;
    return Err(failure("cannot lock key creation"));
  }
  defer unlock(e, c.key_store.class);
  let existing = lookup(e, c, alias)?;
  if (!existing.is_null()) {
    return Ok(());
  }
  let params = key_parameters(e, c, alias)?;
  let generator = c.key_generator.get_instance(e, "AES", PROVIDER)?;
  c.key_generator.init(e, generator, params)?;
  c.key_generator.generate_key(e, generator)?;
  return Ok(());
}

function key_parameters(e: usize, c: bindings.Classes, alias: string): Result[jni.Object, jni.JavaError] {
  let spec = c.key_gen_parameter_spec_builder;
  // PURPOSE_ENCRYPT | PURPOSE_DECRYPT
  let builder = spec.new(e, alias, 3)?;
  spec.set_key_size(e, builder, 256)?;
  let modes = jni.string_array(e, ["GCM"]);
  jni.check(e)?;
  spec.set_block_modes(e, builder, jni.Object { ref: modes })?;
  let padding = jni.string_array(e, ["NoPadding"]);
  jni.check(e)?;
  spec.set_encryption_paddings(e, builder, jni.Object { ref: padding })?;
  return spec.build(e, builder);
}

function unlock(e: usize, class: usize): void {
  jni.call1(e, 218, class);
}
```

### Byte arrays

```fern
<<byte arrays>>=
function java_bytes(e: usize, bytes: [u8]): Result[jni.Object, jni.JavaError] {
  let array = jni.new_byte_array(e, bytes.len());
  jni.check(e)?;
  jni.set_byte_array_region(e, array, 0, bytes);
  jni.check(e)?;
  return Ok(jni.Object { ref: array });
}

function read_bytes(e: usize, array: jni.Object, size: i32): Result[u8[], jni.JavaError] {
  if (array.is_null() || jni.get_array_length(e, array.ref) != size) {
    return Err(failure("unexpected provider output length"));
  }
  let out = __alloc_u8(size);
  // GetByteArrayElements / ReleaseByteArrayElements with JNI_ABORT (read only).
  let ptr = jni.call2(e, 184, array.ref, 0 as usize);
  jni.check(e)?;
  if (ptr == 0 as usize) {
    return Err(failure("cannot read provider output"));
  }
  unsafe {
    for i in 0..size {
      out = out.with(i, __load_u8(ptr + i as usize) as u8);
    }
  }
  jni.call3(e, 192, array.ref, ptr, 2 as usize);
  return Ok(out);
}
```

### Sealed values

```fern
<<sealed values>>=
function key(e: usize, c: bindings.Classes, alias: string): Result[jni.Object, jni.JavaError] {
  let found = lookup(e, c, alias)?;
  if (found.is_null()) {
    return Err(failure("missing key"));
  }
  return Ok(found);
}

function finish(e: usize, c: bindings.Classes, cipher: bindings.Cipher, bytes: [u8], size: i32): Result[u8[], jni.JavaError] {
  let input = java_bytes(e, bytes)?;
  let output = c.cipher.do_final(e, cipher, input)?;
  return read_bytes(e, output, size);
}

function nonce(e: usize, c: bindings.Classes, cipher: bindings.Cipher): Result[u8[], jni.JavaError] {
  let iv = c.cipher.get_iv(e, cipher)?;
  return read_bytes(e, iv, IV);
}

pub function seal(e: usize, alias: string, value: [u8]): Result[u8[], jni.JavaError] {
  if (value.len() < 1 || value.len() > MAX_VALUE) {
    return Err(failure("value must have 1 to 4096 bytes"));
  }
  let c = session(e, alias)?;
  defer close(e, c);
  let cipher = c.cipher.get_instance(e, TRANSFORM)?;
  let handle = key(e, c, alias)?;
  c.cipher.init_int_key(e, cipher, 1, handle)?;
  let iv = nonce(e, c, cipher)?;
  let body = finish(e, c, cipher, value, value.len() + TAG)?;
  return Ok(iv.concat(body));
}

pub function open(e: usize, alias: string, sealed: [u8]): Result[u8[], jni.JavaError] {
  if (sealed.len() <= IV + TAG || sealed.len() > IV + TAG + MAX_VALUE) {
    return Err(failure("invalid envelope length"));
  }
  let c = session(e, alias)?;
  defer close(e, c);
  let cipher = c.cipher.get_instance(e, TRANSFORM)?;
  let handle = key(e, c, alias)?;
  let iv = java_bytes(e, sealed[:IV])?;
  let spec = c.gcmparameter_spec.new(e, TAG * 8, iv)?;
  c.cipher.init_int_key_algorithm_parameter_spec(e, cipher, 2, handle, spec.object())?;
  return finish(e, c, cipher, sealed[IV:], sealed.len() - IV - TAG);
}
```
