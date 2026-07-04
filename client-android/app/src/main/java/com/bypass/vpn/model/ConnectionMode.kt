package com.bypass.vpn.model

/**
 * Режим подключения VPN.
 *
 * AUTO  — автоматический выбор протокола (пробует WDTT, затем VLESS)
 * WDTT  — подключение через WDTT-туннель (обход DPI через VK TURN)
 * VLESS — подключение через VLESS/Xray-core
 */
enum class ConnectionMode(val displayName: String) {
    AUTO("Авто"),
    WDTT("WDTT"),
    VLESS("VLESS");

    companion object {
        /** Получить режим по строковому имени, по умолчанию AUTO */
        fun fromString(name: String?): ConnectionMode =
            entries.firstOrNull { it.name.equals(name, ignoreCase = true) } ?: AUTO
    }
}