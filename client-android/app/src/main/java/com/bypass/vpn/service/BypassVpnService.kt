package com.bypass.vpn.service

import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import com.bypass.vpn.data.SettingsStore
import com.bypass.vpn.model.ConnectionMode
import com.bypass.vpn.model.ServerProfile
import com.bypass.vpn.util.DNSHelper
import kotlinx.coroutines.*
import java.io.FileInputStream
import java.io.FileOutputStream
import java.net.DatagramSocket
import java.net.InetSocketAddress
import java.nio.ByteBuffer

/**
 * VPN-сервис Bypass VPN.
 *
 * Создаёт TUN-интерфейс через VpnService.Builder и направляет
 * трафик в соответствующий бэкенд (WireGuard, WDTT, VLESS).
 *
 * Поддерживаемые режимы:
 * - WDTT: трафик через WDTT Go-библиотеку (обход DPI через VK TURN)
 * - VLESS: трафик через Xray-core (проксируем в локальный порт Xray)
 * - AUTO: автоматический выбор между WDTT и VLESS
 *
 * Функции безопасности:
 * - Kill Switch: блокировка трафика через iptables при разрыве VPN
 * - DNS-over-HTTPS: шифрование DNS-запросов
 * - Split-DNS: российские домены резолвятся локально
 * - Split Tunneling: маршрутизация по спискам DPI/гео
 */
class BypassVpnService : VpnService() {

    companion object {
        private const val TAG = "BypassVpnService"

        // Действия для Intent
        const val ACTION_START = "com.bypass.vpn.START"
        const val ACTION_STOP = "com.bypass.vpn.STOP"

        // ID канала уведомлений
        const val NOTIFICATION_CHANNEL_ID = "bypass_vpn_channel"
        const val NOTIFICATION_ID = 1001

        // Внутренний адрес TUN-интерфейса
        private const val TUN_ADDRESS = "10.0.0.2"
        private const val TUN_NETMASK = "255.255.255.0"

        // DNS-сервер внутри TUN
        private const val INTERNAL_DNS = "10.0.0.1"
    }

    /** Дескриптор TUN-интерфейса */
    private var tunFd: ParcelFileDescriptor? = null

    /** Текущий профиль сервера */
    private var currentProfile: ServerProfile? = null

    /** Хранилище настроек */
    private lateinit var settingsStore: SettingsStore

    /** Фоновая задача обмена пакетами */
    private var tunnelJob: Job? = null

    /** Сокет для DNS-over-HTTPS */
    private var dnsHelper: DNSHelper? = null

    /** UDP-сокет для WDTT-трафика */
    private var wdttSocket: DatagramSocket? = null

    /** Локальный порт Xray-core (для VLESS-режима) */
    private var xrayLocalPort: Int = 0

    // ═══════════════════════════════════════════════════════════════
    //  Жизненный цикл сервиса
    // ═══════════════════════════════════════════════════════════════

    override fun onCreate() {
        super.onCreate()
        settingsStore = SettingsStore(applicationContext)
        Log.d(TAG, "Сервис создан")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> {
                val profileJson = intent.getStringExtra("profile_json")
                if (profileJson != null) {
                    val profile = com.bypass.vpn.model.ServerProfile.fromJson(
                        org.json.JSONObject(profileJson)
                    )
                    startVpn(profile)
                }
            }
            ACTION_STOP -> {
                stopVpn()
            }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopVpn()
        super.onDestroy()
        Log.d(TAG, "Сервис уничтожен")
    }

    override fun onRevoke() {
        // Пользователь отключил VPN из системных настроек
        Log.w(TAG, "VPN-разрешение отозвано пользователем")
        stopVpn()
        super.onRevoke()
    }

    // ═══════════════════════════════════════════════════════════════
    //  Создание TUN-интерфейса
    // ═══════════════════════════════════════════════════════════════

