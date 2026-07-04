package com.bypass.vpn.ui.screens

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.bypass.vpn.data.ProfilesStore
import com.bypass.vpn.model.ConnectionMode
import com.bypass.vpn.model.ServerProfile
import com.bypass.vpn.service.TunnelManager
import com.bypass.vpn.ui.theme.BypassColors
import kotlinx.coroutines.launch

/**
 * Экран управления профилями серверов.
 *
 * Отображает список всех серверов с возможностью:
 * - Добавить новый профиль
 * - Редактировать существующий
 * - Удалить профиль
 * - Выбрать профиль для подключения
 * - Показать пинг до сервера
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProfilesScreen() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val store = remember { ProfilesStore(context) }
    val profiles by store.profiles.collectAsStateWithLifecycle()
    val activeId by store.activeProfileId.collectAsStateWithLifecycle()

    // Диалог добавления/редактирования
    var showAddDialog by remember { mutableStateOf(false) }
    var editingProfile by remember { mutableStateOf<ServerProfile?>(null) }

    // Подтверждение удаления
    var deletingProfile by remember { mutableStateOf<ServerProfile?>(null) }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(BypassColors.DarkBackground)
    ) {
        // ═══ Заголовок ═══
        Surface(
            color = BypassColors.DarkSurface,
            shadowElevation = 4.dp
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 16.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Column {
                    Text(
                        text = "Профили серверов",
                        style = MaterialTheme.typography.titleLarge,
                        color = BypassColors.TextPrimary,
                        fontWeight = FontWeight.Bold
                    )
                    Text(
                        text = "${profiles.size} серверов",
                        style = MaterialTheme.typography.bodySmall,
                        color = BypassColors.TextSecondary
                    )
                }

                FilledTonalButton(
                    onClick = { showAddDialog = true },
                    colors = ButtonDefaults.filledTonalButtonColors(
                        containerColor = BypassColors.GreenDark.copy(alpha = 0.3f),
                        contentColor = BypassColors.GreenPrimary
                    )
                ) {
                    Icon(Icons.Default.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(modifier = Modifier.width(4.dp))
                    Text("Добавить")
                }
            }
        }

        // ═══ Список профилей ═══
        if (profiles.isEmpty()) {
            // Пустое состояние
            Box(
                modifier = Modifier.fillMaxSize(),
                contentAlignment = Alignment.Center
            ) {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(12.dp)
                ) {
                    Icon(
                        Icons.Default.Dns,
                        contentDescription = null,
                        modifier = Modifier.size(64.dp),
                        tint = BypassColors.TextMuted
                    )
                    Text(
                        text = "Нет серверов",
                        style = MaterialTheme.typography.titleMedium,
                        color = BypassColors.TextSecondary
                    )
                    Text(
                        text = "Нажмите «Добавить» чтобы\nсоздать профиль сервера",
                        style = MaterialTheme.typography.bodySmall,
                        color = BypassColors.TextMuted,
                        textAlign = androidx.compose.ui.text.style.TextAlign.Center
                    )
                }
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                items(profiles, key = { it.id }) { profile ->
                    ProfileCard(
                        profile = profile,
                        isActive = profile.id == activeId,
                        onClick = {
                            scope.launch {
                                store.setActiveProfile(profile.id)
                                TunnelManager.addLog("Выбран сервер: ${profile.name}")
                            }
                        },
                        onEdit = { editingProfile = profile },
                        onDelete = { deletingProfile = profile }
                    )
                }
            }
        }
    }

    // ═══ Диалог добавления/редактирования ═══
    if (showAddDialog || editingProfile != null) {
        ProfileDialog(
            profile = editingProfile,
            onDismiss = {
                showAddDialog = false
                editingProfile = null
            },
            onSave = { name, address, mode ->
                scope.launch {
                    if (editingProfile != null) {
                        // Обновляем существующий
                        store.updateProfile(editingProfile!!.copy(
                            name = name,
                            address = address,
                            mode = mode
                        ))
                    } else {
                        // Создаём новый
                        store.createProfile(name, address).let { created ->
                            store.updateProfile(created.copy(mode = mode))
                        }
                    }
                    showAddDialog = false
                    editingProfile = null
                }
            }
        )
    }

    // ═══ Подтверждение удаления ═══
    deletingProfile?.let { profile ->
        AlertDialog(
            onDismissRequest = { deletingProfile = null },
            title = {
                Text(
                    "Удалить профиль?",
                    color = BypassColors.TextPrimary
                )
            },
            text = {
                Text(
                    "Сервер «${profile.name}» будет удалён безвозвратно.",
                    color = BypassColors.TextSecondary
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        scope.launch {
                            store.deleteProfile(profile.id)
                            deletingProfile = null
                        }
                    },
                    colors = ButtonDefaults.textButtonColors(
                        contentColor = BypassColors.Error
                    )
                ) {
                    Text("Удалить")
                }
            },
            dismissButton = {
                TextButton(onClick = { deletingProfile = null }) {
                    Text("Отмена", color = BypassColors.TextSecondary)
                }
            },
            containerColor = BypassColors.DarkSurface
        )
    }
}

/**
 * Карточка профиля сервера.
 */
