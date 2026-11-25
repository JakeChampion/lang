# Sealing small values with Android Keystore

On Android API 23 and later, call `ensure_key(e, alias)` on the app's loop thread to provision an AES-256 key under an app-private alias. Use `has_key(e, alias)` to query an alias without provisioning it. Pass the current thread's JNI environment (for example, `android.env(ctx)`). Reserve the alias for this module and have one app process manage it. Concurrent threads in that process serialize key creation. The key stays in the AndroidKeyStore provider. Hardware backing depends on the device.

`seal(e, alias, value)` accepts 1 to 4096 bytes. Store its result as bytes: a fresh 12-byte provider-generated IV followed by the ciphertext and the 16-byte GCM authentication tag. `open(e, alias, sealed)` releases plaintext only after authentication. Neither operation creates a missing key. Uninstalling the app or losing the keystore entry can make stored values unrecoverable.

Use `ensure_key_with_options(e, alias, options)` when provisioning a new key. Start from `key_options()` and set the unlocked-device, user-authentication, or StrongBox requirements you need. Unavailable requirements return an error. Key generation restrictions cannot be retrofitted to an existing alias. A request with any requirement set refuses an existing key, including one previously created with those requirements. Use a dedicated alias for each policy and check `has_key` before provisioning it again.

Use `seal_with_context(e, alias, value, context)` and `open_with_context(e, alias, sealed, context)` for records that share a key. Choose a stable label for each purpose. The cipher authenticates this label alongside the ciphertext. Opening requires the same label. The original functions use empty context and can read existing envelopes.

Use `make_signing_key(e, alias, challenge, options)` to make an EC P-256 signing key with an attestation chain. It needs Android 7.0 or later. The challenge has 1 to 128 bytes, and the leaf certificate of the chain carries it. A peer gives a fresh challenge and checks the chain with `std/android/attestation`. The call refuses an alias that already has a key.

The result gives the DER SubjectPublicKeyInfo of the key and the DER chain from the leaf to the root. `signing_key(e, alias)` reads them again later. The chain keeps the challenge of the generation.

`sign(e, alias, message)` signs with SHA-256 and ECDSA and gives a DER ECDSA-Sig-Value. `delete_key(e, alias)` deletes the key of an alias.

The Android JNI test app provisions a key that requires an unlocked device. It checks legacy and context-bound envelopes, distinct IVs, and rejection of a changed tag or context. The module clears its Java plaintext input and output arrays before releasing the local frame. The caller owns the input to sealing and the plaintext returned by opening. Run its emulator check for provider behavior. Hardware backing requires testing on the intended physical device.

```fern
<<*>>=
<<session>>

<<key provision>>

<<byte arrays>>

<<sealed values>>

<<signing keys>>
```

### Session

```fern
<<session>>=
// Seal and open small values with an AES-GCM key held in Android Keystore.
import "std/android/keystore/bindings";
import "std/jni";
import "std/crypto";
import "std/result";
import "std/tls/x509";

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

function loaded(e: usize, c: bindings.Classes): Result[bindings.KeyStore, jni.JavaError] {
  let store = c.key_store.get_instance(e, PROVIDER)?;
  c.key_store.load(e, store, jni.Object { ref: 0 as usize })?;
  return Ok(store);
}

function lookup(e: usize, c: bindings.Classes, alias: string): Result[jni.Object, jni.JavaError] {
  let store = loaded(e, c)?;
  return c.key_store.get_key(e, store, alias, jni.Object { ref: 0 as usize });
}
```

### Key provision

