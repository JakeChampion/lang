android.content.Context getClassLoader startForegroundService startService stopService getSystemService(Ljava/lang/String;)Ljava/lang/Object; getApplicationContext getPackageManager getPackageName NOTIFICATION_SERVICE POWER_SERVICE WIFI_SERVICE
java.lang.ClassLoader loadClass(Ljava/lang/String;)Ljava/lang/Class;
java.lang.Class -
android.app.Service stopSelf()V startForeground(ILandroid/app/Notification;)V startForeground(ILandroid/app/Notification;I)V? START_NOT_STICKY
android.content.pm.ServiceInfo FOREGROUND_SERVICE_TYPE_MANIFEST
android.content.Intent <init>(Landroid/content/Context;Ljava/lang/Class;)V setAction putExtra(Ljava/lang/String;Landroid/os/Parcelable;)Landroid/content/Intent; getParcelableExtra(Ljava/lang/String;)Landroid/os/Parcelable;
android.content.pm.PackageManager getLaunchIntentForPackage
android.app.PendingIntent getService getActivity(Landroid/content/Context;ILandroid/content/Intent;I)Landroid/app/PendingIntent; FLAG_IMMUTABLE FLAG_UPDATE_CURRENT
android.app.NotificationManager createNotificationChannel notify(ILandroid/app/Notification;)V IMPORTANCE_LOW
android.app.NotificationChannel <init>(Ljava/lang/String;Ljava/lang/CharSequence;I)V
android.app.Notification -
android.app.Notification$Builder <init>(Landroid/content/Context;Ljava/lang/String;)V setContentTitle setContentText setSmallIcon(I)Landroid/app/Notification$Builder; setOngoing addAction(Landroid/app/Notification$Action;)Landroid/app/Notification$Builder; addAction(ILjava/lang/CharSequence;Landroid/app/PendingIntent;)Landroid/app/Notification$Builder; setStyle setContentIntent build
android.app.Notification$Action$Builder <init>(ILjava/lang/CharSequence;Landroid/app/PendingIntent;)V setAuthenticationRequired? build
android.app.Notification$MediaStyle <init>()V setMediaSession setShowActionsInCompactView
android.os.PowerManager newWakeLock PARTIAL_WAKE_LOCK
android.os.PowerManager$WakeLock acquire()V release()V
android.net.wifi.WifiManager createWifiLock(ILjava/lang/String;)Landroid/net/wifi/WifiManager$WifiLock; WIFI_MODE_FULL_HIGH_PERF
android.net.wifi.WifiManager$WifiLock acquire release
android.R$drawable ic_btn_speak_now ic_menu_close_clear_cancel ic_media_play
