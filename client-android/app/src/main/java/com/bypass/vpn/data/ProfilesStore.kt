package com.bypass.vpn.data

import android.content.Context
import android.content.SharedPreferences
import com.bypass.vpn.model.ServerProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import org.json.JSONArray
import java.util.UUID

/**
 * Хранилище профилей серверов.
 *
 * Использует SharedPreferences + JSON для сериализации списка профилей.
 * Все операции выполняются в IO-диспетчере.
 */
class ProfilesStore(context: Context) {

    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences("bypass_profiles", Context.MODE_PRIVATE)

    private val _profiles = MutableStateFlow(loadProfiles())
    /** Поток списка профилей для наблюдения из UI */
    val profiles: StateFlow<List<ServerProfile>> = _profiles.asStateFlow()

    /** Текущий активный профиль */
    private val _activeProfileId = MutableStateFlow(prefs.getString("active_profile_id", "") ?: "")
    val activeProfileId: StateFlow<String> = _activeProfileId.asStateFlow()

    /**
     * Загрузить профили из SharedPreferences.
     * Сохраняются как JSON-массив в строке.
     */
    private fun loadProfiles(): List<ServerProfile> {
        val raw = prefs.getString("profiles_json", null) ?: return emptyList()
        return try {
            val arr = JSONArray(raw)
            (0 until arr.length()).mapNotNull { i ->
                runCatching { ServerProfile.fromJson(arr.getJSONObject(i)) }.getOrNull()
            }.filter { it.id.isNotBlank() && it.address.isNotBlank() }
        } catch (e: Exception) {
            emptyList()
        }
    }

    /**
     * Сохранить текущий список профилей в SharedPreferences.
     */
    private suspend fun persist() = withContext(Dispatchers.IO) {
        val arr = JSONArray()
        _profiles.value.forEach { p -> arr.put(p.toJson()) }
        prefs.edit().putString("profiles_json", arr.toString()).apply()
    }

    /**
     * Создать новый профиль с уникальным ID.
     */
    suspend fun createProfile(
        name: String,
        address: String
    ): ServerProfile {
        val profile = ServerProfile(
            id = UUID.randomUUID().toString(),
            name = name,
            address = address
        )
        _profiles.value = _profiles.value + profile
        persist()
        return profile
    }

    /**
     * Обновить существующий профиль (перезапись по ID).
     */
    suspend fun updateProfile(profile: ServerProfile) {
        _profiles.value = _profiles.value.map {
            if (it.id == profile.id) profile else it
        }
        persist()
    }

    /**
     * Удалить профиль по ID.
     * Если удаляется активный профиль — сбрасываем активный.
     */
    suspend fun deleteProfile(id: String) {
        _profiles.value = _profiles.value.filter { it.id != id }
        if (_activeProfileId.value == id) {
            _activeProfileId.value = ""
            prefs.edit().remove("active_profile_id").apply()
        }
        persist()
    }

    /**
     * Получить профиль по ID (однократное чтение).
     */
    fun getProfileById(id: String): ServerProfile? =
        _profiles.value.find { it.id == id }

    /**
     * Установить активный профиль.
     */
    suspend fun setActiveProfile(id: String) {
        _activeProfileId.value = id
        prefs.edit().putString("active_profile_id", id).apply()
    }

    /**
     * Получить текущий активный профиль.
     */
    fun getActiveProfile(): ServerProfile? {
        val id = _activeProfileId.value
        return if (id.isBlank()) null else getProfileById(id)
    }

    /**
     * Добавить трафик к профилю.
     */
    suspend fun addTraffic(id: String, mb: Double) {
        if (mb <= 0.0) return
        _profiles.value = _profiles.value.map {
            if (it.id == id) it.copy(trafficMb = it.trafficMb + mb) else it
        }
        persist()
    }

    /**
     * Обновить время последнего подключения.
     */
    suspend fun touchLastConnected(id: String) {
        val now = System.currentTimeMillis()
        _profiles.value = _profiles.value.map {
            if (it.id == id) it.copy(lastConnectedAt = now) else it
        }
        persist()
    }

    /**
     * Очистить весь список профилей.
     */
    suspend fun clearAll() {
        _profiles.value = emptyList()
        _activeProfileId.value = ""
        prefs.edit().clear().apply()
    }
}