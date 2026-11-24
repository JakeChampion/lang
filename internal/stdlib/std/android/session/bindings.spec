android.content.Context getClassLoader
java.lang.ClassLoader loadClass(Ljava/lang/String;)Ljava/lang/Class;
java.lang.Class newInstance()Ljava/lang/Object;
android.os.Looper getMainLooper
android.os.Handler <init>(Landroid/os/Looper;)V
android.media.session.MediaSession <init>(Landroid/content/Context;Ljava/lang/String;)V setCallback(Landroid/media/session/MediaSession$Callback;Landroid/os/Handler;)V setActive setMetadata setPlaybackState getSessionToken release
android.media.session.MediaSession$Callback -
android.media.session.PlaybackState$Builder <init>()V setActions setState(IJF)Landroid/media/session/PlaybackState$Builder; addCustomAction(Ljava/lang/String;Ljava/lang/String;I)Landroid/media/session/PlaybackState$Builder; build
android.media.MediaMetadata$Builder <init>()V putString putLong putBitmap build
android.graphics.Bitmap createBitmap([IIILandroid/graphics/Bitmap$Config;)Landroid/graphics/Bitmap;
android.graphics.Bitmap$Config ARGB_8888
android.media.MediaMetadata METADATA_KEY_TITLE METADATA_KEY_ARTIST METADATA_KEY_ALBUM METADATA_KEY_DURATION METADATA_KEY_ALBUM_ART
android.media.session.PlaybackState STATE_PAUSED STATE_PLAYING ACTION_STOP ACTION_PAUSE ACTION_PLAY ACTION_SKIP_TO_PREVIOUS ACTION_SKIP_TO_NEXT ACTION_SEEK_TO ACTION_PLAY_PAUSE ACTION_PLAY_FROM_MEDIA_ID
