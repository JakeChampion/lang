android.content.Context getNoBackupFilesDir getCacheDir getFilesDir checkSelfPermission getContentResolver getSystemService(Ljava/lang/String;)Ljava/lang/Object; getResources getApplicationContext AUDIO_SERVICE INPUT_METHOD_SERVICE
android.app.Activity getIntent requestPermissions setVolumeControlStream moveTaskToBack getWindow getWindowManager setRequestedOrientation isInPictureInPictureMode()Z? setPictureInPictureParams? getOnBackInvokedDispatcher? addContentView
java.io.File getAbsolutePath
android.content.Intent getAction getStringExtra
android.content.pm.PackageManager PERMISSION_GRANTED
android.view.KeyCharacterMap load get(II)I
android.provider.Settings$System getInt(Landroid/content/ContentResolver;Ljava/lang/String;I)I SCREEN_BRIGHTNESS
android.media.AudioManager getStreamVolume getStreamMaxVolume setStreamVolume STREAM_MUSIC
java.util.TimeZone getDefault getOffset(J)I
android.content.res.Resources getConfiguration
android.content.res.Configuration getLocales fontScale
android.os.LocaleList toLanguageTags
android.view.Window getDecorView setFlags getAttributes setAttributes
android.view.View setSystemUiVisibility requestUnbufferedDispatch(I)V? getWidth getHeight requestApplyInsets setOnApplyWindowInsetsListener onApplyWindowInsets requestFocus()Z clearFocus getWindowToken
android.view.View$OnApplyWindowInsetsListener -
android.view.WindowInsets getInsets(I)Landroid/graphics/Insets;? isVisible(I)Z?
android.view.WindowInsets$Type systemBars ime displayCutout
android.graphics.Insets left top right bottom
android.window.OnBackInvokedDispatcher registerOnBackInvokedCallback
android.window.OnBackInvokedCallback -
android.view.WindowManager getDefaultDisplay
android.view.WindowManager$LayoutParams preferredDisplayModeId screenBrightness FLAG_FULLSCREEN BRIGHTNESS_OVERRIDE_NONE
android.view.Display getMode getSupportedModes
android.view.Display$Mode getPhysicalWidth getPhysicalHeight getRefreshRate getModeId
android.app.PictureInPictureParams$Builder <init>()V setAspectRatio setAutoEnterEnabled? build
android.util.Rational <init>(II)V
android.view.InputDevice SOURCE_CLASS_POINTER
java.lang.System getProperty(Ljava/lang/String;)Ljava/lang/String; setProperty clearProperty
android.os.Build$VERSION SDK_INT
android.widget.EditText <init>(Landroid/content/Context;)V getText setSelection(I)V
android.widget.TextView setSingleLine(Z)V setInputType setImeOptions setText(Ljava/lang/CharSequence;)V length addTextChangedListener setOnEditorActionListener
android.text.Editable replace(IILjava/lang/CharSequence;)Landroid/text/Editable;
android.text.TextWatcher -
android.widget.TextView$OnEditorActionListener -
android.view.ViewGroup$LayoutParams <init>(II)V
android.view.inputmethod.InputMethodManager showSoftInput(Landroid/view/View;I)Z hideSoftInputFromWindow(Landroid/os/IBinder;I)Z
android.os.IBinder -
java.lang.CharSequence -
