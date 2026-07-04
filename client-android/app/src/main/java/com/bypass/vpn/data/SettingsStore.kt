package com.bypass.vpn.data

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Хранилище настроек приложения.
 *
 * Использует SharedPreferences для простых типов.
 * Все значения предоставляются через StateFlow для реактивного UI.
 */
class SettingsStore(context: Context) {

    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences("bypass_settings", Context.MODE_PRIVATE)

    // ═══════════════════════════════════════════════════════════════
    //  Kill Switch (аварийное отключение интернета при обрыве VPN)
    // ═══════════════════════════════════════════════════════════════

    private val _killSwitch = MutableStateFlow(
        prefs.getBoolean("kill_switch", true)
    )
    /** Включён ли Kill Switch (блокировка трафика при разрыве VPN) */
    val killSwitch: StateFlow<Boolean> = _killSwitch.asStateFlow()

    suspend fun setKillSwitch(enabled: Boolean) {
        _killSwitch.value = enabled
        prefs.edit().putBoolean("kill_switch", enabled).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  DNS-настройки
    // ═══════════════════════════════════════════════════════════════

    private val _dnsOverHttps = MutableStateFlow(
        prefs.getBoolean("dns_over_https", true)
    )
    /** Использовать DNS-over-HTTPS (DoH) */
    val dnsOverHttps: StateFlow<Boolean> = _dnsOverHttps.asStateFlow()

    private val _dnsServer = MutableStateFlow(
        prefs.getString("dns_server", "https://dns.google/dns-query") ?: "https://dns.google/dns-query"
    )
    /** Адрес DoH-сервера */
    val dnsServer: StateFlow<String> = _dnsServer.asStateFlow()

    private val _splitDns = MutableStateFlow(
        prefs.getBoolean("split_dns", false)
    )
    /** Split-DNS:resolver для российских доменов напрямую */
    val splitDns: StateFlow<Boolean> = _splitDns.asStateFlow()

    private val _localDnsServer = MutableStateFlow(
        prefs.getString("local_dns_server", "77.88.8.8") ?: "77.88.8.8"
    )
    /** Локальный DNS-сервер для российских доменов */
    val localDnsServer: StateFlow<String> = _localDnsServer.asStateFlow()

    suspend fun setDnsOverHttps(enabled: Boolean) {
        _dnsOverHttps.value = enabled
        prefs.edit().putBoolean("dns_over_https", enabled).apply()
    }

    suspend fun setDnsServer(url: String) {
        _dnsServer.value = url
        prefs.edit().putString("dns_server", url).apply()
    }

    suspend fun setSplitDns(enabled: Boolean) {
        _splitDns.value = enabled
        prefs.edit().putBoolean("split_dns", enabled).apply()
    }

    suspend fun setLocalDnsServer(ip: String) {
        _localDnsServer.value = ip
        prefs.edit().putString("local_dns_server", ip).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Авто-подключение
    // ═══════════════════════════════════════════════════════════════

    private val _autoConnect = MutableStateFlow(
        prefs.getBoolean("auto_connect", false)
    )
    /** Автоматически подключаться при запуске приложения */
    val autoConnect: StateFlow<Boolean> = _autoConnect.asStateFlow()

    suspend fun setAutoConnect(enabled: Boolean) {
        _autoConnect.value = enabled
        prefs.edit().putBoolean("auto_connect", enabled).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  MTU
    // ═══════════════════════════════════════════════════════════════

    private val _mtu = MutableStateFlow(
        prefs.getInt("mtu", 1420)
    )
    /** MTU для TUN-интерфейса */
    val mtu: StateFlow<Int> = _mtu.asStateFlow()

    suspend fun setMtu(value: Int) {
        val clamped = value.coerceIn(1280, 9000)
        _mtu.value = clamped
        prefs.edit().putInt("mtu", clamped).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Тема оформления
    // ═══════════════════════════════════════════════════════════════

    private val _darkTheme = MutableStateFlow(
        prefs.getBoolean("dark_theme", true)
    )
    /** Использовать тёмную тему */
    val darkTheme: StateFlow<Boolean> = _darkTheme.asStateFlow()

    suspend fun setDarkTheme(enabled: Boolean) {
        _darkTheme.value = enabled
        prefs.edit().putBoolean("dark_theme", enabled).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Split Tunneling (разделение трафика)
    // ═══════════════════════════════════════════════════════════════

    private val _splitTunneling = MutableStateFlow(
        prefs.getBoolean("split_tunneling", false)
    )
    /** Включено ли разделение трафика */
    val splitTunneling: StateFlow<Boolean> = _splitTunneling.asStateFlow()

    private val _splitTunnelMode = MutableStateFlow(
        prefs.getString("split_tunnel_mode", "blacklist") ?: "blacklist"
    )
    /** Режим: "blacklist" — всё через VPN, "whitelist" — только выбранные */
    val splitTunnelMode: StateFlow<String> = _splitTunnelMode.asStateFlow()

    private val _excludedApps = MutableStateFlow(
        prefs.getString("excluded_apps", "") ?: ""
    )
    /** Список исключённых пакетов через запятую */
    val excludedApps: StateFlow<String> = _excludedApps.asStateFlow()

    suspend fun setSplitTunneling(enabled: Boolean) {
        _splitTunneling.value = enabled
        prefs.edit().putBoolean("split_tunneling", enabled).apply()
    }

    suspend fun setSplitTunnelMode(mode: String) {
        _splitTunnelMode.value = mode
        prefs.edit().putString("split_tunnel_mode", mode).apply()
    }

    suspend fun setExcludedApps(packages: String) {
        _excludedApps.value = packages
        prefs.edit().putString("excluded_apps", packages).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Протокол подключения
    // ═══════════════════════════════════════════════════════════════

    private val _connectionMode = MutableStateFlow(
        prefs.getString("connection_mode", "AUTO") ?: "AUTO"
    )
    /** Глобальный режим подключения: AUTO, WDTT, VLESS */
    val connectionMode: StateFlow<String> = _connectionMode.asStateFlow()

    suspend fun setConnectionMode(mode: String) {
        _connectionMode.value = mode
        prefs.edit().putString("connection_mode", mode).apply()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Разное
    // ═══════════════════════════════════════════════════════════════

    private val _detailedLogs = MutableStateFlow(
        prefs.getBoolean("detailed_logs", false)
    )
    /** Подробные логи (для отладки) */
    val detailedLogs: StateFlow<Boolean> = _detailedLogs.asStateFlow()

    suspend fun setDetailedLogs(enabled: Boolean) {
        _detailedLogs.value = enabled
        prefs.edit().putBoolean("detailed_logs", enabled).apply()
    }

    companion object {
        /** Домены, которые при Split-DNS резолвятся локально (российские сервисы) */
        val LOCAL_DNS_DOMAINS = listOf(
            ".ru", ".su", ".рф",
            "vk.com", "vk.ru", "vkontakte.ru",
            "yandex.ru", "ya.ru",
            "mail.ru", "inbox.ru", "bk.ru", "list.ru",
            "rambler.ru", "lenta.ru",
            "sberbank.ru", "tinkoff.ru", "alfabank.ru",
            "gosuslugi.ru", "mos.ru",
            "ozon.ru", "wildberries.ru"
        )
    }
}