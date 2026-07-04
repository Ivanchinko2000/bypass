package com.bypass.vpn.util

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.Build
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import kotlin.math.pow
import kotlin.math.sqrt

/**
 * Помощник для измерения задержки до серверов.
 *
 * Реализует простой UDP/ICMP ping для проверки доступности сервера.
 * Используется для определения качества соединения и выбора
 * ближайшего сервера.
 *
 * Также может использоваться для whitelist-детекции:
 * проверяем доступность google.com — если недоступен, значит
 * интернет заблокирован без VPN.
 */
object PingHelper {

    /**
     * Результат пинга.
     *
     * @param latencyMs Задержка в миллисекундах (-1 если недоступен)
     * @param lossPercent Процент потерь пакетов
     * @param reachable Достижим ли хост
     */
    data class PingResult(
        val latencyMs: Long,
        val lossPercent: Float,
        val reachable: Boolean
    )

    /**
     * Измерить задержку до хоста через UDP-пинг.
     *
     * Отправляет несколько UDP-пакетов и измеряет время ответа.
     *
     * @param host Имя хоста или IP-адрес
     * @param port UDP-порт (по умолчанию 53 для DNS)
     * @param count Количество пакетов
     * @return Результат пинга
     */
    suspend fun ping(
        host: String,
        port: Int = 53,
        count: Int = 3
    ): PingResult = withContext(Dispatchers.IO) {
        try {
            val address = InetAddress.getByName(host)
            val latencies = mutableListOf<Long>()
            var lost = 0

            repeat(count) { _ ->
                val latency = singlePing(address, port)
                if (latency >= 0) {
                    latencies.add(latency)
                } else {
                    lost++
                }
                delay(200) // Пауза между пакетами
            }

            if (latencies.isEmpty()) {
                PingResult(
                    latencyMs = -1,
                    lossPercent = 100f,
                    reachable = false
                )
            } else {
                // Медиана для устойчивости к выбросам
                val sorted = latencies.sorted()
                val median = sorted[sorted.size / 2]
                val loss = (lost.toFloat() / count) * 100f

                PingResult(
                    latencyMs = median,
                    lossPercent = loss,
                    reachable = loss < 100f
                )
            }
        } catch (e: Exception) {
            PingResult(latencyMs = -1, lossPercent = 100f, reachable = false)
        }
    }

    /**
     * Одиночный UDP-пинг: отправляем пакет и замеряем время ответа.
     *
     * @return Задержка в мс или -1 при таймауте
     */
    private fun singlePing(address: InetAddress, port: Int): Long {
        val socket = DatagramSocket()
        socket.soTimeout = 3000 // 3 секунды таймаут

        return try {
            // Отправляем тестовый UDP-пакет (простой payload)
            val sendData = byteArrayOf(0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
            val sendPacket = DatagramPacket(sendData, sendData.size, address, port)

            val start = System.nanoTime()
            socket.send(sendPacket)

            // Пытаемся получить ответ
            val receiveData = ByteArray(512)
            val receivePacket = DatagramPacket(receiveData, receiveData.size)
            socket.receive(receivePacket)
            val elapsed = (System.nanoTime() - start) / 1_000_000 // наносекунды -> миллисекунды

            elapsed
        } catch (e: Exception) {
            -1L // Таймаут или ошибка
        } finally {
            socket.close()
        }
    }

    /**
     * Проверить доступность Google (whitelist-детекция).
     *
     * Если Google DNS (8.8.8.8) недоступен через обычный интернет,
     * значит трафик заблокирован и нужен VPN.
     *
     * @return true, если Google доступен (интернет работает без VPN)
     */
    suspend fun isGoogleReachable(): Boolean {
        val result = ping("8.8.8.8", port = 53, count = 1)
        return result.reachable
    }

    /**
     * Проверить, есть ли активное интернет-соединение.
     */
    fun hasInternet(context: Context): Boolean {
        val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            cm.activeNetwork?.let { network ->
                cm.getNetworkCapabilities(network)
                    ?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true
            } ?: false
        } else {
            @Suppress("DEPRECATION")
            cm.activeNetworkInfo?.isConnected == true
        }
    }

    /**
     * Определить тип текущего соединения.
     *
     * @return Строковое описание типа сети
     */
    fun getNetworkType(context: Context): String {
        val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val caps = cm.activeNetwork?.let { cm.getNetworkCapabilities(it) }
            when {
                caps?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true -> "Wi-Fi"
                caps?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true -> "Моб. сеть"
                caps?.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) == true -> "Ethernet"
                else -> "Нет сети"
            }
        } else {
            @Suppress("DEPRECATION")
            when (cm.activeNetworkInfo?.type) {
                ConnectivityManager.TYPE_WIFI -> "Wi-Fi"
                ConnectivityManager.TYPE_MOBILE -> "Моб. сеть"
                ConnectivityManager.TYPE_ETHERNET -> "Ethernet"
                else -> "Нет сети"
            }
        }
    }
}