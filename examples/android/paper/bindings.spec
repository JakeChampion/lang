# The Java classes that app.fern calls through typed wrappers. Write
# bindings.fern again with
#   jnibind $ANDROID_HOME/platforms/android-34/android.jar bindings.spec
android.app.Activity getWindow
android.app.ActivityOptions makeClipRevealAnimation toBundle
android.content.ComponentName unflattenFromString
android.content.Context getPackageManager startActivity(Landroid/content/Intent;Landroid/os/Bundle;)V
android.content.Intent <init>(Ljava/lang/String;)V <init>(Ljava/lang/String;Landroid/net/Uri;)V addCategory addFlags setComponent putExtra(Ljava/lang/String;Ljava/lang/String;)Landroid/content/Intent; makeMainSelectorActivity ACTION_MAIN ACTION_VIEW ACTION_WEB_SEARCH ACTION_DELETE CATEGORY_LAUNCHER CATEGORY_APP_MUSIC FLAG_ACTIVITY_NEW_TASK FLAG_ACTIVITY_RESET_TASK_IF_NEEDED
android.content.pm.ActivityInfo -
android.content.pm.PackageItemInfo name packageName
android.content.pm.PackageManager queryIntentActivities(Landroid/content/Intent;I)Ljava/util/List; resolveActivity(Landroid/content/Intent;I)Landroid/content/pm/ResolveInfo; getLaunchIntentForPackage MATCH_DEFAULT_ONLY
android.content.pm.ResolveInfo activityInfo loadLabel
android.net.Uri parse
android.os.Bundle -
android.view.View -
android.view.Window getDecorView
java.util.List size get
