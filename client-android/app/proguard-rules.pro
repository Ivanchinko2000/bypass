# Proguard-правила для Bypass VPN

# Сохраняем классы gomobile (WireGuard Go-бэкенд)
-keep class com.wireguard.** { *; }
-keep class go.** { *; }

# Сохраняем модели (сериализация через JSON)
-keep class com.bypass.vpn.model.** { *; }

# OkHttp
-dontwarn okhttp3.**
-dontwarn okio.**

# AndroidX
-keep class androidx.** { *; }

# Корутины
-keepclassmembers class kotlinx.coroutines.internal.MainDispatcherFactory { *; }
-keepnames class kotlinx.coroutines.CoroutineExceptionHandler {}