package com.bypass.vpn.ui.navigation

import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ListAlt
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.bypass.vpn.ui.screens.ConnectScreen
import com.bypass.vpn.ui.screens.LogsScreen
import com.bypass.vpn.ui.screens.ProfilesScreen
import com.bypass.vpn.ui.screens.SettingsScreen
import com.bypass.vpn.ui.theme.BypassColors

/**
 * Навигационный маршрут.
 */
sealed class Screen(val route: String, val label: String, val icon: ImageVector) {
    data object Connect : Screen("connect", "Подключение", Icons.Default.PowerSettingsNew)
    data object Profiles : Screen("profiles", "Профили", Icons.Default.ListAlt)
    data object Logs : Screen("logs", "Логи", Icons.Default.ListAlt)
    data object Settings : Screen("settings", "Настройки", Icons.Default.Settings)
}

/**
 * Главная навигация приложения.
 *
 * Реализует нижнюю панель навигации с 4 экранами:
 * - Подключение: большой кнопка включения/выключения
 * - Профили: список серверов
 * - Логи: просмотр событий туннеля
 * - Настройки: параметры приложения
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AppNavigation() {
    val navController = rememberNavController()

    // Список экранов для нижней панели
    val screens = listOf(
        Screen.Connect,
        Screen.Profiles,
        Screen.Logs,
        Screen.Settings
    )

    // Текущий маршрут для подсветки в навигации
    val navBackStackEntry by navController.currentBackStackEntryAsState()
    val currentDestination = navBackStackEntry?.destination

    Scaffold(
        bottomBar = {
            NavigationBar(
                containerColor = BypassColors.DarkSurface,
                contentColor = BypassColors.TextPrimary
            ) {
                screens.forEach { screen ->
                    NavigationBarItem(
                        icon = {
                            Icon(
                                imageVector = screen.icon,
                                contentDescription = screen.label
                            )
                        },
                        label = { Text(screen.label) },
                        selected = currentDestination?.hierarchy?.any {
                            it.route == screen.route
                        } == true,
                        onClick = {
                            navController.navigate(screen.route) {
                                // Очищаем стек при переключении вкладок
                                popUpTo(navController.graph.findStartDestination().id) {
                                    saveState = true
                                }
                                launchSingleTop = true
                                restoreState = true
                            }
                        },
                        colors = NavigationBarItemDefaults.colors(
                            selectedIconColor = BypassColors.GreenPrimary,
                            selectedTextColor = BypassColors.GreenPrimary,
                            unselectedIconColor = BypassColors.TextSecondary,
                            unselectedTextColor = BypassColors.TextSecondary,
                            indicatorColor = BypassColors.GreenDark.copy(alpha = 0.3f)
                        )
                    )
                }
            }
        }
    ) { innerPadding ->
        NavHost(
            navController = navController,
            startDestination = Screen.Connect.route,
            modifier = Modifier.padding(innerPadding)
        ) {
            // Экран подключения
            composable(Screen.Connect.route) {
                ConnectScreen(
                    onNavigateToProfiles = {
                        navController.navigate(Screen.Profiles.route) {
                            popUpTo(Screen.Connect.route) { saveState = true }
                            launchSingleTop = true
                            restoreState = true
                        }
                    }
                )
            }

            // Экран профилей
            composable(Screen.Profiles.route) {
                ProfilesScreen()
            }

            // Экран логов
            composable(Screen.Logs.route) {
                LogsScreen()
            }

            // Экран настроек
            composable(Screen.Settings.route) {
                SettingsScreen()
            }
        }
    }
}