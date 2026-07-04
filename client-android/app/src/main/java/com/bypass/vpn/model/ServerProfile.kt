package com.bypass.vpn.model

import org.json.JSONObject

/**
 * Профиль сервера для подключения.
 *
 * Содержит всю необходимую информацию: адрес, режим подключения,
 * ключи WireGuard, настройки VLESS и метаданные.
 */
data class ServerProfile(
    /** Уникальный идентификатор профиля */
    val id: String,
    /** Отображаемое имя сервера */
    val name: String,
    /** Адрес сервера (хост или хост:порт) */
    val address: String,
    /** Режим подключения */
    val mode: ConnectionMode = ConnectionMode.AUTO,
    // ---- Поля WireGuard ----
    /** Приватный ключ WireGuard клиента */
    val wgPrivateKey: String = "",
    /** Публичный ключ сервера WireGuard */
    val wgServerPublicKey: String = "",
    /** Общий ключ (pre-shared key) WireGuard */
    val wgPreSharedKey: String = "",
    /** Порт WireGuard на сервере */
    val wgPort: Int = 51820,
    // ---- Поля VLESS ----
    /** UUID для VLESS-подключения */
    val vlessUuid: String = "",
    /** Потоковый транспорт VLESS (tcp, ws, grpc) */
    val vlessFlow: String = "tcp",
    /** SNI для VLESS-транспортного уровня */
    val vlessSni: String = "",
    /** Путь для WebSocket-транспорта */
    val vlessWsPath: String = "",
    // ---- Поля WDTT ----
    /** Хеши VK для WDTT-метода */
    val wdttVkHashes: String = "",
    /** Пароль подключения WDTT */
    val wdttPassword: String = "",
    // ---- Метаданные ----
    /** Потраченный трафик (МБ) */
    val trafficMb: Double = 0.0,
    /** Время последнего успешного подключения (timestamp) */
    val lastConnectedAt: Long = 0L,
    /** Используется ли профиль в данный момент */
    val isActive: Boolean = false
) {
    /**
     * Сериализация профиля в JSON-объект.
     */
    fun toJson(): JSONObject = JSONObject().apply {
        put("id", id)
        put("name", name)
        put("address", address)
        put("mode", mode.name)
        put("wg_private_key", wgPrivateKey)
        put("wg_server_public_key", wgServerPublicKey)
        put("wg_pre_shared_key", wgPreSharedKey)
        put("wg_port", wgPort)
        put("vless_uuid", vlessUuid)
        put("vless_flow", vlessFlow)
        put("vless_sni", vlessSni)
        put("vless_ws_path", vlessWsPath)
        put("wdtt_vk_hashes", wdttVkHashes)
        put("wdtt_password", wdttPassword)
        put("traffic_mb", trafficMb)
        put("last_connected_at", lastConnectedAt)
        put("is_active", isActive)
    }

    companion object {
        /**
         * Десериализация профиля из JSON-объекта.
         */
        fun fromJson(json: JSONObject): ServerProfile = ServerProfile(
            id = json.optString("id", ""),
            name = json.optString("name", "Безымянный"),
            address = json.optString("address", ""),
            mode = ConnectionMode.fromString(json.optString("mode")),
            wgPrivateKey = json.optString("wg_private_key", ""),
            wgServerPublicKey = json.optString("wg_server_public_key", ""),
            wgPreSharedKey = json.optString("wg_pre_shared_key", ""),
            wgPort = json.optInt("wg_port", 51820),
            vlessUuid = json.optString("vless_uuid", ""),
            vlessFlow = json.optString("vless_flow", "tcp"),
            vlessSni = json.optString("vless_sni", ""),
            vlessWsPath = json.optString("vless_ws_path", ""),
            wdttVkHashes = json.optString("wdtt_vk_hashes", ""),
            wdttPassword = json.optString("wdtt_password", ""),
            trafficMb = json.optDouble("traffic_mb", 0.0),
            lastConnectedAt = json.optLong("last_connected_at", 0L),
            isActive = json.optBoolean("is_active", false)
        )
    }
}