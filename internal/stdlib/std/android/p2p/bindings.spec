android.content.Context getClassLoader()Ljava/lang/ClassLoader; getMainLooper()Landroid/os/Looper; getSystemService(Ljava/lang/String;)Ljava/lang/Object; registerReceiver(Landroid/content/BroadcastReceiver;Landroid/content/IntentFilter;)Landroid/content/Intent; unregisterReceiver(Landroid/content/BroadcastReceiver;)V
android.content.BroadcastReceiver -
android.content.Intent getAction()Ljava/lang/String; getIntExtra(Ljava/lang/String;I)I getParcelableExtra(Ljava/lang/String;)Landroid/os/Parcelable;
android.content.IntentFilter <init>()V addAction(Ljava/lang/String;)V
android.os.Looper -
android.os.Parcelable -
android.net.wifi.p2p.WifiP2pManager cancelConnect(Landroid/net/wifi/p2p/WifiP2pManager$Channel;Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;)V connect(Landroid/net/wifi/p2p/WifiP2pManager$Channel;Landroid/net/wifi/p2p/WifiP2pConfig;Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;)V discoverPeers(Landroid/net/wifi/p2p/WifiP2pManager$Channel;Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;)V initialize(Landroid/content/Context;Landroid/os/Looper;Landroid/net/wifi/p2p/WifiP2pManager$ChannelListener;)Landroid/net/wifi/p2p/WifiP2pManager$Channel; removeGroup(Landroid/net/wifi/p2p/WifiP2pManager$Channel;Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;)V stopPeerDiscovery(Landroid/net/wifi/p2p/WifiP2pManager$Channel;Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;)V
android.net.wifi.p2p.WifiP2pManager$Channel -
android.net.wifi.p2p.WifiP2pManager$ChannelListener -
android.net.wifi.p2p.WifiP2pManager$ActionListener -
android.net.wifi.p2p.WifiP2pConfig <init>()V deviceAddress
android.net.wifi.p2p.WifiP2pDeviceList getDeviceList()Ljava/util/Collection;
android.net.wifi.p2p.WifiP2pDevice deviceAddress deviceName status
android.net.wifi.p2p.WifiP2pInfo groupFormed groupOwnerAddress isGroupOwner
java.net.InetAddress getHostAddress()Ljava/lang/String;
java.util.Collection toArray()[Ljava/lang/Object;
java.lang.Class newInstance()Ljava/lang/Object;
java.lang.ClassLoader loadClass(Ljava/lang/String;)Ljava/lang/Class;
