package com.bypass.vpn.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.bypass.vpn.data.SettingsStore
import com.bypass.vpn.ui.theme.BypassColors
import kotlinx.coroutines.launch

/**
 * Экран настроек приложения.
 *
 * Содержит группы настроек:
 * - Безопасность: Kill Switch
 * - DNS: DoH, Split-DNS, серверы
 * - Подключение: автоподключение, MTU
 * - Разделение трафика: Split Tunneling
 * - Протокол: режим подключения
 * - Внешний вид: тёмная тема
 * - Отладка: подробные логи
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val store = remember { SettingsStore(context) }

    // Читаем настройки
    val killSwitch by store.killSwitch.collectAsStateWithLifecycle()
    val dnsOverHttps by store.dnsOverHttps.collectAsStateWithLifecycle()
    val dnsServer by store.dnsServer.collectAsStateWithLifecycle()
    val splitDns by store.splitDns.collectAsStateWithLifecycle()
    val localDnsServer by store.localDnsServer.collectAsStateWithLifecycle()
    val autoConnect by store.autoConnect.collectAsStateWithLifecycle()
    val mtu by store.mtu.collectAsStateWithLifecycle()
    val darkTheme by store.darkTheme.collectAsStateWithLifecycle()
    val splitTunneling by store.splitTunneling.collectAsStateWithLifecycle()
    val connectionMode by store.connectionMode.collectAsStateWithLifecycle()
    val detailedLogs by store.detailedLogs.collectAsStateWithLifecycle()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(BypassColors.DarkBackground)
            .verticalScroll(rememberScrollState())
    ) {
        // ═══ Заголовок ═══
        Surface(
            color = BypassColors.DarkSurface,
            shadowElevation = 4.dp
        ) {
            Text(
                text = "Настройки",
                style = MaterialTheme.typography.titleLarge,
                color = BypassColors.TextPrimary,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 16.dp)
            )
        }

        Spacer(modifier = Modifier.height(8.dp))

        // ═══ БЕЗОПАСНОСТЬ ═══
        SettingsSectionHeader(title = "Безопасность")
        SettingsCard {
            // Kill Switch
            SettingsSwitchRow(
                title = "Kill Switch",
                description = "Блокировать интернет при разрыве VPN",
                icon = Icons.Default.Security,
                checked = killSwitch,
                onCheckedChange = { scope.launch { store.setKillSwitch(it) } }
            )
        }

        Spacer(modifier = Modifier.height(8.dp))

        // ═══ DNS ═══
        SettingsSectionHeader(title = "DNS")
        SettingsCard {
            // DNS-over-HTTPS
            SettingsSwitchRow(
                title = "DNS-over-HTTPS",
                description = "Шифровать DNS-запросы через HTTPS",
                icon = Icons.Default.Lock,
                checked = dnsOverHttps,
                onCheckedChange = { scope.launch { store.setDnsOverHttps(it) } }
            )

            HorizontalDivider(color = BypassColors.DarkBorder, modifier = Modifier.padding(horizontal = 16.dp))

            // Split DNS
            SettingsSwitchRow(
                title = "Split-DNS",
                description = "Российские домены резолвить локально",
                icon = Icons.Default.Dns,
                checked = splitDns,
                onCheckedChange = { scope.launch { store.setSplitDns(it) } }
            )

            if (splitDns) {
                HorizontalDivider(color = BypassColors.DarkBorder, modifier = Modifier.padding(horizontal = 16.dp))

                // Локальный DNS-сервер
                var editLocalDns by remember { mutableStateOf(localDnsServer) }
                var showLocalDnsDialog by remember { mutableStateOf(false) }

                SettingsClickRow(
                    title = "Локальный DNS",
                    description = localDnsServer,
                    icon = Icons.Default.SettingsEthernet,
                    onClick = { showLocalDnsDialog = true }
                )

                if (showLocalDnsDialog) {
                    EditStringDialog(
                        title = "Локальный DNS-сервер",
                        value = editLocalDns,
                        onValueChange = { editLocalDns = it },
                        onDismiss = { showLocalDnsDialog = false },
                        onConfirm = {
                            scope.launch { store.setLocalDnsServer(it) }
                            showLocalDnsDialog = false
                        }
                    )
                }
            }

            HorizontalDivider(color = BypassColors.DarkBorder, modifier = Modifier.padding(horizontal = 16.dp))

            // DoH-сервер
            var editDohUrl by remember { mutableStateOf(dnsServer) }
            var showDohDialog by remember { mutableStateOf(false) }

            SettingsClickRow(
                title = "DoH-сервер",
                description = dnsServer,
                icon = Icons.Default.Http,
                onClick = { showDohDialog = true }
            )

            if (showDohDialog) {
                EditStringDialog(
                    title = "Адрес DoH-сервера",
                    value = editDohUrl,
                    onValueChange = { editDohUrl = it },
                    onDismiss = { showDohDialog = false },
                    onConfirm = {
                        scope.launch { store.setDnsServer(it) }
                        showDohDialog = false
                    }
                )
            }
        }

        Spacer(modifier = Modifier.height(8.dp))

        // ═══ ПОДКЛЮЧЕНИЕ ═══
        SettingsSectionHeader(title = "Подключение")
        SettingsCard {
            // Автоподключение
            SettingsSwitchRow(
                title = "Авто-подключение",
                description = "Подключаться при запуске приложения",
                icon = Icons.Default.Autorenew,
                checked = autoConnect,
                onCheckedChange = { scope.launch { store.setAutoConnect(it) } }
            )

            HorizontalDivider(color = BypassColors.DarkBorder, modifier = Modifier.padding(horizontal = 16.dp))

            // MTU
            var editMtu by remember { mutableStateOf(mtu.toString()) }
            var showMtuDialog by remember { mutableStateOf(false) }

            SettingsClickRow(
                title = "MTU",
                description = "$mtu байт",
                icon = Icons.Default.Straighten,
                onClick = { showMtuDialog = true }
            )

            if (showMtuDialog) {
                EditStringDialog(
                    title = "MTU (1280–9000)",
                    value = editMtu,
                    onValueChange = { editMtu = it },
                    onDismiss = { showMtuDialog = false },
                    onConfirm = {
                        it.toIntOrNull()?.let { v ->
                            scope.launch { store.setMtu(v) }
                        }
                        showMtuDialog = false
                    }
                )
            }
        }

        Spacer(modifier = Modifier.height(8.dp))

        // ═══ РАЗДЕЛЕНИЕ ТРАФИКА ═══
        SettingsSectionHeader(title = "Разделение трафика")
        SettingsCard {
            SettingsSwitchRow(
                title = "Split Tunneling",
                description = "Выбрать приложения для исключения из VPN",
                icon = Icons.Default.CallSplit,
                checked = splitTunneling,
                onCheckedChange = { scope.launch { store.setSplitTunneling(it) } }
            )
        }

        Spacer(modifier = Modifier.height(8.dp))

        // ═══ ВНЕШНИЙ ВИД ═══
        SettingsSectionHeader(title = "Внешний вид")
        SettingsCard {
            SettingsSwitchRow(
                title = "Тёмная тема",
                description = "Использовать тёмное оформление",
                icon = Icons.Default.DarkMode,
                checked = darkTheme,
                onCheckedChange = { scope.launch { store.setDarkTheme(it) } }
            )
        }

        Spacer(modifier = Modifier.height(8.dp))

        // ═══ ОТЛАДКА ═══
        SettingsSectionHeader(title = "Отладка")
        SettingsCard {
            SettingsSwitchRow(
                title = "Подробные логи",
                description = "Показывать расширенную диагностику",
                icon = Icons.Default.BugReport,
                checked = detailedLogs,
                onCheckedChange = { scope.launch { store.setDetailedLogs(it) } }
            )
        }

        Spacer(modifier = Modifier.height(32.dp))
    }
}

/**
 * Заголовок секции настроек.
 */
