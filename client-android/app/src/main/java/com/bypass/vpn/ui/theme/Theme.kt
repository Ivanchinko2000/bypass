package com.bypass.vpn.ui.theme

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

/**
 * Цветовая палитра Bypass VPN.
 *
 * Тёмная тема с зелёными акцентами — отсылка к
 * статусу «подключено» и безопасности.
 */
@Immutable
object BypassColors {
    // Основные цвета
    val GreenPrimary = Color(0xFF4CAF50)       // Зелёный — основной акцент
    val GreenDark = Color(0xFF2E7D32)          // Зелёный тёмный
    val GreenLight = Color(0xFF81C784)         // Зелёный светлый

    // Статусы подключения
    val Connected = Color(0xFF4CAF50)          // Подключено — зелёный
    val Connecting = Color(0xFFFFB74D)         // Подключение — оранжевый
    val Error = Color(0xFFE57373)             // Ошибка — красный
    val Disconnected = Color(0xFF9E9E9E)      // Отключено — серый
    val Reconnecting = Color(0xFF90CAF9)      // Переподключение — голубой

    // Тёмная тема
    val DarkBackground = Color(0xFF0D1117)    // Фон (GitHub-dark стиль)
    val DarkSurface = Color(0xFF161B22)       // Поверхность карточек
    val DarkSurfaceVariant = Color(0xFF21262D) // Вариант поверхности
    val DarkBorder = Color(0xFF30363D)        // Границы

    // Текст
    val TextPrimary = Color(0xFFE6EDF3)
    val TextSecondary = Color(0xFF8B949E)
    val TextMuted = Color(0xFF484F58)

    // Терминал (для логов)
    val TerminalBackground = Color(0xFF0D1117)
    val TerminalText = Color(0xFFC9D1D9)
    val TerminalGreen = Color(0xFF7EE787)
    val TerminalRed = Color(0xFFFF7B72)
    val TerminalBlue = Color(0xFF79C0FF)
    val TerminalYellow = Color(0xFFFFA657)
}

/**
 * Светлая цветовая схема Material3.
 */
private val LightColorScheme = lightColorScheme(
    primary = BypassColors.GreenPrimary,
    onPrimary = Color.White,
    primaryContainer = BypassColors.GreenLight,
    onPrimaryContainer = BypassColors.GreenDark,
    secondary = BypassColors.GreenDark,
    background = Color(0xFFF5F5F5),
    surface = Color.White,
    onBackground = Color(0xFF1C1C1C),
    onSurface = Color(0xFF1C1C1C),
    error = BypassColors.Error
)

/**
 * Тёмная цветовая схема Material3.
 *
 * Основная схема приложения — все цвета подобраны
 * для комфортной работы в тёмной среде.
 */
private val DarkColorScheme = darkColorScheme(
    primary = BypassColors.GreenPrimary,
    onPrimary = Color.Black,
    primaryContainer = BypassColors.GreenDark,
    onPrimaryContainer = BypassColors.GreenLight,
    secondary = BypassColors.GreenLight,
    background = BypassColors.DarkBackground,
    surface = BypassColors.DarkSurface,
    surfaceVariant = BypassColors.DarkSurfaceVariant,
    onBackground = BypassColors.TextPrimary,
    onSurface = BypassColors.TextPrimary,
    onSurfaceVariant = BypassColors.TextSecondary,
    outline = BypassColors.DarkBorder,
    error = BypassColors.Error,
    errorContainer = Color(0xFF3D1518),
    onErrorContainer = BypassColors.Error
)

/**
 * Провайдер темы Bypass VPN.
 *
 * По умолчанию использует тёмную тему.
 * Можно переопределить через настройки пользователя.
 */
@Composable
fun BypassTheme(
    darkTheme: Boolean = true, // По умолчанию тёмная
    content: @Composable () -> Unit
) {
    val colorScheme = if (darkTheme) DarkColorScheme else LightColorScheme

    MaterialTheme(
        colorScheme = colorScheme,
        typography = Typography(),
        content = content
    )
}