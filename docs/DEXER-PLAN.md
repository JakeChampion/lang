# A DEX compiler in Fern

Status: [plan] the order of the work for item 6 of `docs/ANDROID-STANDING.md`.

`fern -apk` writes an APK with no Android SDK, no Gradle and no JDK. A
Java or Kotlin library comes as a JAR or an AAR, and its classes are Java
class files. ART runs DEX files. Today `tools/media3/fetch` runs D8, from
the R8 of Google. D8 changes the class files of Media3 into DEX files, and
`-apk-dex` puts those files in the APK. D8 needs a JDK.

The dexer of this plan does the work of D8 in the compiler. It reads the
class files of a JAR or of the `classes.jar` of an AAR, and it writes one
DEX file. Then `fern -apk` takes JAR and AAR files with no other tool.

## The parts

1. **The class files.** `std/classfile` reads the code of each method: the
   bytecode, the size of the stack and of the locals, and the table of the
   exceptions. It also reads the bootstrap methods, the inner classes and
   the other attributes that a DEX file keeps.

2. **The translation.** `compiler/dexer.fern` changes the bytecode of each
   method into Dalvik bytecode. Each slot of the locals and of the stack
   of the JVM becomes a register. The parameters go in the last registers,
   as ART needs them.

   An analysis of the stack gives the type of each
   slot at each instruction: int, float, long, double or a reference.
   Thus each move and each constant takes the instruction of its type. A
   handler of an exception starts with `move-exception`. A `tableswitch`
   becomes a `packed-switch`, and a `lookupswitch` a `sparse-switch`.

3. **The DEX file.** One writer makes a DEX file from classes with fields,
   methods, code, handlers and the values of the static fields. The writer
   of `compiler/dex.fern` for the `@jni` classes then uses it, and its own
   code goes.

4. **Desugaring.** ART does not have all of the Java platform. The dexer
   changes these forms, as D8 changes them:
   - A lambda and a method reference, which call `LambdaMetafactory`
     through `invokedynamic`, become a class of their own.
   - A concatenation of strings through `invokedynamic` becomes calls of
     `StringBuilder`.
   - A call of a private method of another class of the same nest gets a
     method that has access to it.
   Each `invokedynamic` that has no such form stops the dexer, with an
   error that names the class and the method.

5. **The metadata.** The runtime annotations, the generic signatures, the
   inner classes and the enclosing methods become the annotations of
   `dalvik.annotation` in the DEX file, because reflection reads them.
   The numbers of the lines become the debug information.

6. **The AAR files.** `fern -apk` takes `-apk-jar` and `-apk-aar`. The
   manifest of an AAR merges into the manifest of the app: its
   permissions, activities, services, receivers, providers and meta-data.
   The resources of an AAR come later, in their own work.

7. **Media3.** `tools/media3/fetch` and its R8 go, and the Media3 of
   `std/android/exoplayer` takes the dexer.

## The checks

- Each part has tests in Fern or in Go, with class files that `javac`
  writes for the test.
- D8 reads each DEX file that a test writes, and makes it again. D8
  builds the code of each method with the types of its registers, so it
  finds a register with the wrong type and a broken file. The test skips
  this check only where no JDK or no R8 is on the machine.
- The emulator lane of `android.yml` runs an app with a library that the
  dexer compiled. The app calls the library and compares each result with
  the result of the JVM.

## The limits of the first parts

- A method with more than 255 registers stops the dexer at first. The
  moves to the low registers come in the translation later.
- `jsr` and `ret`, which no compiler of Java 7 or later writes, stop the
  dexer.
- The dexer writes DEX 037 for an app of API level 24 or later, the
  default of `fern -apk`, because it has the default methods of
  interfaces.