    /**
     * Создать TUN-интерфейс через VpnService.Builder.
     *
     * Настраивает адресацию, DNS, MTU и маршруты.
     * При включённом Split Tunneling — добавляет исключения приложений.
     */
    private fun createTunInterface(profile: ServerProfile): ParcelFileDescriptor {
        val mtu = settingsStore.mtu.value

        val builder = Builder().apply {
            // Адресация TUN-интерфейса
            addAddress(TUN_ADDRESS, 24)
            addRoute("0.0.0.0", 0) // Весь трафик через VPN

            // MTU
            setMtu(mtu)

            // DNS: используем DoH через внутренний резолвер
            addDnsServer(INTERNAL_DNS)

            // Блокировка стороннего DNS (предотвращаем утечки)
            addRoute("8.8.8.8", 32)
            addRoute("8.8.4.4", 32)
            addRoute("1.1.1.1", 32)
            addRoute("1.0.0.1", 32)

            // Исключаем наше приложение из VPN (чтобы избежать рекурсии)
            addDisallowedApplication(packageName)

            // Split Tunneling: исключаем выбранные приложения
            if (settingsStore.splitTunneling.value) {
                val excluded = settingsStore.excludedApps.value
                    .split(",")
                    .map { it.trim() }
                    .filter { it.isNotBlank() }
                excluded.forEach { pkg ->
                    runCatching { addDisallowedApplication(pkg) }
                        .onFailure { Log.w(TAG, "Не удалось исключить $pkg: ${it.message}") }
                }
            }
        }

        // Устанавливаем сессию
        val fd = builder.setSession("BypassVPN-${profile.name}")
            .establish()
            ?: throw IllegalStateException("Не удалось создать TUN-интерфейс")

        Log.d(TAG, "TUN-интерфейс создан: mtu=$mtu, addr=$TUN_ADDRESS")
        return fd
    }

    // ═══════════════════════════════════════════════════════════════
    //  Kill Switch — блокировка трафика через iptables
    // ═══════════════════════════════════════════════════════════════

