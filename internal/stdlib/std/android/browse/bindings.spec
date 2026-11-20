# The classes of std/android/browse. Write bindings.fern again with
#   jnibind $ANDROID_HOME/platforms/android-34/android.jar bindings.spec
android.service.media.MediaBrowserService setSessionToken
android.service.media.MediaBrowserService$BrowserRoot <init>(Ljava/lang/String;Landroid/os/Bundle;)V
android.service.media.MediaBrowserService$Result detach sendResult
android.media.session.MediaSession$Token -
android.media.browse.MediaBrowser$MediaItem <init>(Landroid/media/MediaDescription;I)V
android.media.MediaDescription -
android.media.MediaDescription$Builder <init>()V setMediaId setTitle setSubtitle build
java.util.ArrayList <init>()V add(Ljava/lang/Object;)Z
