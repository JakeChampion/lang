android.app.Notification -
android.app.Notification$Builder <init>(Landroid/content/Context;)V <init>(Landroid/content/Context;Ljava/lang/String;)V? setSmallIcon(I)Landroid/app/Notification$Builder; setContentTitle setContentText setOngoing setAutoCancel setProgress setContentIntent build
android.app.NotificationChannel <init>(Ljava/lang/String;Ljava/lang/CharSequence;I)V
android.app.NotificationManager areNotificationsEnabled()Z? cancel(I)V createNotificationChannel(Landroid/app/NotificationChannel;)V? notify(ILandroid/app/Notification;)V
android.app.PendingIntent getActivity(Landroid/content/Context;ILandroid/content/Intent;I)Landroid/app/PendingIntent;
android.content.Context getPackageManager getPackageName getSystemService(Ljava/lang/String;)Ljava/lang/Object;
android.content.Intent -
android.content.pm.PackageManager getLaunchIntentForPackage
