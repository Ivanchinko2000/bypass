package com.bypass.vpn.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.bypass.vpn.service.TunnelManager
import com.bypass.vpn.ui.theme.BypassColors
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * Экран просмотра логов туннеля.
 *
 * Отображает события VPN-подключения в реальном времени.
 * Поддерживает фильтрацию по типу (все, ошибки, обычные)
 * и возможность копирования всех логов.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LogsScreen() {
    val context = LocalContext.current
    val clipboardManager = LocalClipboardManager.current
    val logs by TunnelManager.logs.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()

    // Фильтр
    var filter by remember { mutableStateOf(LogFilter.ALL) }

    // Отфильтрованные логи
    val filteredLogs = remember(logs, filter) {
        when (filter) {
            LogFilter.ALL -> logs
            LogFilter.ERRORS -> logs.filter { it.isError }
            LogFilter.INFO -> logs.filter { !it.isError }
        }
    }

    // Автопрокрутка вниз при новых логах
    LaunchedEffect(logs.size) {
        if (logs.isNotEmpty()) {
            listState.animateScrollToItem(logs.size - 1)
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(BypassColors.DarkBackground)
    ) {
        // ═══ Панель инструментов ═══
        Surface(
            color = BypassColors.DarkSurface,
            shadowElevation = 4.dp
        ) {
            Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp)) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        text = "Лог событий",
                        style = MaterialTheme.typography.titleLarge,
                        color = BypassColors.TextPrimary,
                        fontWeight = FontWeight.Bold
                    )

                    // Кнопки действий
                    Row {
                        // Копировать все логи
                        IconButton(onClick = {
                            val text = buildString {
                                logs.forEach { entry ->
                                    append(formatTimestamp(entry.timestamp))
                                    append(" ")
                                    if (entry.isError) append("[!] ")
                                    append(entry.message)
                                    append("\n")
                                }
                            }
                            clipboardManager.setText(AnnotatedString(text))
                            android.widget.Toast
                                .makeText(context, "Логи скопированы", android.widget.Toast.LENGTH_SHORT)
                                .show()
                        }) {
                            Icon(
                                Icons.Default.ContentCopy,
                                contentDescription = "Копировать",
                                tint = BypassColors.TextSecondary
                            )
                        }

                        // Очистить логи
                        IconButton(onClick = { TunnelManager.clearLogs() }) {
                            Icon(
                                Icons.Default.Delete,
                                contentDescription = "Очистить",
                                tint = BypassColors.Error
                            )
                        }
                    }
                }

                Spacer(modifier = Modifier.height(8.dp))

                // Фильтры
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    LogFilter.entries.forEach { f ->
                        FilterChip(
                            selected = filter == f,
                            onClick = { filter = f },
                            label = {
                                Text(
                                    f.label,
                                    color = if (filter == f) {
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
                                borderColor = if (filter == f) BypassColors.GreenPrimary else BypassColors.DarkBorder,
                                selectedBorderColor = BypassColors.GreenPrimary,
                                enabled = true,
                                selected = filter == f
                            )
                        )
                    }

                    Spacer(modifier = Modifier.weight(1f))

                    // Счётчик записей
                    Text(
                        text = "${filteredLogs.size} записей",
                        style = MaterialTheme.typography.labelSmall,
                        color = BypassColors.TextMuted,
                        modifier = Modifier.align(Alignment.CenterVertically)
                    )
                }
            }
        }

        // ═══ Список логов ═══
        Card(
            modifier = Modifier
                .fillMaxSize()
                .padding(12.dp),
            colors = CardDefaults.cardColors(
                containerColor = BypassColors.TerminalBackground
            ),
            shape = RoundedCornerShape(12.dp),
            elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
        ) {
            if (filteredLogs.isEmpty()) {
                // Пустое состояние
                Box(
                    modifier = Modifier.fillMaxSize().padding(32.dp),
                    contentAlignment = Alignment.Center
                ) {
                    Column(
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(16.dp)
                    ) {
                        Icon(
                            imageVector = Icons.Filled.Notes,
                            contentDescription = null,
                            modifier = Modifier.size(64.dp),
                            tint = BypassColors.TextMuted.copy(alpha = 0.5f)
                        )
                        Text(
                            text = "Логи пусты",
                            style = MaterialTheme.typography.titleMedium,
                            color = BypassColors.TextSecondary,
                            textAlign = TextAlign.Center
                        )
                        Text(
                            text = "События туннеля будут\nотображаться здесь",
                            style = MaterialTheme.typography.bodySmall,
                            color = BypassColors.TextMuted.copy(alpha = 0.7f),
                            textAlign = TextAlign.Center
                        )
                    }
                }
            } else {
                LazyColumn(
                    state = listState,
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(12.dp)
                ) {
                    items(filteredLogs, key = { it.id }) { entry ->
                        LogLineItem(entry)
                    }
                }
            }
        }
    }
}

/**
 * Элемент лога — одна строка в логе.
 */
@Composable
private fun LogLineItem(entry: TunnelManager.LogEntry) {
    val textColor = when {
        entry.isError -> BypassColors.TerminalRed
        entry.message.contains("подключ", ignoreCase = true) -> BypassColors.TerminalGreen
        entry.message.contains("скорость", ignoreCase = true) -> BypassColors.TerminalBlue
        entry.message.contains("переподключ", ignoreCase = true) -> BypassColors.TerminalYellow
        else -> BypassColors.TerminalText
    }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 3.dp)
    ) {
        // Таймстемп
        Text(
            text = formatTimestamp(entry.timestamp),
            color = BypassColors.TextMuted,
            fontSize = 11.sp,
            fontFamily = FontFamily.Monospace,
            modifier = Modifier.width(70.dp)
        )

        // Префикс ошибки
        Text(
            text = if (entry.isError) "[!] " else "    ",
            color = if (entry.isError) BypassColors.TerminalRed else BypassColors.TextMuted,
            fontSize = 12.sp,
            fontFamily = FontFamily.Monospace
        )

        // Сообщение
        Text(
            text = entry.message,
            color = textColor,
            fontSize = 12.sp,
            fontFamily = FontFamily.Monospace,
            lineHeight = 16.sp,
            fontWeight = if (entry.isError) FontWeight.Bold else FontWeight.Normal,
            modifier = Modifier.weight(1f)
        )
    }
}

/**
 * Форматировать таймстемп в короткую строку (ЧЧ:ММ:СС).
 */
private fun formatTimestamp(timestamp: Long): String {
    val sdf = SimpleDateFormat("HH:mm:ss", Locale.getDefault())
    return sdf.format(Date(timestamp))
}

/**
 * Фильтры логов.
 */
private enum class LogFilter(val label: String) {
    ALL("Все"),
    ERRORS("Ошибки"),
    INFO("Инфо")
}