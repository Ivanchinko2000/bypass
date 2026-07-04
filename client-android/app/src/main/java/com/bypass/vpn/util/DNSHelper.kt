package com.bypass.vpn.util

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.URL
import javax.net.ssl.HttpsURLConnection

/**
 * DNS-over-HTTPS клиент для Android.
 *
 * Выполняет DNS-запросы через HTTPS, шифруя их от провайдера.
 * Поддерживает Split-DNS: российские домены можно резолвить
 * через обычный (незащищённый) DNS-сервер.
 *
 * Реализует метод RFC 8484 (DNS Queries over HTTPS).
 */
class DNSHelper(
    /** URL DoH-сервера (например, https://dns.google/dns-query) */
    private val dohUrl: String = "https://dns.google/dns-query",
    /** Включить Split-DNS (локальная резолвция для российских доменов) */
    private val splitDns: Boolean = false,
    /** Локальный DNS-сервер для Split-DNS */
    private val localDns: String = "77.88.8.8",
    /** Домены, которые резолвятся локально */
    private val localDomains: List<String> = emptyList()
) {
    companion object {
        private const val TAG = "DNSHelper"

        // DNS-типы записей
        private const val DNS_TYPE_A = 1
        private const val DNS_TYPE_AAAA = 28

        // Стандартный DNS-порт
        private const val DNS_PORT = 53
    }

    /**
     * Определить, нужно ли резолвить домен локально (через обычный DNS).
     *
     * Если включён Split-DNS и домен оканчивается на один из
     * локальных суффиксов (или совпадает с доменом из списка),
     * резолвим через локальный DNS-сервер.
     *
     * @param domain Имя домена
     * @return true, если домен нужно резолвить локально
     */
    fun shouldResolveLocally(domain: String): Boolean {
        if (!splitDns) return false

        val lowerDomain = domain.lowercase()
        return localDomains.any { local ->
            when {
                local.startsWith(".") -> lowerDomain.endsWith(local)
                lowerDomain == local -> true
                lowerDomain.endsWith(".$local") -> true
                else -> false
            }
        }
    }

    /**
     * Разрезолвить доменное имя в IP-адрес.
     *
     * Если домен в списке локальных — используется обычный DNS.
     * Иначе — DNS-over-HTTPS.
     *
     * @param domain Имя домена
     * @return IP-адрес (IPv4) или null при ошибке
     */
    suspend fun resolve(domain: String): String? = withContext(Dispatchers.IO) {
        try {
            if (shouldResolveLocally(domain)) {
                resolveLocal(domain)
            } else {
                resolveDoH(domain)
            }
        } catch (e: Exception) {
            Log.w(TAG, "Ошибка резолвции $domain: ${e.message}")
            // Fallback: пробуем другой метод
            try {
                if (shouldResolveLocally(domain)) resolveDoH(domain) else resolveLocal(domain)
            } catch (e2: Exception) {
                Log.e(TAG, "Fallback-резолвция также не удалась: ${e2.message}")
                null
            }
        }
    }

    /**
     * Резолвция через DNS-over-HTTPS (RFC 8484).
     *
     * Формирует DNS-запрос в wire-формате, отправляет через HTTPS POST,
     * парсит ответ и извлекает IP-адрес.
     *
     * @param domain Имя домена
     * @return IP-адрес или null
     */
    private fun resolveDoH(domain: String): String? {
        val dnsQuery = buildDnsQuery(domain, DNS_TYPE_A)

        val url = URL(dohUrl)
        val conn = (url.openConnection() as HttpsURLConnection).apply {
            requestMethod = "POST"
            doOutput = true
            setRequestProperty("Content-Type", "application/dns-message")
            setRequestProperty("Accept", "application/dns-message")
            connectTimeout = 5000
            readTimeout = 5000
        }

        try {
            // Отправляем DNS-запрос
            conn.outputStream.use { it.write(dnsQuery) }

            // Проверяем HTTP-ответ
            val responseCode = conn.responseCode
            if (responseCode != 200) {
                Log.w(TAG, "DoH вернул код $responseCode")
                return null
            }

            // Читаем DNS-ответ
            val responseBytes = conn.inputStream.use { it.readBytes() }
            return parseDnsResponse(responseBytes)
        } finally {
            conn.disconnect()
        }
    }

    /**
     * Резолвция через локальный DNS-сервер (обычный UDP DNS).
     *
     * @param domain Имя домена
     * @return IP-адрес или null
     */
    private fun resolveLocal(domain: String): String? {
        val socket = DatagramSocket()
        socket.soTimeout = 3000

        return try {
            val query = buildDnsQuery(domain, DNS_TYPE_A)
            val serverAddress = InetAddress.getByName(localDns)
            val sendPacket = DatagramPacket(query, query.size, serverAddress, DNS_PORT)

            socket.send(sendPacket)

            val response = ByteArray(512)
            val receivePacket = DatagramPacket(response, response.size)
            socket.receive(receivePacket)

            parseDnsResponse(response.copyOfRange(0, receivePacket.length))
        } catch (e: Exception) {
            Log.w(TAG, "Локальная DNS-резолвция не удалась: ${e.message}")
            null
        } finally {
            socket.close()
        }
    }

    /**
     * Построить DNS-запрос в wire-формате (RFC 1035).
     *
     * Формирует простой A-запрос с одним вопросом.
     *
     * @param domain Имя домена
     * @param type Тип записи (A=1, AAAA=28)
     * @return Массив байт DNS-запроса
     */
    private fun buildDnsQuery(domain: String, type: Int): ByteArray {
        val parts = domain.split(".")
        val domainBytes = mutableListOf<Byte>()

        // Кодируем имя: каждая метка с префиксом длины
        for (part in parts) {
            val labelBytes = part.toByteArray(Charsets.US_ASCII)
            domainBytes.add(labelBytes.size.toByte())
            domainBytes.addAll(labelBytes.toList())
        }
        domainBytes.add(0) // Завершающий нулевой байт

        // Собираем полный DNS-пакет
        val packet = ByteBuffer.allocate(512).apply {
            // DNS-заголовок (12 байт)
            putShort(0x0001.toShort())  // ID (произвольный)
            putShort(0x0100.toShort())  // Флаги: рекурсивный запрос
            putShort(0x0001.toShort())  // Количество вопросов: 1
            putShort(0x0000.toShort())  // Количество ответов: 0
            putShort(0x0000.toShort())  // Количество авторитетных: 0
            putShort(0x0000.toShort())  // Количество дополнительных: 0

            // Секция вопросов
            for (b in domainBytes) put(b)
            putShort(type.toShort())    // Тип записи
            putShort(0x0001.toShort())  // Класс: IN
        }

        return packet.array().copyOf(packet.position())
    }

    /**
     * Разобрать DNS-ответ и извлечь первый IP-адрес.
     *
     * @param response Байты DNS-ответа
     * @return IP-адрес (строка) или null
     */
    private fun parseDnsResponse(response: ByteArray): String? {
        try {
            // Пропускаем заголовок (12 байт)
            // Пропускаем секцию вопросов
            val qdCount = ((response[4].toInt() and 0xFF) shl 8) or
                    (response[5].toInt() and 0xFF)
            var offset = 12

            // Перепрыгиваем через вопросы
            repeat(qdCount) {
                while (offset < response.size && response[offset].toInt() != 0) {
                    val labelLen = response[offset].toInt() and 0xFF
                    if (labelLen == 0) break
                    // Обрабатываем указатели (сжатие имён)
                    if ((labelLen and 0xC0) == 0xC0) {
                        offset += 2
                        break
                    }
                    offset += 1 + labelLen
                }
                if (offset < response.size) offset++ // нулевой байт
                offset += 4 // тип + класс
            }

            // Читаем ответы
            val anCount = ((response[6].toInt() and 0xFF) shl 8) or
                    (response[7].toInt() and 0xFF)

            repeat(anCount) {
                if (offset >= response.size) return@repeat

                // Пропускаем имя (может быть указателем)
                if ((response[offset].toInt() and 0xC0) == 0xC0) {
                    offset += 2 // Указатель
                } else {
                    while (offset < response.size && response[offset].toInt() != 0) {
                        val labelLen = response[offset].toInt() and 0xFF
                        offset += 1 + labelLen
                    }
                    if (offset < response.size) offset++
                }

                if (offset + 10 > response.size) return@repeat

                // Тип записи
                val rtype = ((response[offset].toInt() and 0xFF) shl 8) or
                        (response[offset + 1].toInt() and 0xFF)
                offset += 2

                // Класс
                offset += 2

                // TTL
                offset += 4

                // Длина данных
                val rdLength = ((response[offset].toInt() and 0xFF) shl 8) or
                        (response[offset + 1].toInt() and 0xFF)
                offset += 2

                if (offset + rdLength > response.size) return@repeat

                // Если это A-запись — извлекаем IPv4
                if (rtype == DNS_TYPE_A && rdLength == 4) {
                    val ip = "${response[offset].toInt() and 0xFF}." +
                            "${response[offset + 1].toInt() and 0xFF}." +
                            "${response[offset + 2].toInt() and 0xFF}." +
                            "${response[offset + 3].toInt() and 0xFF}"
                    return ip
                }

                // Если это AAAA-запись — извлекаем IPv6 (в будущем)
                // if (rtype == DNS_TYPE_AAAA && rdLength == 16) { ... }

                offset += rdLength
            }
        } catch (e: Exception) {
            Log.w(TAG, "Ошибка парсинга DNS-ответа: ${e.message}")
        }
        return null
    }

    // Вспомогательный класс для работы с байтами
    private inner class ByteBuffer(private val array: ByteArray) {
        var position = 0
        fun put(b: Byte) { array[position++] = b }
        fun putShort(s: Short) {
            array[position++] = (s.toInt() shr 8).toByte()
            array[position++] = s.toByte()
        }
    }
}