# Regenerate with jnibind android.jar bindings.spec (Android API 34).
java.security.KeyStore getInstance(Ljava/lang/String;)Ljava/security/KeyStore; load(Ljava/security/KeyStore$LoadStoreParameter;)V getKey getCertificateChain deleteEntry
javax.crypto.KeyGenerator getInstance(Ljava/lang/String;Ljava/lang/String;)Ljavax/crypto/KeyGenerator; init(Ljava/security/spec/AlgorithmParameterSpec;)V generateKey
java.security.KeyPairGenerator getInstance(Ljava/lang/String;Ljava/lang/String;)Ljava/security/KeyPairGenerator; initialize(Ljava/security/spec/AlgorithmParameterSpec;)V generateKeyPair
android.security.keystore.KeyGenParameterSpec$Builder <init>(Ljava/lang/String;I)V setKeySize setBlockModes setEncryptionPaddings setDigests setAttestationChallenge? setUnlockedDeviceRequired? setUserAuthenticationRequired setIsStrongBoxBacked? build
javax.crypto.Cipher getInstance(Ljava/lang/String;)Ljavax/crypto/Cipher; init(ILjava/security/Key;)V init(ILjava/security/Key;Ljava/security/spec/AlgorithmParameterSpec;)V getIV updateAAD([B)V doFinal([B)[B
java.security.Signature getInstance(Ljava/lang/String;)Ljava/security/Signature; initSign(Ljava/security/PrivateKey;)V update([B)V sign()[B
java.security.cert.Certificate getEncoded
javax.crypto.spec.GCMParameterSpec <init>(I[B)V
java.util.Arrays fill([BB)V