```fern
<<key provision>>=
// Look up an alias without provisioning a replacement.
pub function has_key(e: usize, alias: string): Result[boolean, jni.JavaError] {
  let c = session(e, alias)?;
  defer close(e, c);
  let existing = lookup(e, c, alias)?;
  return Ok(!existing.is_null());
}

pub struct KeyOptions { unlocked_device_required: boolean, user_authentication_required: boolean, strongbox_required: boolean }

pub function key_options(): KeyOptions {
  return KeyOptions { unlocked_device_required: false, user_authentication_required: false, strongbox_required: false };
}

pub function ensure_key(e: usize, alias: string): Result[void, jni.JavaError] {
  return ensure_key_with_options(e, alias, key_options());
}

pub function ensure_key_with_options(e: usize, alias: string, options: KeyOptions): Result[void, jni.JavaError] {
  let c = session(e, alias)?;
  defer close(e, c);
  lock(e, c)?;
  defer unlock(e, c.key_store.class);
  let existing = lookup(e, c, alias)?;
  if (!existing.is_null()) {
    if (options.unlocked_device_required || options.user_authentication_required || options.strongbox_required) {
      return Err(failure("cannot apply key generation requirements to an existing alias"));
    }
    return Ok(());
  }
  let params = key_parameters(e, c, alias, options)?;
  let generator = c.key_generator.get_instance(e, "AES", PROVIDER)?;
  c.key_generator.init(e, generator, params)?;
  c.key_generator.generate_key(e, generator)?;
  return Ok(());
}

function key_parameters(e: usize, c: bindings.Classes, alias: string, options: KeyOptions): Result[jni.Object, jni.JavaError] {
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
  return restricted(e, c, builder, options);
}

function restricted(e: usize, c: bindings.Classes, builder: bindings.KeyGenParameterSpec_Builder, options: KeyOptions): Result[jni.Object, jni.JavaError] {
  let spec = c.key_gen_parameter_spec_builder;
  if (options.unlocked_device_required) {
    spec.set_unlocked_device_required(e, builder, true)?;
  }
  if (options.user_authentication_required) {
    spec.set_user_authentication_required(e, builder, true)?;
  }
  if (options.strongbox_required) {
    spec.set_is_strong_box_backed(e, builder, true)?;
  }
  return spec.build(e, builder);
}

// MonitorEnter on the shared Java class serializes lookup with generation and deletion.
function lock(e: usize, c: bindings.Classes): Result[void, jni.JavaError] {
  if (jni.call1(e, 217, c.key_store.class) as i32 != 0) {
    jni.check(e)?;
    return Err(failure("cannot lock key creation"));
  }
  return Ok(());
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
  // SetByteArrayRegion copies synchronously from the borrowed Fern bytes.
  unsafe {
    jni.call4(e, 208, array, 0 as usize, bytes.len() as usize, __bytes_data(bytes));
  }
  jni.check(e)?;
  return Ok(jni.Object { ref: array });
}

function read_bytes(e: usize, array: jni.Object, size: i32): Result[u8[], jni.JavaError] {
  if (array.is_null() || jni.get_array_length(e, array.ref) != size) {
    return Err(failure("unexpected provider output length"));
  }
  let out = __alloc_u8(size);
  // GetByteArrayRegion copies directly into this fresh, unshared array.
  unsafe {
    jni.call4(e, 200, array.ref, 0 as usize, size as usize, __bytes_data(out));
  }
  if let Err(err) = jni.check(e) {
    crypto.erase(out);
    return Err(err);
  }
  return Ok(out);
}

function array_bytes(e: usize, array: jni.Object, limit: i32): Result[u8[], jni.JavaError] {
  if (array.is_null()) {
    return Err(failure("missing provider output"));
  }
  let size = jni.get_array_length(e, array.ref);
  if (size < 1 || size > limit) {
    return Err(failure("unexpected provider output length"));
  }
  return read_bytes(e, array, size);
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

function nonce(e: usize, c: bindings.Classes, cipher: bindings.Cipher): Result[u8[], jni.JavaError] {
  let iv = c.cipher.get_iv(e, cipher)?;
  return read_bytes(e, iv, IV);
}

function authenticate(e: usize, c: bindings.Classes, cipher: bindings.Cipher, context: [u8]): Result[void, jni.JavaError] {
  if (context.len() > 0) {
    let aad = java_bytes(e, context)?;
    c.cipher.update_aad(e, cipher, aad)?;
  }
  return Ok(());
}

pub function seal(e: usize, alias: string, value: [u8]): Result[u8[], jni.JavaError] {
  return seal_with_context(e, alias, value, []);
}

pub function seal_with_context(e: usize, alias: string, value: [u8], context: [u8]): Result[u8[], jni.JavaError] {
  if (value.len() < 1 || value.len() > MAX_VALUE) {
    return Err(failure("value must have 1 to 4096 bytes"));
  }
  let c = session(e, alias)?;
  defer close(e, c);
  let cipher = c.cipher.get_instance(e, TRANSFORM)?;
  let handle = key(e, c, alias)?;
  c.cipher.init_int_key(e, cipher, 1, handle)?;
  authenticate(e, c, cipher, context)?;
  let iv = nonce(e, c, cipher)?;
  let body = encrypt_value(e, c, cipher, value)?;
  return Ok(iv.concat(body));
}

function encrypt_value(e: usize, c: bindings.Classes, cipher: bindings.Cipher, value: [u8]): Result[u8[], jni.JavaError] {
  let input = java_bytes(e, value)?;
  let encrypted = c.cipher.do_final(e, cipher, input);
  c.arrays.fill(e, input, 0)?;
  let output = encrypted?;
  return read_bytes(e, output, value.len() + TAG);
}

pub function open(e: usize, alias: string, sealed: [u8]): Result[u8[], jni.JavaError] {
  return open_with_context(e, alias, sealed, []);
}

pub function open_with_context(e: usize, alias: string, sealed: [u8], context: [u8]): Result[u8[], jni.JavaError] {
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
  authenticate(e, c, cipher, context)?;
  let input = java_bytes(e, sealed[IV:])?;
  let output = c.cipher.do_final(e, cipher, input)?;
  return take_plaintext(e, c, output, sealed.len() - IV - TAG);
}

function take_plaintext(e: usize, c: bindings.Classes, output: jni.Object, size: i32): Result[u8[], jni.JavaError] {
  if (output.is_null()) {
    return Err(failure("missing provider output"));
  }
  let plain = read_bytes(e, output, size);
  if let Err(err) = c.arrays.fill(e, output, 0) {
    if let Ok(bytes) = plain {
      crypto.erase(bytes);
    }
    return Err(err);
  }
  return plain;
}
```

