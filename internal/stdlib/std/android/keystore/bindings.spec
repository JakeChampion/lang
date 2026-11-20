# Regenerate with jnibind android.jar bindings.spec (Android API 34).
java.security.KeyStore getInstance(Ljava/lang/String;)Ljava/security/KeyStore; load(Ljava/security/KeyStore$LoadStoreParameter;)V getKey
javax.crypto.KeyGenerator getInstance(Ljava/lang/String;Ljava/lang/String;)Ljavax/crypto/KeyGenerator; init(Ljava/security/spec/AlgorithmParameterSpec;)V generateKey
android.security.keystore.KeyGenParameterSpec$Builder <init>(Ljava/lang/String;I)V setKeySize setBlockModes setEncryptionPaddings build
javax.crypto.Cipher getInstance(Ljava/lang/String;)Ljavax/crypto/Cipher; init(ILjava/security/Key;)V init(ILjava/security/Key;Ljava/security/spec/AlgorithmParameterSpec;)V getIV doFinal([B)[B
javax.crypto.spec.GCMParameterSpec <init>(I[B)V
