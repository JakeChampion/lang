# The classes of std/android/exoplayer. Write bindings.fern again with
#   tools/media3/fetch
#   jnibind $ANDROID_HOME/platforms/android-34/android.jar build/media3/jars/*.jar bindings.spec
android.view.Surface release setFrameRate(FII)V?
android.graphics.SurfaceTexture release
android.content.Context -
java.util.List size get(I)Ljava/lang/Object;
java.util.concurrent.atomic.AtomicLongArray <init>(I)V get(I)J set(IJ)V
java.util.concurrent.atomic.AtomicReference get()Ljava/lang/Object; set(Ljava/lang/Object;)V
androidx.media3.common.AudioAttributes -
androidx.media3.common.AudioAttributes$Builder <init>()V setUsage setContentType build
androidx.media3.exoplayer.ExoPlayer -
androidx.media3.exoplayer.ExoPlayer$Builder <init>(Landroid/content/Context;)V setAudioAttributes build
androidx.media3.common.Player addListener setMediaItem(Landroidx/media3/common/MediaItem;J)V addMediaItem(Landroidx/media3/common/MediaItem;)V removeMediaItems(II)V getMediaItemCount getCurrentMediaItemIndex prepare play pause seekTo(J)V setPlaybackSpeed stop clearMediaItems release setVideoSurface clearVideoSurface()V getCurrentPosition getDuration getPlaybackState isPlaying getPlayWhenReady getCurrentTracks getTrackSelectionParameters setTrackSelectionParameters getVideoSize getPlayerError
androidx.media3.common.Player$Listener -
androidx.media3.common.Player$Events contains
androidx.media3.common.MediaItem fromUri(Ljava/lang/String;)Landroidx/media3/common/MediaItem;
androidx.media3.common.Tracks getGroups
androidx.media3.common.Tracks$Group getType isSelected isSupported()Z getMediaTrackGroup
androidx.media3.common.TrackGroup -
androidx.media3.common.TrackSelectionParameters buildUpon
androidx.media3.common.TrackSelectionParameters$Builder setOverrideForType setTrackTypeDisabled build
androidx.media3.common.TrackSelectionOverride <init>(Landroidx/media3/common/TrackGroup;I)V
androidx.media3.common.VideoSize width height
androidx.media3.common.PlaybackException errorCode
androidx.media3.exoplayer.audio.AudioCapabilities getCapabilities(Landroid/content/Context;)Landroidx/media3/exoplayer/audio/AudioCapabilities; supportsEncoding
androidx.media3.common.text.CueGroup cues
androidx.media3.common.text.Cue bitmap position line size bitmapHeight
android.graphics.Bitmap getWidth getHeight getPixels
