# The Java classes that app.fern calls through typed wrappers. Write
# bindings.fern again with
#   jnibind $ANDROID_HOME/platforms/android-34/android.jar bindings.spec
android.app.Activity getWindow
android.app.ActivityOptions makeClipRevealAnimation toBundle
android.content.ComponentName unflattenFromString
android.content.ContentResolver -
android.content.Context getPackageManager startActivity(Landroid/content/Intent;Landroid/os/Bundle;)V getContentResolver()Landroid/content/ContentResolver; getFilesDir()Ljava/io/File; getSystemService(Ljava/lang/String;)Ljava/lang/Object;
android.content.Intent <init>(Ljava/lang/String;)V <init>(Ljava/lang/String;Landroid/net/Uri;)V addCategory addFlags setComponent putExtra(Ljava/lang/String;Ljava/lang/String;)Landroid/content/Intent; makeMainSelectorActivity ACTION_MAIN ACTION_VIEW ACTION_WEB_SEARCH ACTION_DELETE CATEGORY_LAUNCHER CATEGORY_APP_MUSIC FLAG_ACTIVITY_NEW_TASK FLAG_ACTIVITY_RESET_TASK_IF_NEEDED
android.content.pm.ActivityInfo -
android.content.pm.ApplicationInfo -
android.content.pm.PackageItemInfo name packageName
android.content.pm.PackageManager queryIntentActivities(Landroid/content/Intent;I)Ljava/util/List; resolveActivity(Landroid/content/Intent;I)Landroid/content/pm/ResolveInfo; getLaunchIntentForPackage MATCH_DEFAULT_ONLY getApplicationInfo(Ljava/lang/String;I)Landroid/content/pm/ApplicationInfo; getApplicationLabel(Landroid/content/pm/ApplicationInfo;)Ljava/lang/CharSequence;
android.content.pm.ResolveInfo activityInfo loadLabel
android.location.Location getTime()J getLatitude()D getLongitude()D
android.location.LocationManager getProviders(Z)Ljava/util/List; getLastKnownLocation(Ljava/lang/String;)Landroid/location/Location;
android.net.Uri parse
android.os.Bundle -
android.provider.Settings$Secure getString(Landroid/content/ContentResolver;Ljava/lang/String;)Ljava/lang/String;
android.provider.Telephony$Sms getDefaultSmsPackage(Landroid/content/Context;)Ljava/lang/String;
android.telecom.TelecomManager getDefaultDialerPackage()Ljava/lang/String;
android.text.format.DateFormat is24HourFormat(Landroid/content/Context;)Z
android.view.View -
android.view.Window getDecorView
java.io.File getAbsolutePath()Ljava/lang/String;
java.util.List size get
java.util.Locale getDefault()Ljava/util/Locale; getCountry()Ljava/lang/String;
