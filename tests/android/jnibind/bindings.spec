# The classes of tests/android/jnibind/app.fern. Write bindings.fern again
# with
#   jnibind $ANDROID_HOME/platforms/android-34/android.jar bindings.spec
android.app.Activity getLocalClassName isFinishing
android.content.Context getPackageName getPackageManager
android.content.pm.PackageManager queryIntentActivities(Landroid/content/Intent;I)Ljava/util/List;
android.content.Intent <init>(Ljava/lang/String;)V addCategory getAction addFlags getFlags ACTION_MAIN CATEGORY_LAUNCHER FLAG_ACTIVITY_NEW_TASK
android.content.ComponentName <init>(Ljava/lang/String;Ljava/lang/String;)V flattenToShortString()Ljava/lang/String;
android.graphics.Point <init>(II)V x y offset
android.os.SystemClock uptimeMillis
android.text.format.DateFormat is24HourFormat(Landroid/content/Context;)Z
java.util.List size get
java.lang.Integer parseInt(Ljava/lang/String;)I MAX_VALUE
java.lang.Math sqrt
java.lang.Float parseFloat
java.lang.System out