@Composable
private fun ProfileCard(
    profile: ServerProfile,
    isActive: Boolean,
    onClick: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit
) {
    val borderColor = if (isActive) BypassColors.GreenPrimary else BypassColors.DarkBorder
    val backgroundColor = if (isActive) {
        BypassColors.DarkSurfaceVariant.copy(alpha = 0.5f)
    } else {
        BypassColors.DarkSurface
    }

    Card(
        modifier = Modifier
            .fillMaxWidth()
            .animateContentSize()
            .clip(RoundedCornerShape(12.dp))
            .background(backgroundColor)
            .then(
                if (isActive) {
                    Modifier.background(
                        brush = androidx.compose.foundation.BorderStroke(
                            width = 1.dp,
                            color = borderColor
                        ).let { }
                    )
                } else {
                    Modifier
                }
            ),
        colors = CardDefaults.cardColors(containerColor = backgroundColor),
        shape = RoundedCornerShape(12.dp),
        elevation = CardDefaults.cardElevation(
            defaultElevation = if (isActive) 4.dp else 0.dp
        )
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onClick)
                .padding(16.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            // Индикатор статуса
            Box(
                modifier = Modifier
                    .size(10.dp)
                    .clip(androidx.compose.foundation.shape.CircleShape)
                    .background(if (isActive) BypassColors.GreenPrimary else BypassColors.TextMuted)
            )

            Spacer(modifier = Modifier.width(12.dp))

            // Информация о сервере
            Column(modifier = Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = profile.name,
                        style = MaterialTheme.typography.titleSmall,
                        color = BypassColors.TextPrimary,
                        fontWeight = FontWeight.SemiBold,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    // Бейдж режима
                    Surface(
                        color = when (profile.mode) {
                            ConnectionMode.AUTO -> BypassColors.TerminalBlue.copy(alpha = 0.2f)
                            ConnectionMode.WDTT -> BypassColors.TerminalYellow.copy(alpha = 0.2f)
                            ConnectionMode.VLESS -> BypassColors.TerminalGreen.copy(alpha = 0.2f)
                        },
                        shape = RoundedCornerShape(4.dp)
                    ) {
                        Text(
                            text = profile.mode.displayName,
                            fontSize = 10.sp,
                            color = when (profile.mode) {
                                ConnectionMode.AUTO -> BypassColors.TerminalBlue
                                ConnectionMode.WDTT -> BypassColors.TerminalYellow
                                ConnectionMode.VLESS -> BypassColors.TerminalGreen
                            },
                            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp)
                        )
                    }
                }

                Spacer(modifier = Modifier.height(2.dp))

                Text(
                    text = profile.address,
                    style = MaterialTheme.typography.bodySmall,
                    color = BypassColors.TextSecondary,
                    fontFamily = FontFamily.Monospace,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis
                )

                if (profile.trafficMb > 0) {
                    Text(
                        text = "Трафик: ${"%.1f".format(profile.trafficMb)} МБ",
                        style = MaterialTheme.typography.labelSmall,
                        color = BypassColors.TextMuted,
                        fontSize = 11.sp
                    )
                }
            }

            // Кнопки действий
            IconButton(onClick = onEdit) {
                Icon(
                    Icons.Default.Edit,
                    contentDescription = "Редактировать",
                    tint = BypassColors.TextSecondary,
                    modifier = Modifier.size(20.dp)
                )
            }
            IconButton(onClick = onDelete) {
                Icon(
                    Icons.Default.Delete,
                    contentDescription = "Удалить",
                    tint = BypassColors.Error.copy(alpha = 0.7f),
                    modifier = Modifier.size(20.dp)
                )
            }
        }
    }
}

/**
 * Диалог добавления/редактирования профиля.
 */
@Composable
private fun ProfileDialog(
    profile: ServerProfile?,
    onDismiss: () -> Unit,
    onSave: (name: String, address: String, ConnectionMode) -> Unit
) {
    var name by remember(profile) { mutableStateOf(profile?.name ?: "") }
    var address by remember(profile) { mutableStateOf(profile?.address ?: "") }
    var mode by remember(profile) { mutableStateOf(profile?.mode ?: ConnectionMode.AUTO) }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                if (profile != null) "Редактировать сервер" else "Новый сервер",
                color = BypassColors.TextPrimary
            )
        },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Имя сервера") },
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

                OutlinedTextField(
                    value = address,
                    onValueChange = { address = it },
                    label = { Text("Адрес (хост:порт)") },
                    singleLine = true,
                    placeholder = { Text("example.com:443") },
                    modifier = Modifier.fillMaxWidth(),
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedTextColor = BypassColors.TextPrimary,
                        unfocusedTextColor = BypassColors.TextPrimary,
                        cursorColor = BypassColors.GreenPrimary,
                        focusedBorderColor = BypassColors.GreenPrimary,
                        unfocusedBorderColor = BypassColors.DarkBorder
                    )
                )

                Text(
                    text = "Режим подключения",
                    style = MaterialTheme.typography.labelMedium,
                    color = BypassColors.TextSecondary
                )

                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    ConnectionMode.entries.forEach { m ->
                        FilterChip(
                            selected = mode == m,
                            onClick = { mode = m },
                            label = { Text(m.displayName) },
                            colors = FilterChipDefaults.filterChipColors(
                                selectedContainerColor = BypassColors.GreenDark.copy(alpha = 0.3f),
                                containerColor = BypassColors.DarkSurfaceVariant
                            )
                        )
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onSave(name, address, mode) },
                enabled = name.isNotBlank() && address.isNotBlank()
            ) {
                Text("Сохранить", color = BypassColors.GreenPrimary)
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