package com.bypass.vpn

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.*
import androidx.core.content.ContextCompat
import com.bypass.vpn.data.ProfilesStore
import com.bypass.vpn.data.SettingsStore
import com.bypass.vpn.service.TunnelManager
import com.bypass.vpn.ui.navigation.AppNavigation
import com.bypass.vpn.ui.theme.BypassTheme
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Главная активность Bypass VPN.
 *
 * Отвечает за:
 * - Запуск Jetpack Compose UI с навигацией
 * - Запрос разрешений (VPN, уведомления)
 * - Обработку результатов из других активностей
 * - Авто-подключение при запуске (если включено в настройках)
 */
class MainActivity : ComponentActivity() {

    // Лаунчер для VPN-разрешения
    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            // VPN-разрешение получено, подключаемся
            connectToActiveProfile()
        } else {
            TunnelManager.addLog("VPN-разрешение отклонено пользователем", isError = true)
        }
    }

    // Лаунчер для разрешения уведомлений
    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        if (!granted) {
            Toast.makeText(this, "Уведомления отключены", Toast.LENGTH_SHORT).show()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        // Запрашиваем разрешение на уведомления (Android 13+)
        requestNotificationPermission()

        // Устанавливаем Compose UI
        setContent {
            val darkTheme by SettingsStore(this).darkTheme.collectAsStateWithLifecycle()
            BypassTheme(darkTheme = darkTheme) {
                AppNavigation()
            }
        }

        // Проверяем, нужно ли автоподключаться
        checkAutoConnect()
    }

    /**
     * Запросить разрешение на показ уведомлений.
     * Требуется на Android 13 (API 33) и выше для foreground-сервисов.
     */
    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(
                    this,
                    Manifest.permission.POST_NOTIFICATIONS
                ) != PackageManager.PERMISSION_GRANTED
            ) {
                notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
    }

    /**
     * Подготовить VPN-разрешение и подключиться.
     *
     * Если разрешение ещё не выдано — показывает системный диалог.
     * Если выдано — сразу подключается.
     */
    fun requestVpnAndConnect() {
        val vpnIntent = VpnService.prepare(this)
        if (vpnIntent != null) {
            // Нужно запросить разрешение
            vpnPermissionLauncher.launch(vpnIntent)
        } else {
            // Разрешение уже есть
            connectToActiveProfile()
        }
    }

    /**
     * Подключиться к текущему активному профилю.
     */
    private fun connectToActiveProfile() {
        val scope = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.Dispatchers.Main)
        scope.launch {
            val store = ProfilesStore(this@MainActivity)
            val activeId = store.activeProfileId.first()
            val profile = if (activeId.isNotBlank()) {
                store.getProfileById(activeId)
            } else {
                store.profiles.first().firstOrNull()
            }

            if (profile != null) {
                TunnelManager.connect(this@MainActivity, profile)
            } else {
                TunnelManager.addLog("Нет профилей для подключения", isError = true)
                Toast.makeText(
                    this@MainActivity,
                    "Сначала добавьте сервер в «Профили»",
                    Toast.LENGTH_LONG
                ).show()
            }
        }
    }

    /**
     * Проверить и выполнить автоподключение.
     *
     * Если в настройках включён «Авто-подключение» и есть
     * сохранённый профиль — подключаемся автоматически.
     */
    private fun checkAutoConnect() {
        val scope = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.Dispatchers.IO)
        scope.launch {
            val settings = SettingsStore(this@MainActivity)
            val autoConnect = settings.autoConnect.first()
            if (!autoConnect) return@launch

            // Небольшая задержка, чтобы UI успел отрисоваться
            kotlinx.coroutines.delay(500)

            val store = ProfilesStore(this@MainActivity)
            val activeId = store.activeProfileId.first()
            if (activeId.isBlank()) return@launch

            val vpnIntent = VpnService.prepare(this@MainActivity)
            if (vpnIntent == null) {
                // Разрешение есть — подключаемся на Main
                kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Main) {
                    connectToActiveProfile()
                }
            }
            // Если разрешения нет — не подключаем автоматически
            // (пользователь должен подтвердить вручную)
        }
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        // Обработка intent из виджета, уведомления и т.д.
        // В реальном приложении здесь обрабатываются deep links
    }
}