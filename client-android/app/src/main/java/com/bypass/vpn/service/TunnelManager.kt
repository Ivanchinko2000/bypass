package com.bypass.vpn.service

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.util.Log
import com.bypass.vpn.data.ProfilesStore
import com.bypass.vpn.data.SettingsStore
import com.bypass.vpn.model.ConnectionMode
import com.bypass.vpn.model.ConnectionState
import com.bypass.vpn.model.ServerProfile
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import org.json.JSONObject
import java.text.DecimalFormat

/**
 * Менеджер VPN-туннеля.
 *
 * Управляет жизненным циклом VPN-сервиса, отслеживает состояние
 * подключения и реализует автоматическое переподключение.
 *
 * Выступает единым источником истины (Single Source of Truth)
 * для состояния VPN, логов и статистики.
 */
object TunnelManager {

    private const val TAG = "TunnelManager"

    // ═══════════════════════════════════════════════════════════════
    //  Состояние подключения
    // ═══════════════════════════════════════════════════════════════

    private val _connectionState = MutableStateFlow(ConnectionState.DISCONNECTED)
    /** Текущее состояние VPN-подключения */
    val connectionState: StateFlow<ConnectionState> = _connectionState.asStateFlow()

    // ═══════════════════════════════════════════════════════════════
    //  Статистика трафика
    // ═══════════════════════════════════════════════════════════════

    private val _downloadSpeed = MutableStateFlow(0.0)
    /** Скорость загрузки (КБ/с) */
    val downloadSpeed: StateFlow<Double> = _downloadSpeed.asStateFlow()

    private val _uploadSpeed = MutableStateFlow(0.0)
    /** Скорость отдачи (КБ/с) */
    val uploadSpeed: StateFlow<Double> = _uploadSpeed.asStateFlow()

    private val _totalDownloaded = MutableStateFlow(0L)
    /** Всего скачано (байт) */
    val totalDownloaded: StateFlow<Long> = _totalDownloaded.asStateFlow()

    private val _totalUploaded = MutableStateFlow(0L)
    /** Всего загружено (байт) */
    val totalUploaded: StateFlow<Long> = _totalUploaded.asStateFlow()

    // ═══════════════════════════════════════════════════════════════
    //  Логи
    // ═══════════════════════════════════════════════════════════════

    data class LogEntry(
        val id: Long,
        val message: String,
        val timestamp: Long = System.currentTimeMillis(),
        val isError: Boolean = false
    )

    private val _logs = MutableStateFlow<List<LogEntry>>(emptyList())
    /** Список лог-записей */
    val logs: StateFlow<List<LogEntry>> = _logs.asStateFlow()

    private var logIdCounter = 0L
    private val maxLogs = 500 // Максимальное количество записей в логе

    // ═══════════════════════════════════════════════════════════════
    //  Текущий профиль
    // ═══════════════════════════════════════════════════════════════

    private val _activeProfile = MutableStateFlow<ServerProfile?>(null)
    /** Текущий активный профиль */
    val activeProfile: StateFlow<ServerProfile?> = _activeProfile.asStateFlow()

    // ═══════════════════════════════════════════════════════════════
    //  Авто-переподключение
    // ═══════════════════════════════════════════════════════════════

    private var reconnectJob: Job? = null
    private var reconnectAttempts = 0
    private val maxReconnectAttempts = 5
    private val reconnectDelays = longArrayOf(2000, 5000, 10000, 20000, 30000)

    // ═══════════════════════════════════════════════════════════════
    //  Область видимости корутин
    // ═══════════════════════════════════════════════════════════════

    /** Глобальный scope для фоновых операций */
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    // ═══════════════════════════════════════════════════════════════
    //  Публичные методы
    // ═══════════════════════════════════════════════════════════════

    /**
     * Подключиться к VPN с заданным профилем.
     *
     * Проверяет VPN-разрешение, запускает сервис и передаёт профиль.
     *
     * @param context Контекст приложения
     * @param profile Профиль сервера для подключения
     */
    fun connect(context: Context, profile: ServerProfile) {
        // Проверяем, есть ли VPN-разрешение
        val vpnIntent = VpnService.prepare(context)
        if (vpnIntent != null) {
            // Разрешение ещё не выдано — нужно показать системный диалог
            // Вызывающая активность должна обрабатывать это
            updateState(ConnectionState.ERROR)
            addLog("Требуется разрешение на VPN", isError = true)
            return
        }

        _activeProfile.value = profile
        reconnectAttempts = 0

        addLog("Подключение к ${profile.name} (${profile.address})...")

        // Запускаем VPN-сервис
        val intent = Intent(context, BypassVpnService::class.java).apply {
            action = BypassVpnService.ACTION_START
            putExtra("profile_json", profile.toJson().toString())
        }

        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {
            context.startForegroundService(intent)
        } else {
            context.startService(intent)
        }
    }

