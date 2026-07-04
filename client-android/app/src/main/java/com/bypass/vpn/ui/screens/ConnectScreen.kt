package com.bypass.vpn.ui.screens

import android.content.Intent
import android.net.VpnService
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.*
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.bypass.vpn.data.ProfilesStore
import com.bypass.vpn.data.SettingsStore
import com.bypass.vpn.model.ConnectionMode
import com.bypass.vpn.model.ConnectionState
import com.bypass.vpn.model.ServerProfile
import com.bypass.vpn.service.TunnelManager
import com.bypass.vpn.ui.theme.BypassColors
import com.bypass.vpn.util.PingHelper
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Главный экран подключения.
 *
 * Содержит:
 * - Большую кнопку питания (включение/выключение VPN)
 * - Текущий статус подключения
 * - Скорость загрузки/отдачи
 * - Выбор режима подключения (AUTO/WDTT/VLESS)
 * - Информация о текущем сервере
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConnectScreen(
    onNavigateToProfiles: () -> Unit = {}
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val scrollState = rememberScrollState()

    // Состояния
    val connectionState by TunnelManager.connectionState.collectAsStateWithLifecycle()
    val downloadSpeed by TunnelManager.downloadSpeed.collectAsStateWithLifecycle()
    val uploadSpeed by TunnelManager.uploadSpeed.collectAsStateWithLifecycle()
    val totalDownloaded by TunnelManager.totalDownloaded.collectAsStateWithLifecycle()
    val totalUploaded by TunnelManager.totalUploaded.collectAsStateWithLifecycle()
    val activeProfile by TunnelManager.activeProfile.collectAsStateWithLifecycle()

    // Хранилища
    val profilesStore = remember { ProfilesStore(context) }
    val settingsStore = remember { SettingsStore(context) }
    val profiles by profilesStore.profiles.collectAsStateWithLifecycle()
    val connectionMode by settingsStore.connectionMode.collectAsStateWithLifecycle()

    // VPN permission request
    var vpnPermissionIntent by remember { mutableStateOf<Intent?>(null) }

    // Анимация пульса для кнопки
    val infiniteTransition = rememberInfiniteTransition(label = "pulse")
    val pulseScale by infiniteTransition.animateFloat(
        initialValue = 1f,
        targetValue = if (connectionState == ConnectionState.CONNECTED) 1.05f else 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(1500, easing = EaseInOutCubic),
            repeatMode = RepeatMode.Reverse
        ),
        label = "pulseScale"
    )

    // Цвет кнопки зависит от состояния
    val buttonColor by animateColorAsState(
        targetValue = when (connectionState) {
            ConnectionState.DISCONNECTED -> BypassColors.Disconnected
            ConnectionState.CONNECTING -> BypassColors.Connecting
            ConnectionState.CONNECTED -> BypassColors.Connected
            ConnectionState.RECONNECTING -> BypassColors.Reconnecting
            ConnectionState.ERROR -> BypassColors.Error
        },
        animationSpec = tween(500),
        label = "buttonColor"
    )

    // Обработчик нажатия кнопки подключения
    val onConnectToggle: () -> Unit = {
        scope.launch {
            when (connectionState) {
                ConnectionState.DISCONNECTED, ConnectionState.ERROR -> {
                    // Нужно подключиться
                    val activeId = profilesStore.activeProfileId.first()
                    val profile = if (activeId.isNotBlank()) {
                        profilesStore.getProfileById(activeId)
                    } else {
                        profiles.firstOrNull()
                    }

                    if (profile == null) {
                        // Нет профилей — переходим на экран профилей
                        onNavigateToProfiles()
                        return@launch
                    }

                    // Проверяем VPN-разрешение
                    val vpnIntent = VpnService.prepare(context)
                    if (vpnIntent != null) {
                        vpnPermissionIntent = vpnIntent
                    } else {
                        TunnelManager.connect(context, profile)
                    }
                }
                ConnectionState.CONNECTING, ConnectionState.CONNECTED, ConnectionState.RECONNECTING -> {
                    // Отключаем
                    TunnelManager.disconnect(context)
                }
            }
        }
    }

    // Обработка результата запроса VPN-разрешения
    LaunchedEffect(vpnPermissionIntent) {
        vpnPermissionIntent?.let { intent ->
            vpnPermissionIntent = null
            // Активность должна обрабатывать startActivityForResult
            // В реальном приложении здесь используется ActivityResultLauncher
        }
    }

    // Обновление статуса каждые 2 секунды
    LaunchedEffect(connectionState) {
        while (true) {
            delay(2000)
            // Можно обновлять дополнительную информацию
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(BypassColors.DarkBackground)
            .verticalScroll(scrollState)
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Spacer(modifier = Modifier.height(40.dp))

        // ═══ Статус подключения ═══
        Text(
            text = connectionState.displayName,
            style = MaterialTheme.typography.titleMedium,
            color = buttonColor,
            fontWeight = FontWeight.SemiBold
        )

        Spacer(modifier = Modifier.height(8.dp))

        // Имя текущего сервера
        Text(
            text = activeProfile?.name ?: if (profiles.isEmpty()) "Нет серверов" else "Выберите сервер",
            style = MaterialTheme.typography.bodyMedium,
            color = BypassColors.TextSecondary
        )

        Spacer(modifier = Modifier.height(48.dp))

        // ═══ Большая кнопка питания ═══
        Box(
            modifier = Modifier
                .size(160.dp)
                .clip(CircleShape)
                .background(buttonColor.copy(alpha = 0.15f))
                .border(
                    width = 3.dp,
                    color = buttonColor,
                    shape = CircleShape
                ),
            contentAlignment = Alignment.Center
        ) {
            IconButton(
                onClick = onConnectToggle,
                modifier = Modifier
                    .size(140.dp)
                    .clip(CircleShape)
                    .background(buttonColor.copy(alpha = 0.2f))
                    .then(
                        Modifier.background(
                            brush = Brush.radialGradient(
                                colors = listOf(
                                    buttonColor.copy(alpha = 0.3f),
                                    buttonColor.copy(alpha = 0.05f)
                                )
                            )
                        )
                    ),
            ) {
                Icon(
                    imageVector = if (connectionState == ConnectionState.CONNECTED) {
                        Icons.Default.PowerSettingsNew
                    } else {
                        Icons.Default.PowerSettingsNew
                    },
                    contentDescription = if (connectionState == ConnectionState.CONNECTED) {
                        "Отключить"
                    } else {
                        "Подключить"
                    },
                    modifier = Modifier.size(72.dp),
                    tint = buttonColor
                )
            }
        }

        Spacer(modifier = Modifier.height(48.dp))

        // ═══ Скорость и трафик ═══
        if (connectionState == ConnectionState.CONNECTED) {
            // Скорость
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceEvenly
            ) {
                SpeedColumn(
                    icon = Icons.Default.ArrowDownward,
                    label = "ЗАГРУЗКА",
                    value = TunnelManager.formatSpeed(downloadSpeed),
                    color = BypassColors.Connected
                )
                SpeedColumn(
                    icon = Icons.Default.ArrowUpward,
                    label = "ОТДАЧА",
                    value = TunnelManager.formatSpeed(uploadSpeed),
                    color = BypassColors.TerminalBlue
                )
            }

            Spacer(modifier = Modifier.height(24.dp))

            // Общий трафик
            Text(
                text = "Всего: ↓ ${TunnelManager.formatTraffic(totalDownloaded)}  " +
                        "↑ ${TunnelManager.formatTraffic(totalUploaded)}",
                style = MaterialTheme.typography.bodySmall,
                color = BypassColors.TextSecondary,
                fontFamily = FontFamily.Monospace
            )
        }

        Spacer(modifier = Modifier.height(32.dp))

        // ═══ Режим подключения ═══
        Card(
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(
                containerColor = BypassColors.DarkSurface
            ),
            shape = RoundedCornerShape(16.dp)
        ) {
            Column(modifier = Modifier.padding(16.dp)) {
                Text(
                    text = "Режим подключения",
                    style = MaterialTheme.typography.labelMedium,
                    color = BypassColors.TextSecondary
                )

                Spacer(modifier = Modifier.height(12.dp))

                // Переключатель режима
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    ConnectionMode.entries.forEach { mode ->
                        val isSelected = connectionMode == mode.name
                        FilterChip(
                            selected = isSelected,
                            onClick = {
                                scope.launch { settingsStore.setConnectionMode(mode.name) }
                            },
                            label = {
                                Text(
                                    mode.displayName,
                                    color = if (isSelected) {
                                        BypassColors.GreenPrimary
                                    } else {
                                        BypassColors.TextSecondary
                                    }
                                )
                            },
                            colors = FilterChipDefaults.filterChipColors(
                                selectedContainerColor = BypassColors.GreenDark.copy(alpha = 0.3f),
                                containerColor = BypassColors.DarkSurfaceVariant
                            ),
                            border = FilterChipDefaults.filterChipBorder(
                                borderColor = if (isSelected) BypassColors.GreenPrimary else BypassColors.DarkBorder,
                                selectedBorderColor = BypassColors.GreenPrimary,
                                enabled = true,
                                selected = isSelected
                            )
                        )
                    }
                }
            }
        }

        Spacer(modifier = Modifier.height(16.dp))

        // ═══ Информация о сети ═══
        Card(
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(
                containerColor = BypassColors.DarkSurface
            ),
            shape = RoundedCornerShape(16.dp)
        ) {
            Column(modifier = Modifier.padding(16.dp)) {
                Text(
                    text = "Сеть",
                    style = MaterialTheme.typography.labelMedium,
                    color = BypassColors.TextSecondary
                )

                Spacer(modifier = Modifier.height(8.dp))

                val networkType = remember { PingHelper.getNetworkType(context) }
                InfoRow("Тип сети", networkType)

                if (activeProfile != null) {
                    InfoRow("Сервер", activeProfile!!.address)
                    InfoRow("Протокол", activeProfile!!.mode.displayName)
                }
            }
        }

        Spacer(modifier = Modifier.height(32.dp))
    }
}

/**
 * Колонка скорости (загрузка или отдача).
 */
@Composable
private fun SpeedColumn(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    label: String,
    value: String,
    color: Color
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier.width(130.dp)
    ) {
        Icon(
            imageVector = icon,
            contentDescription = label,
            tint = color,
            modifier = Modifier.size(20.dp)
        )
        Spacer(modifier = Modifier.height(4.dp))
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = BypassColors.TextSecondary,
            letterSpacing = 1.sp
        )
        Spacer(modifier = Modifier.height(2.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.titleMedium,
            color = color,
            fontWeight = FontWeight.Bold,
            fontFamily = FontFamily.Monospace
        )
    }
}

/**
 * Строка информации.
 */
@Composable
private fun InfoRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(text = label, color = BypassColors.TextSecondary, fontSize = 14.sp)
        Text(
            text = value,
            color = BypassColors.TextPrimary,
            fontSize = 14.sp,
            fontWeight = FontWeight.Medium
        )
    }
}