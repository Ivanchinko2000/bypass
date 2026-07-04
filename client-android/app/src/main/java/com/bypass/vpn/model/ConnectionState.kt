package com.bypass.vpn.model

/**
 * Состояние VPN-подключения.
 *
 * DISCONNECTED  — отключен
 * CONNECTING    — идёт установка соединения
 * CONNECTED     — соединение активно, трафик идёт
 * RECONNECTING  — потеря соединения, идёт переподключение
 * ERROR         — критическая ошибка, требующая действий пользователя
 */
enum class ConnectionState(val displayName: String, val colorHint: Int) {
    DISCONNECTED("Отключено", 0xFF9E9E9E.toInt()),
    CONNECTING("Подключение...", 0xFFFFB74D.toInt()),
    CONNECTED("Подключено", 0xFF81C784.toInt()),
    RECONNECTING("Переподключение...", 0xFF90CAF9.toInt()),
    ERROR("Ошибка", 0xFFE57373.toInt());

    companion object {
        /** Получить состояние по порядковому номеру */
        fun fromOrdinal(ordinal: Int): ConnectionState =
            entries.getOrElse(ordinal) { DISCONNECTED }
    }
}