    /**
     * Отключиться от VPN.
     */
    fun disconnect(context: Context) {
        reconnectJob?.cancel()
        reconnectJob = null
        reconnectAttempts = 0

        val intent = Intent(context, BypassVpnService::class.java).apply {
            action = BypassVpnService.ACTION_STOP
        }
        context.startService(intent)

        addLog("Отключено")
        _activeProfile.value = null
    }

    /**
     * Обновить состояние подключения (вызывается из BypassVpnService).
     */
    fun updateState(state: ConnectionState) {
        _connectionState.value = state
    }

    /**
     * Обновить скорость трафика (вызывается из BypassVpnService).
     */
    fun updateSpeed(downloadKb: Double, uploadKb: Double) {
        _downloadSpeed.value = downloadKb
        _uploadSpeed.value = uploadKb
    }

    /**
     * Обновить общий объём трафика (вызывается из BypassVpnService).
     */
    fun updateTotalTraffic(downloaded: Long, uploaded: Long) {
        _totalDownloaded.value = downloaded
        _totalUploaded.value = uploaded
    }

    /**
     * Добавить запись в лог.
     */
    fun addLog(message: String, isError: Boolean = false) {
        logIdCounter++
        val entry = LogEntry(
            id = logIdCounter,
            message = message,
            isError = isError
        )
        val currentLogs = _logs.value.toMutableList()
        currentLogs.add(entry)

        // Ограничиваем размер лога
        if (currentLogs.size > maxLogs) {
            _logs.value = currentLogs.takeLast(maxLogs)
        } else {
            _logs.value = currentLogs
        }

        if (isError) {
            Log.e(TAG, message)
        } else {
            Log.d(TAG, message)
        }
    }

    /**
     * Очистить все логи.
     */
    fun clearLogs() {
        _logs.value = emptyList()
        logIdCounter = 0L
    }

    /**
     * Запустить авто-переподключение.
     *
     * Используется при потере соединения. Задержка между попытками
     * увеличивается экспоненциально: 2с, 5с, 10с, 20с, 30с.
     * После maxReconnectAttempts неудачных попыток — останавливается.
     */
    fun scheduleReconnect(context: Context) {
        if (reconnectAttempts >= maxReconnectAttempts) {
            addLog("Превышено число попыток переподключения", isError = true)
            updateState(ConnectionState.ERROR)
            return
        }

        val profile = _activeProfile.value ?: return
        val delay = reconnectDelays.getOrElse(reconnectAttempts) { 30000 }

        updateState(ConnectionState.RECONNECTING)
        addLog("Переподключение через ${delay / 1000}с (попытка ${reconnectAttempts + 1}/$maxReconnectAttempts)")

        reconnectJob?.cancel()
        reconnectJob = scope.launch {
            delay(delay)
            reconnectAttempts++
            connect(context, profile)
        }
    }

    /**
     * Отменить запланированное переподключение.
     */
    fun cancelReconnect() {
        reconnectJob?.cancel()
        reconnectJob = null
        reconnectAttempts = 0
    }

    /**
     * Получить текущий профиль по ID.
     */
    suspend fun getCurrentProfile(context: Context): ServerProfile? {
        val store = ProfilesStore(context)
        val activeId = store.activeProfileId.first()
        return if (activeId.isBlank()) null else store.getProfileById(activeId)
    }

    /**
     * Проверить, включён ли авточтконнект и нужно ли подключаться автоматически.
     */
    suspend fun shouldAutoConnect(context: Context): Boolean {
        val settings = SettingsStore(context)
        return settings.autoConnect.first()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Утилиты форматирования
    // ═══════════════════════════════════════════════════════════════

    private val speedFormat = DecimalFormat("0.0")
    private val trafficFormat = DecimalFormat("#,##0.0")

    /**
     * Форматировать скорость в читаемую строку.
     */
    fun formatSpeed(kbPerSec: Double): String {
        return when {
            kbPerSec < 1 -> "${(kbPerSec * 1024).toInt()} Б/с"
            kbPerSec < 1024 -> "${speedFormat.format(kbPerSec)} КБ/с"
            else -> "${speedFormat.format(kbPerSec / 1024)} МБ/с"
        }
    }

    /**
     * Форматировать объём трафика в читаемую строку.
     */
    fun formatTraffic(bytes: Long): String {
        val mb = bytes / (1024.0 * 1024.0)
        return when {
            mb < 1 -> "${(bytes / 1024).toInt()} КБ"
            mb < 1024 -> "${trafficFormat.format(mb)} МБ"
            else -> "${trafficFormat.format(mb / 1024)} ГБ"
        }
    }

    /**
     * Получить описание текущего состояния для уведомления.
     */
    fun getNotificationText(): String {
        val state = _connectionState.value
        return when (state) {
            ConnectionState.CONNECTED -> {
                val dl = formatSpeed(_downloadSpeed.value)
                val ul = formatSpeed(_uploadSpeed.value)
                "↓ $dl  ↑ $ul"
            }
            ConnectionState.CONNECTING -> "Подключение..."
            ConnectionState.RECONNECTING -> "Переподключение..."
            ConnectionState.ERROR -> "Ошибка подключения"
            ConnectionState.DISCONNECTED -> "Отключено"
        }
    }
}