### Signing keys

```fern
<<signing keys>>=
const ATTESTED: string = "EC";

const SIGNATURE: string = "SHA256withECDSA";

const MAX_CHALLENGE: i32 = 128;

const MAX_CHAIN: i32 = 16;

const MAX_CERTIFICATE: i32 = 65536;

const MAX_SIGNATURE: i32 = 72;

const MAX_MESSAGE: i32 = 1048576;

// The DER SubjectPublicKeyInfo of the key, and its attestation chain from the leaf to the root.
pub struct SigningKey { public_key: u8[], chain: u8[][] }

// Makes an EC P-256 signing key whose leaf certificate carries `challenge`.
pub function make_signing_key(e: usize, alias: string, challenge: [u8], options: KeyOptions): Result[SigningKey, jni.JavaError] {
  if (challenge.len() < 1 || challenge.len() > MAX_CHALLENGE) {
    return Err(failure("challenge must have 1 to 128 bytes"));
  }
  let c = session(e, alias)?;
  defer close(e, c);
  if (!c.key_gen_parameter_spec_builder.has_set_attestation_challenge()) {
    return Err(failure("key attestation needs Android 7.0 or later"));
  }
  lock(e, c)?;
  defer unlock(e, c.key_store.class);
  let existing = lookup(e, c, alias)?;
  if (!existing.is_null()) {
    return Err(failure("alias already has a key"));
  }
  let params = signing_parameters(e, c, alias, challenge, options)?;
  let generator = c.key_pair_generator.get_instance(e, ATTESTED, PROVIDER)?;
  c.key_pair_generator.initialize(e, generator, params)?;
  c.key_pair_generator.generate_key_pair(e, generator)?;
  return attested(e, c, alias);
}

function signing_parameters(e: usize, c: bindings.Classes, alias: string, challenge: [u8], options: KeyOptions): Result[jni.Object, jni.JavaError] {
  let spec = c.key_gen_parameter_spec_builder;
  // PURPOSE_SIGN | PURPOSE_VERIFY
  let builder = spec.new(e, alias, 12)?;
  // An EC key of 256 bits is on the curve P-256.
  spec.set_key_size(e, builder, 256)?;
  let digests = jni.string_array(e, ["SHA-256"]);
  jni.check(e)?;
  spec.set_digests(e, builder, jni.Object { ref: digests })?;
  let nonce = java_bytes(e, challenge)?;
  spec.set_attestation_challenge(e, builder, nonce)?;
  return restricted(e, c, builder, options);
}

// Reads the public key and the attestation chain of an existing signing key.
pub function signing_key(e: usize, alias: string): Result[SigningKey, jni.JavaError] {
  let c = session(e, alias)?;
  defer close(e, c);
  return attested(e, c, alias);
}

function attested(e: usize, c: bindings.Classes, alias: string): Result[SigningKey, jni.JavaError] {
  let store = loaded(e, c)?;
  let certs = c.key_store.get_certificate_chain(e, store, alias)?;
  if (certs.is_null()) {
    return Err(failure("missing key"));
  }
  let n = jni.get_array_length(e, certs.ref);
  if (n < 1 || n > MAX_CHAIN) {
    return Err(failure("unexpected certificate chain length"));
  }
  let chain: u8[][] = [];
  for i in 0..n {
    chain = chain.append(encoded(e, c, certs, i)?);
  }
  return match (x509.parse(chain[0])) {
    Ok(leaf) => Ok(SigningKey { public_key: leaf.spki, chain: chain }),
    Err(err) => Err(failure("leaf certificate: " + err.message())),
  };
}

function encoded(e: usize, c: bindings.Classes, certs: jni.Object, i: i32): Result[u8[], jni.JavaError] {
  let cert = jni.get_object_array_element(e, certs.ref, i);
  jni.check(e)?;
  defer jni.delete_local_ref(e, cert);
  let der = c.certificate.get_encoded(e, bindings.Certificate { ref: cert })?;
  defer jni.delete_local_ref(e, der.ref);
  return array_bytes(e, der, MAX_CERTIFICATE);
}

// Signs `message` with SHA-256 and ECDSA. The result is a DER ECDSA-Sig-Value.
pub function sign(e: usize, alias: string, message: [u8]): Result[u8[], jni.JavaError] {
  if (message.len() > MAX_MESSAGE) {
    return Err(failure("message must have at most 1048576 bytes"));
  }
  let c = session(e, alias)?;
  defer close(e, c);
  let handle = key(e, c, alias)?;
  let signer = c.signature.get_instance(e, SIGNATURE)?;
  c.signature.init_sign(e, signer, handle)?;
  if (message.len() > 0) {
    let input = java_bytes(e, message)?;
    c.signature.update(e, signer, input)?;
  }
  let sig = c.signature.sign(e, signer)?;
  return array_bytes(e, sig, MAX_SIGNATURE);
}

// Deletes the key of an alias. A missing alias is not an error.
pub function delete_key(e: usize, alias: string): Result[void, jni.JavaError] {
  let c = session(e, alias)?;
  defer close(e, c);
  lock(e, c)?;
  defer unlock(e, c.key_store.class);
  let store = loaded(e, c)?;
  return c.key_store.delete_entry(e, store, alias);
}
```