@Composable
private fun SettingsSectionHeader(title: String) {
    Text(
        text = title.uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = BypassColors.TextMuted,
        fontWeight = FontWeight.Bold,
        letterSpacing = 1.sp,
        modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp)
    )
}

/**
 * Карточка группы настроек.
 */
@Composable
private fun SettingsCard(content: @Composable ColumnScope.() -> Unit) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp),
        colors = CardDefaults.cardColors(
            containerColor = BypassColors.DarkSurface
        ),
        shape = RoundedCornerShape(12.dp),
        elevation = CardDefaults.cardElevation(defaultElevation = 0.dp)
    ) {
        Column(content = content)
    }
}

/**
 * Строка настройки с переключателем.
 */
@Composable
private fun SettingsSwitchRow(
    title: String,
    description: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = BypassColors.TextSecondary,
            modifier = Modifier.size(24.dp)
        )
        Spacer(modifier = Modifier.width(16.dp))
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyLarge,
                color = BypassColors.TextPrimary,
                fontWeight = FontWeight.Medium
            )
            Text(
                text = description,
                style = MaterialTheme.typography.bodySmall,
                color = BypassColors.TextSecondary,
                fontSize = 12.sp
            )
        }
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            colors = SwitchDefaults.colors(
                checkedTrackColor = BypassColors.GreenPrimary.copy(alpha = 0.5f),
                checkedThumbColor = BypassColors.GreenPrimary,
                uncheckedTrackColor = BypassColors.DarkBorder,
                uncheckedThumbColor = BypassColors.TextSecondary
            )
        )
    }
}

/**
 * Строка настройки с кликом.
 */
@Composable
private fun SettingsClickRow(
    title: String,
    description: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    onClick: () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = BypassColors.TextSecondary,
            modifier = Modifier.size(24.dp)
        )
        Spacer(modifier = Modifier.width(16.dp))
        Column(
            modifier = Modifier.weight(1f)
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyLarge,
                color = BypassColors.TextPrimary,
                fontWeight = FontWeight.Medium
            )
            Text(
                text = description,
                style = MaterialTheme.typography.bodySmall,
                color = BypassColors.TextSecondary,
                fontSize = 12.sp
            )
        }
        Icon(
            Icons.Default.ChevronRight,
            contentDescription = null,
            tint = BypassColors.TextMuted,
            modifier = Modifier.size(20.dp)
        )
    }
}

/**
 * Диалог редактирования строки (DNS, MTU).
 */
@Composable
private fun EditStringDialog(
    title: String,
    value: String,
    onValueChange: (String) -> Unit,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title, color = BypassColors.TextPrimary) },
        text = {
            OutlinedTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                colors = OutlinedTextFieldDefaults.colors(
                    focusedTextColor = BypassColors.TextPrimary,
                    unfocusedTextColor = BypassColors.TextPrimary,
                    cursorColor = BypassColors.GreenPrimary,
                    focusedBorderColor = BypassColors.GreenPrimary,
                    unfocusedBorderColor = BypassColors.DarkBorder
                )
            )
        },
        confirmButton = {
            TextButton(onClick = { onConfirm(value) }) {
                Text("ОК", color = BypassColors.GreenPrimary)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("Отмена", color = BypassColors.TextSecondary)
            }
        },
        containerColor = BypassColors.DarkSurface
    )
}