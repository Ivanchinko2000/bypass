package com.bypass.vpn

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import com.bypass.vpn.service.BypassVpnService

/**
 * Класс приложения Bypass VPN.
 *
 * Инициализирует глобальные зависимости при запуске:
 * - Создаёт канал уведомлений для VPN-сервиса
 * - Инициализирует хранилища данных
 *
 * Синглтон-доступ через applicationContext cast.
 */
class BypassApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        instance = this
        createNotificationChannels()
    }

    /**
     * Создать каналы уведомлений (требуется для Android 8.0+).
     *
     * VPN-сервис работает как foreground service и нуждается
     * в канале уведомлений для отображения в шторке.
     */
    private fun createNotificationChannels() {
        // Канал для VPN-сервиса
        val vpnChannel = NotificationChannel(
            BypassVpnService.NOTIFICATION_CHANNEL_ID,
            "Bypass VPN Туннель",
            NotificationManager.IMPORTANCE_DEFAULT
        ).apply {
            description = "Уведомление о работе VPN-туннеля"
            setShowBadge(false)
            setSound(null, null)
            enableVibration(false)
        }

        val notificationManager = getSystemService(NotificationManager::class.java)
        notificationManager.createNotificationChannel(vpnChannel)
    }

    companion object {
        /** Глобальный экземпляр приложения */
        @Volatile
        private var instance: BypassApplication? = null

        /** Получить экземпляр приложения */
        fun getInstance(): BypassApplication = instance ?: error("Application не инициализирован")
    }
}