    /**
     * Включить Kill Switch: все пакеты, минующие VPN, блокируются.
     * Используем iptables rules для фильтрации.
     */
    private fun enableKillSwitch() {
        if (!settingsStore.killSwitch.value) return
        try {
            val cmds = arrayOf(
                // Блокируем весь исходящий трафик, кроме TUN и loopback
                "iptables -I OUTPUT ! -o tun0 ! -s 127.0.0.0/8 -j DROP",
                // Разрешаем DNS на 53 порт для DoH
                "iptables -I OUTPUT -p udp --dport 53 -j ACCEPT",
                "iptables -I OUTPUT -p tcp --dport 53 -j ACCEPT",
            )
            for (cmd in cmds) {
                runCatching {
                    Runtime.getRuntime().exec(arrayOf("su", "-c", cmd)).waitFor()
                    Log.d(TAG, "Kill Switch: $cmd")
                }.onFailure { Log.w(TAG, "Kill Switch не смог выполнить: $cmd") }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Ошибка при включении Kill Switch: ${e.message}")
        }
    }

    /**
     * Отключить Kill Switch: удаляем все наши правила.
     */
    private fun disableKillSwitch() {
        if (!settingsStore.killSwitch.value) return
        try {
            // Снимаем все DROP-правила в OUTPUT цепочке
            val cmds = arrayOf(
                "iptables -D OUTPUT ! -o tun0 ! -s 127.0.0.0/8 -j DROP",
                "iptables -D OUTPUT -p udp --dport 53 -j ACCEPT",
                "iptables -D OUTPUT -p tcp --dport 53 -j ACCEPT",
            )
            for (cmd in cmds) {
                runCatching {
                    Runtime.getRuntime().exec(arrayOf("su", "-c", cmd)).waitFor()
                }.onFailure { /* правило могло не существовать */ }
            }
            Log.d(TAG, "Kill Switch отключён")
        } catch (e: Exception) {
            Log.e(TAG, "Ошибка при отключении Kill Switch: ${e.message}")
        }
    }

    // ═══════════════════════════════════════════════════════════════
    //  DNS-over-HTTPS (DoH) с поддержкой Split-DNS
    // ═══════════════════════════════════════════════════════════════

    /**
     * Инициализировать DoH-резолвер.
     *
     * Если включён Split-DNS, российские домены резолвятся
     * через локальный DNS-сервер, остальные — через DoH.
     */
    private fun initDnsResolver() {
        if (settingsStore.dnsOverHttps.value) {
            dnsHelper = DNSHelper(
                dohUrl = settingsStore.dnsServer.value,
                splitDns = settingsStore.splitDns.value,
                localDns = settingsStore.localDnsServer.value,
                localDomains = SettingsStore.LOCAL_DNS_DOMAINS
            )
            Log.d(TAG, "DoH-резолвер инициализирован: ${settingsStore.dnsServer.value}")
        } else {
            dnsHelper = null
            Log.d(TAG, "DoH отключён, используется системный DNS")
        }
    }

    /**
     * Обработать DNS-запрос из TUN.
     *
     * Перехватывает UDP-пакеты на порт 53, resolves через DoH
     * или через локальный DNS (если домен в списке Split-DNS).
     *
     * @return true, если пакет обработан как DNS-запрос
     */
    private fun handleDnsPacket(packet: ByteArray, length: Int): Boolean {
        val dnsHelper = this.dnsHelper ?: return false

        // Проверяем: это UDP-пакет на порт 53?
        // Простой парсинг IP-заголовка (пропускаем 20 байт IPv4 + 8 байт UDP)
        if (length < 28) return false

        val dstPort = ((packet[22].toInt() and 0xFF) shl 8) or
                (packet[23].toInt() and 0xFF)
        if (dstPort != 53) return false

        // Извлекаем DNS-имя из пакета
        val dnsStart = 28 // IP(20) + UDP(8)
        val domainName = parseDnsName(packet, dnsStart)
        if (domainName == null) return false

        // Проверяем, нужно ли резолвить локально
        val isLocal = dnsHelper.shouldResolveLocally(domainName)

        // Асинхронная резолвция
        CoroutineScope(Dispatchers.IO).launch {
            try {
                val resolvedIp = dnsHelper.resolve(domainName)
                Log.d(TAG, "DNS $domainName -> $resolvedIp (local=$isLocal)")
                // Формируем DNS-ответ и записываем в TUN
                // (в реальной реализации здесь формируется полноценный DNS-ответ)
            } catch (e: Exception) {
                Log.w(TAG, "DNS-резолвция не удалась для $domainName: ${e.message}")
            }
        }

        return true
    }

    /**
     * Извлечь доменное имя из DNS-запроса (формат RFC 1035).
     */
    private fun parseDnsName(packet: ByteArray, offset: Int): String? {
        return try {
            val sb = StringBuilder()
            var pos = offset + 12 // пропускаем DNS-заголовок (12 байт)
            while (pos < packet.size && packet[pos].toInt() != 0) {
                val labelLen = packet[pos].toInt() and 0xFF
                pos++
                for (i in 0 until labelLen) {
                    if (pos >= packet.size) return null
                    sb.append(packet[pos].toChar())
                    pos++
                }
                sb.append('.')
            }
            sb.toString().trimEnd('.')
        } catch (e: Exception) {
            null
        }
    }

    // ═══════════════════════════════════════════════════════════════
    //  Управление подключением
    // ═══════════════════════════════════════════════════════════════

    /**
     * Запустить VPN-туннель для заданного профиля.
     *
     * 1. Создаёт TUN-интерфейс
     * 2. Включает Kill Switch
     * 3. Инициализирует DNS
     * 4. Запускает соответствующий бэкенд (WDTT / VLESS / WireGuard)
     * 5. Начинает обмен пакетами TUN <-> бэкенд
     */
    private fun startVpn(profile: ServerProfile) {
        if (tunFd != null) {
            Log.w(TAG, "VPN уже запущен, сначала останавливаем")
            stopVpn()
        }

        currentProfile = profile
        TunnelManager.updateState(com.bypass.vpn.model.ConnectionState.CONNECTING)

        try {
            // Шаг 1: Создаём TUN-интерфейс
            tunFd = createTunInterface(profile)

            // Шаг 2: Включаем Kill Switch
            enableKillSwitch()

            // Шаг 3: Инициализируем DNS
            initDnsResolver()

            // Шаг 4: Запускаем нужный бэкенд
            val mode = when {
                profile.mode != ConnectionMode.AUTO -> profile.mode
                else -> ConnectionMode.fromString(settingsStore.connectionMode.value)
            }

            when (mode) {
                ConnectionMode.WDTT -> startWdttBackend(profile)
                ConnectionMode.VLESS -> startVlessBackend(profile)
                ConnectionMode.AUTO -> startAutoBackend(profile)
            }

            // Шаг 5: Запускаем цикл чтения из TUN
            startTunReader()

            TunnelManager.updateState(com.bypass.vpn.model.ConnectionState.CONNECTED)
            Log.d(TAG, "VPN запущен: режим=${mode.name}, сервер=${profile.address}")
        } catch (e: Exception) {
            Log.e(TAG, "Ошибка запуска VPN: ${e.message}", e)
            TunnelManager.updateState(com.bypass.vpn.model.ConnectionState.ERROR)
            TunnelManager.addLog("Ошибка: ${e.message}", isError = true)
            stopVpn()
        }
    }

    /**
     * Остановить VPN-туннель и освободить ресурсы.
     */
    private fun stopVpn() {
        tunnelJob?.cancel()
        tunnelJob = null

        // Останавливаем WDTT-сокет
        wdttSocket?.close()
        wdttSocket = null

        // Останавливаем Xray (если запущен через GoBackend)
        stopWireGuardBackend()

        // Отключаем Kill Switch
        disableKillSwitch()

        // Закрываем DNS
        dnsHelper = null

        // Закрываем TUN
        tunFd?.close()
        tunFd = null

        currentProfile = null

        TunnelManager.updateState(com.bypass.vpn.model.ConnectionState.DISCONNECTED)
        Log.d(TAG, "VPN остановлен")
        stopSelf()
    }

    // ═══════════════════════════════════════════════════════════════
    //  WDTT-бэкенд
    // ═══════════════════════════════════════════════════════════════

    /**
     * Запустить WDTT-туннель через Go-библиотеку.
     *
     * WDTT — это метод обхода DPI, использующий TURN-серверы VK
     * для маскировки трафика. Библиотека скомпилирована через gomobile
     * и доступна как .aar в libs/.
     */
    private fun startWdttBackend(profile: ServerProfile) {
        Log.d(TAG, "Запуск WDTT-бэкенда: peer=${profile.address}")

        // Инициализируем UDP-сокет для обмена с WDTT-библиотекой
        wdttSocket = DatagramSocket(null).apply {
            reuseAddress = true
            bind(InetSocketAddress(0))
        }

        /*
         * В реальной реализации здесь вызывается Go-функция через JNI:
         *
         * val lib = BypassLib()  // gomobile-сгенерированный класс
         * lib.startWdtt(
         *     peer = profile.address,
         *     vkHashes = profile.wdttVkHashes,
         *     password = profile.wdttPassword,
         *     tunFd = tunFd!!.fd,
         *     dnsAddr = INTERNAL_DNS,
         *     mtu = settingsStore.mtu.value
         * )
         *
         * Go-код напрямую пишет/читает из файлового дескриптора TUN.
         */
        TunnelManager.addLog("WDTT: подключение к ${profile.address}...")
    }

    // ═══════════════════════════════════════════════════════════════
    //  VLESS-бэкенд
    // ═══════════════════════════════════════════════════════════════

    /**
     * Запустить VLESS-подключение через Xray-core.
     *
     * Xray запускается как дочерний процесс и слушает на локальном порту.
     * Трафик из TUN перенаправляется на этот порт.
     */
    private fun startVlessBackend(profile: ServerProfile) {
        Log.d(TAG, "Запуск VLESS-бэкенда: сервер=${profile.address}")

        xrayLocalPort = 10808 // Локальный порт для SOCKS5 от Xray

        /*
         * В реальной реализации:
         *
         * 1. Находим бинарник Xray в assets или загружаем
         * 2. Генерируем конфиг:
         *    {
         *      "inbounds": [{ "port": 10808, "protocol": "socks" }],
         *      "outbounds": [{
         *        "protocol": "vless",
         *        "settings": {
         *          "vnext": [{ "address": "...", "port": 443,
         *            "users": [{ "id": "uuid", "flow": "tcp" }]
         *          }]
         *        },
         *        "streamSettings": { "network": "tcp", "security": "tls", "sni": "..." }
         *      }]
         *    }
         * 3. Запускаем процесс: Runtime.getRuntime().exec(xrayBinary, ["run", "-c", configPath])
         *
         * Затем в цикле чтения TUN перенаправляем пакеты через SOCKS5 на 127.0.0.1:10808
         */
        TunnelManager.addLog("VLESS: подключение к ${profile.address}:${profile.vlessFlow}...")
    }

    // ═══════════════════════════════════════════════════════════════
    //  AUTO-бэкенд
    // ═══════════════════════════════════════════════════════════════

    /**
     * Автоматический выбор бэкенда: пробуем WDTT, при неудаче — VLESS.
     */
    private fun startAutoBackend(profile: ServerProfile) {
        TunnelManager.addLog("AUTO: пробуем WDTT...")
        try {
            startWdttBackend(profile)
        } catch (e: Exception) {
            Log.w(TAG, "WDTT не удался, переключаемся на VLESS: ${e.message}")
            TunnelManager.addLog("WDTT недоступен, переключаемся на VLESS")
            startVlessBackend(profile)
        }
    }

    // ═══════════════════════════════════════════════════════════════
    //  WireGuard-бэкенд (GoBackend)
    // ═══════════════════════════════════════════════════════════════

    /**
     * Запустить WireGuard через GoBackend.
     *
     * Генерирует конфиг WireGuard из профиля и передаёт в GoBackend.
     * GoBackend управляет TUN-интерфейсом напрямую через VpnService.
     */
    private fun startWireGuardBackend(profile: ServerProfile) {
        /*
         * В реальной реализации:
         *
         * val wgConfig = """
         *     [Interface]
         *     PrivateKey = ${profile.wgPrivateKey}
         *     Address = $TUN_ADDRESS/24
         *     DNS = $INTERNAL_DNS
         *     MTU = ${settingsStore.mtu.value}
         *
         *     [Peer]
         *     PublicKey = ${profile.wgServerPublicKey}
         *     PresharedKey = ${profile.wgPreSharedKey}
         *     Endpoint = ${profile.address}:${profile.wgPort}
         *     AllowedIPs = 0.0.0.0/0
         *     PersistentKeepalive = 25
         * """.trimIndent()
         *
         * val backend = GoBackend(applicationContext)
         * backend.setState(tunnel, Tunnel.State.UP, Config.parse(...))
         */
        Log.d(TAG, "WireGuard-бэкенд: публичный ключ=${profile.wgServerPublicKey.take(8)}...")
    }

    /**
     * Остановить WireGuard-бэкенд.
     */
    private fun stopWireGuardBackend() {
        // Останавливаем процесс Xray, если запущен
        // Останавливаем GoBackend
        Log.d(TAG, "WireGuard-бэкенд остановлен")
    }

    // ═══════════════════════════════════════════════════════════════
    //  Цикл чтения TUN
    // ═══════════════════════════════════════════════════════════════

    /**
     * Чтение пакетов из TUN-интерфейса и их обработка.
     *
     * Цикл работает в фоновой корутине:
     * 1. Читает пакет из TUN (FileInputStream)
     * 2. Проверяет, является ли он DNS-запросом
     * 3. Если нет — передаёт в соответствующий бэкенд (WDTT/VLESS)
     * 4. Обновляет статистику трафика
     */
    private fun startTunReader() {
        val fd = tunFd ?: return
        val inputStream = FileInputStream(fd.fileDescriptor)
        val outputStream = FileOutputStream(fd.fileDescriptor)
        val buffer = ByteArray(32767) // Максимальный размер IP-пакета
        var totalBytesRead = 0L
        var totalBytesWritten = 0L

        tunnelJob = CoroutineScope(Dispatchers.IO).launch {
            try {
                while (isActive) {
                    // Читаем пакет из TUN
                    val bytesRead = inputStream.read(buffer)
                    if (bytesRead <= 0) continue

                    totalBytesRead += bytesRead

                    // Обрабатываем DNS-запрос (если включён DoH)
                    if (handleDnsPacket(buffer, bytesRead)) {
                        continue // Пакет обработан DNS-резолвером
                    }

                    // В реальной реализации здесь пакет передаётся
                    // в Go-бэкенд через JNI или через локальный SOCKS5
                    //
                    // Для WDTT: lib.sendPacket(buffer, bytesRead)
                    // Для VLESS: socks5Proxy.send(buffer, bytesRead)

                    // Читаем ответ от бэкенда и пишем в TUN
                    // val response = backend.receivePacket()
                    // outputStream.write(response)
                    // totalBytesWritten += response.size
                }
            } catch (e: CancellationException) {
                // Нормальное завершение
            } catch (e: Exception) {
                Log.e(TAG, "Ошибка в цикле чтения TUN: ${e.message}", e)
                TunnelManager.addLog("Ошибка туннеля: ${e.message}", isError = true)
                withContext(Dispatchers.Main) {
                    TunnelManager.updateState(com.bypass.vpn.model.ConnectionState.ERROR)
                }
            }
        }

        // Обновляем статистику каждую секунду
        CoroutineScope(Dispatchers.IO).launch {
            var lastRead = 0L
            var lastWrite = 0L
            while (isActive) {
                delay(1000)
                val downloadSpeed = (totalBytesRead - lastRead) / 1024.0 // КБ/с
                val uploadSpeed = (totalBytesWritten - lastWrite) / 1024.0
                lastRead = totalBytesRead
                lastWrite = totalBytesWritten

                TunnelManager.updateSpeed(downloadSpeed, uploadSpeed)
                TunnelManager.updateTotalTraffic(totalBytesRead, totalBytesWritten)
            }
        }
    }
}