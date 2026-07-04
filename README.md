# BypassVPN

**BypassVPN** — комплексное решение для обхода цензуры и блокировок в России. Включает серверную часть (РФ + ЕС узлы) и кроссплатформенные клиенты (Windows, Android). Обеспечивает доступ к заблокированным по DPI доменам (YouTube, Instagram и др.) и географически ограниченным сервисам (ChatGPT, Claude и др.) через шифрованные туннели с автоматическим выбором протокола.

---

## Архитектура

```
                    ┌──────────────────────────────────────────────────────────┐
                    │                     РОССИЙСКАЯ ФЕДЕРАЦИЯ                  │
                    │                                                          │
    ┌──────────┐    │    ┌─────────────────────────────────────────────────┐   │
    │          │    │    │              РФ-Сервер (bypass-server)           │   │
    │  Клиент  │────┼───>│                                                   │   │
    │ (Win/    │    │    │  ┌─────────┐  ┌──────────┐  ┌───────────────┐   │   │
    │  Android)│    │    │  │  WDTT   │  │  Zapret  │  │ VLESS+Reality │   │   │
    │          │    │    │  │ DTLS:   │  │  nfqws   │  │    Client     │   │   │
    └──────────┘    │    │  │ :56000  │  │ DPI      │  │  (Xray-core)  │   │   │
                    │    │  └────┬────┘  │ bypass   │  └───────┬───────┘   │   │
                    │    │       │       └──────────┘          │           │   │
                    │    │  ┌────▼──────────────────────┐      │           │   │
                    │    │  │    WireGuard (userspace)   │      │           │   │
                    │    │  │    10.66.0.0/16            │      │           │   │
                    │    │  └───────────────────────────┘      │           │   │
                    │    │  ┌──────────────────────────────┐   │           │   │
                    │    │  │  DNS (split: DPI / Geo /     │   │           │   │
                    │    │  │  Direct) + Fake-IP кэш      │   │           │   │
                    │    │  └──────────────────────────────┘   │           │   │
                    │    └──────────────────────────────────────┼───────────┘   │
                    │                                           │               │
                    └───────────────────────────────────────────┼───────────────┘
                                                                │
                                     VLESS+Reality (Xray-core)  │ TLS camouflaged
                                     (XTLS Vision, Reality TLS) │
                                                                ▼
                    ┌──────────────────────────────────────────────────────────┐
                    │                     ЕВРОПЕЙСКИЙ СОЮЗ                      │
                    │                                                          │
                    │    ┌─────────────────────────────────────────────────┐   │
                    │    │          ЕС-Сервер (Xray-core)                   │   │
                    │    │                                                   │   │
                    │    │  VLESS+Reality Server :443                       │   │
                    │    │  (камуфляж под www.microsoft.com)                │   │
                    │    │                                                   │   │
                    │    └───────────────────────┬─────────────────────────┘   │
                    │                            │                               │
                    └────────────────────────────┼───────────────────────────────┘
                                                 │
                                                 ▼
                                            ┌─────────┐
                                            │ Internet │
                                            └─────────┘
```

### 6 путей трафика

| # | Путь | Описание |
|---|------|----------|
| 1 | **Клиент → WDTT → WireGuard → Zapret → DPI-сайт** | Заблокированные по DPI (YouTube, Instagram). Обход через nfqws с подменой SNI. Трафик идёт напрямую из РФ. |
| 2 | **Клиент → WDTT → WireGuard → VLESS → ЕС → Geo-сайт** | Географически заблокированные (ChatGPT, Claude). Трафик через VLESS-туннель к ЕС-серверу. |
| 3 | **Клиент → WDTT → WireGuard → Прямой сайт** | Обычные российские и незаблокированные сайты. Прямой выход в интернет. |
| 4 | **Клиент → VLESS → ЕС → Любой сайт** | Прямое VLESS-подключение к ЕС-серверу (если провайдер не блокирует). Весь трафик через ЕС. |
| 5 | **Клиент → VLESS → ЕС → DPI-сайт** | Альтернативный путь для DPI-заблокированных, если Zapret не справляется. |
| 6 | **Клиент → Auto** | Автоматический выбор: TCP-ping google.com:443. Доступен → VLESS (#4). Заблокирован → WDTT (#1-3). |

---

## Возможности

- **WDTT (WireGuard over DTLS через VK TURN)** — проприетарный протокол инкапсуляции WireGuard в DTLS с обфускацией RTP (ChaCha20-Poly1305 WRAP). Работает даже при глубоком DPI и блокировке VPN.
- **VLESS+Reality** — протокол на базе Xray-core с TLS-камуфляжем под легитимный сайт (по умолчанию www.microsoft.com). Невозможно отличить от обычного HTTPS-трафика.
- **Zapret DPI bypass** — интеграция с [bol-van/zapret](https://github.com/bol-van/zapret) (nfqws) для обхода DPI-блокировок по сигнатурам (TLS SNI, HTTP Host).
- **Split tunneling** — автоматическое разделение трафика: DPI-домены → Zapret, Geo-домены → VLESS-туннель, остальные → напрямую.
- **Fake-IP DNS** — локальный DNS-резолвер с кэшем и split-DNS маршрутизацией запросов.
- **Kill switch** — блокировка всего трафика при разрыве туннеля (nftables/iptables на Linux, WFP/netsh на Windows).
- **Multi-user** — управление пользователями через API (добавление, удаление, сроки действия, привязка устройств).
- **Кроссплатформенные клиенты** — Windows (Wails v2 + React) и Android (Jetpack Compose).

---

## Структура проекта

```
bypass-app/
├── server/                        # Серверная часть (Go)
│   ├── cmd/server/main.go         # Точка входа сервера
│   ├── configs/
│   │   ├── server.yaml.example    # Пример конфигурации
│   │   ├── users.json.example     # Пример файла пользователей
│   │   ├── dpi_blocklist.txt      # DPI-блоклист (YouTube, Instagram...)
│   │   └── geo_blocklist.txt      # Geo-блоклист (ChatGPT, Claude...)
│   ├── internal/
│   │   ├── api/server.go          # HTTP API (chi router)
│   │   ├── config/config.go       # Загрузка YAML конфига
│   │   ├── config/types.go        # Типы конфигурации
│   │   ├── dns/resolver.go        # DNS-резолвер с split-DNS
│   │   ├── lists/manager.go       # Менеджер блоклистов
│   │   ├── router/manager.go      # IP-маршрутизация (nftables/iptables)
│   │   ├── users/manager.go       # Менеджер пользователей
│   │   ├── vless/manager.go       # Управление Xray-core (VLESS+Reality)
│   │   ├── wdtt/listener.go       # WDTT DTLS-терминация + WireGuard
│   │   └── zapret/manager.go      # Управление Zapret nfqws
│   └── go.mod
│
├── client-core/                   # Общая логика клиентов (Go)
│   ├── auth/client.go             # HTTP-аутентификация на сервере
│   ├── dns/resolver.go            # DoH клиент + split-DNS
│   ├── killswitch/manager.go      # Kill switch (Linux/Windows)
│   ├── routing/lists.go           # Менеджер доменных списков
│   ├── tunnel/manager.go          # Оркестратор туннеля (WDTT/VLESS/Auto)
│   └── go.mod
│
├── client-windows/                # Windows-клиент (Wails v2 + React/TS)
│   ├── main.go                    # Точка входа Wails
│   ├── wails.json                 # Конфигурация Wails
│   ├── backend/
│   │   ├── app.go                 # Wails-биндинги для фронтенда
│   │   └── tunnel.go              # Оркестратор туннеля (Windows)
│   ├── frontend/                  # React/TypeScript UI
│   │   ├── src/
│   │   │   ├── pages/             # Экраны (Connect, Logs, Settings)
│   │   │   ├── components/        # UI-компоненты (Layout)
│   │   │   └── lib/stores/        # Состояние (tunnel, logs, toast)
│   │   ├── package.json
│   │   └── vite.config.ts
│   └── go.mod
│
├── client-android/                # Android-клиент (Kotlin + Jetpack Compose)
│   ├── app/src/main/java/com/bypass/vpn/
│   │   ├── MainActivity.kt        # Главная активность
│   │   ├── BypassApplication.kt   # Класс приложения
│   │   ├── model/                 # Модели данных
│   │   ├── data/                  # Хранилища (Settings, Profiles)
│   │   ├── service/               # VpnService + TunnelManager
│   │   ├── util/                  # DNS, Ping хелперы
│   │   └── ui/                    # Jetpack Compose UI
│   │       ├── theme/             # Тёмная Material3 тема
│   │       ├── navigation/        # Навигация
│   │       └── screens/           # Экраны (Connect, Profiles, Logs, Settings)
│   ├── build.gradle.kts
│   └── settings.gradle.kts
│
├── .github/workflows/             # CI/CD (GitHub Actions)
│   ├── build-server.yml           # Сборка Linux-бинарника сервера
│   ├── build-windows.yml          # Сборка Windows EXE (Wails)
│   └── build-android.yml          # Сборка Android APK
│
├── deploy_rf.sh                   # Скрипт развёртывания РФ-сервера
├── deploy_eu.sh                   # Скрипт развёртывания ЕС-сервера
├── go.work                        # Go workspace
├── .gitignore
└── README.md                      # Этот файл
```

---

## Быстрый старт

### Шаг 1. Развёртывание ЕС-сервера (Европа)

Сервер в ЕС терминирует VLESS+Reality туннель и выпускает трафик в интернет.

```bash
# 1. Загрузите скрипт на ЕС-сервер (Ubuntu 22.04/24.04 или Debian 12+)
scp deploy_eu.sh root@eu-server:/root/

# 2. Запустите от root
ssh root@eu-server
bash deploy_eu.sh

# 3. Скрипт автоматически:
#    - Установит Xray-core (последняя версия)
#    - Сгенерирует Reality ключи (x25519)
#    - Сгенерирует VLESS UUID
#    - Создаст Xray конфиг (VLESS+Reality :443)
#    - Настроит systemd (xray-bypass.service)
#    - Откроет порт 443 в firewall

# 4. Запишите выведенные параметры — они понадобятся для РФ-сервера:
#    - Public IP
#    - VLESS UUID
#    - Reality Public Key
#    - Reality Short ID
#    - SNI (по умолчанию www.microsoft.com)

# 5. Запустите
systemctl start xray-bypass
systemctl status xray-bypass
```

### Шаг 2. Развёртывание РФ-сервера (Россия)

РФ-сервер терминирует WDTT-подключения клиентов, запускает Zapret для DPI-обхода и перенаправляет geo-трафик через VLESS-туннель к ЕС.

```bash
# 1. Загрузите скрипт на РФ-сервер
scp deploy_rf.sh root@rf-server:/root/

# 2. Запустите от root
ssh root@rf-server
bash deploy_rf.sh

# 3. Скрипт автоматически:
#    - Установит WireGuard, Zapret (nfqws), Xray-core
#    - Включит IP forwarding и BBR
#    - Сгенерирует WireGuard ключи
#    - Создаст конфиг /opt/bypass-server/configs/server.yaml
#    - Настроит systemd (bypass-server.service)
#    - Откроет порты 56000 (WDTT), 8080 (API)

# 4. Отредактируйте конфиг
nano /opt/bypass-server/configs/server.yaml
# Обязательные поля для редактирования:
#   wdtt.password           — надёжный пароль WDTT
#   vless.client.remote_addr — IP:порт ЕС-сервера
#   vless.client.uuid       — VLESS UUID от ЕС-сервера
#   vless.client.public_key — Reality public key от ЕС-сервера
#   vless.client.short_id   — Reality short ID от ЕС-сервера
#   api.auth_token          — токен для административного API

# 5. Добавьте пользователей
nano /opt/bypass-server/configs/users.json
# Формат: массив объектов {id, password, vless_uuid, active, expires, ...}

# 6. Запустите
systemctl start bypass-server
journalctl -u bypass-server -f
```

### Шаг 3. Сборка Windows-клиента

**Локально (требуется Windows):**

```bash
# Установите зависимости:
#   - Go 1.23+
#   - Node.js 22+
#   - Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest

cd client-windows

# Установка frontend-зависимостей
cd frontend && npm install && cd ..

# Сборка
wails build -platform windows/amd64

# Результат: build/bin/bypass-vpn.exe
```

**Через GitHub Actions:**

1. Загрузите код в GitHub-репозиторий
2. Перейдите в **Actions → Сборка Windows**
3. Нажмите **Run workflow** (или сделайте push в main)
4. Скачайте артефакт `bypass-vpn-windows` (EXE-файл)

### Шаг 4. Сборка Android-клиента

**Локально:**

```bash
# Требуется: Android Studio, JDK 17, NDK 26.1, Go 1.23+ (для gomobile)

# 1. Сборка Go-библиотеки через gomobile
gomobile init
cd client-core
gomobile bind -target=android -o ../client-android/app/libs/bypass-lib.aar .

# 2. Сборка APK
cd ../client-android
chmod +x gradlew
./gradlew assembleDebug

# Результат: app/build/outputs/apk/debug/app-debug.apk
```

**Через GitHub Actions:**

1. Загрузите код в GitHub-репозиторий
2. Перейдите в **Actions → Сборка Android**
3. Нажмите **Run workflow**
4. Скачайте артефакт `bypass-vpn-android` (APK-файлы)

> **Примечание:** для полной сборки с Go-бэкендом через CI необходимо добавить gomobile-шаг в workflow и настроить secrets для подписи.

---

## Конфигурация сервера

Файл: `/opt/bypass-server/configs/server.yaml`

### Ключевые поля

| Поле | Описание | Пример |
|------|----------|--------|
| `mode` | Режим работы: `rf` (Россия) или `eu` (Европа) | `rf` |
| `listen.api_addr` | Адрес HTTP API | `:8080` |
| `listen.wdtt_dtls_addr` | UDP-порт WDTT DTLS | `0.0.0.0:56000` |
| `listen.socks5_addr` | Локальный SOCKS5 для geo-трафика | `127.0.0.1:1080` |
| `wireguard.interface_name` | Имя TUN-интерфейса | `bypass0` |
| `wireguard.private_key` | Приватный ключ WG (base64) | `WGaB1c2...` |
| `wireguard.subnet` | Подсеть клиентов | `10.66.0.0/16` |
| `wireguard.server_ip` | IP сервера в подсети | `10.66.66.1` |
| `wireguard.mtu` | MTU (max 1280 для DTLS) | `1280` |
| `wireguard.internal_port` | Внутренний WG UDP-порт | `56001` |
| `wdtt.enabled` | Включить WDTT терминацию | `true` |
| `wdtt.password` | Пароль WDTT (HKDF вывод ключа) | `your-secure-password` |
| `vless.role` | `client` (РФ) или `server` (ЕС) | `client` |
| `vless.client.remote_addr` | Адрес ЕС-сервера | `1.2.3.4:443` |
| `vless.client.uuid` | VLESS UUID | `a1b2c3d4-...` |
| `vless.client.public_key` | Reality публичный ключ ЕС | `xxxx...` |
| `vless.client.fingerprint` | TLS fingerprint | `chrome` |
| `zapret.enabled` | Включить Zapret nfqws | `true` |
| `zapret.nfqws_path` | Путь к nfqws | `/usr/bin/nfqws` |
| `zapret.nfqws_args` | Аргументы nfqws | `--warp-crypto=auto --host-another=sni.yandex.net --host-pfx=2` |
| `zapret.qnum` | Номер NFQUEUE | `200` |
| `dns.enabled` | Включить DNS-резолвер | `true` |
| `dns.upstream` | Upstream DNS серверы | `["1.1.1.1", "8.8.8.8"]` |
| `dns.cache_ttl` | TTL кэша (секунды) | `300` |
| `api.enabled` | Включить HTTP API | `true` |
| `api.auth_token` | Токен администратора | `your-api-token` |
| `users_file` | Путь к файлу пользователей | `configs/users.json` |
| `lists.dpi_blocklist` | Путь к DPI-блоклисту | `configs/dpi_blocklist.txt` |
| `lists.geo_blocklist` | Путь к Geo-блоклисту | `configs/geo_blocklist.txt` |
| `log_level` | Уровень логирования | `info` |

---

## Управление пользователями

### Формат файла пользователей

Файл: `/opt/bypass-server/configs/users.json`

```json
[
  {
    "id": "ivan",
    "password": "MySecretPassword123",
    "device_id": "",
    "vless_uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "wg_public_key": "",
    "wg_ip": "",
    "active": true,
    "created": "2025-01-15",
    "expires": "2025-12-31",
    "description": "Иван, личный телефон",
    "traffic_used_mb": 0,
    "last_seen": ""
  }
]
```

### Добавление пользователя

**Через API:**

```bash
TOKEN="your-api-token"
SERVER="http://rf-server:8080"

curl -X POST "$SERVER/api/users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "newuser",
    "password": "SecurePassword456",
    "active": true,
    "expires": "2026-01-01",
    "description": "Новый пользователь"
  }'
```

**Вручную:**

```bash
nano /opt/bypass-server/configs/users.json
# Добавьте объект в массив и перезагрузите:
curl -X POST "$SERVER/api/reload" -H "Authorization: Bearer $TOKEN"
# Или: systemctl restart bypass-server
```

### Удаление пользователя

```bash
curl -X DELETE "$SERVER/api/users/newuser" \
  -H "Authorization: Bearer $TOKEN"
```

### Обновление пользователя

```bash
curl -X PUT "$SERVER/api/users/ivan" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"active": false, "expires": "2025-06-01"}'
```

---

## Списки блокировок

### dpi_blocklist.txt

Домены, блокируемые РКН по DPI-сигнатурам (TLS SNI, HTTP Host). Трафик к этим доменам проходит через Zapret nfqws для обхода DPI.

**Формат:** по одному домену на строку. Пустые строки и строки с `#` игнорируются.

```
# Видео
youtube.com
www.youtube.com
youtu.be
m.youtube.com

# Соцсети
instagram.com
www.instagram.com
facebook.com
x.com
discord.com

# Стриминг
twitch.tv
netflix.com
tiktok.com
```

### geo_blocklist.txt

Домены сервисов, географически недоступных в РФ. Запросы к этим доменам направляются через VLESS+Reality туннель к ЕС-серверу.

**Формат:** аналогичен dpi_blocklist.txt.

```
# AI-сервисы
chatgpt.com
openai.com
claude.ai
anthropic.com
gemini.google.com
midjourney.com
huggingface.co
```

### Перезагрузка списков без перезапуска

```bash
# Через API
curl -X POST "http://rf-server:8080/api/lists/update" \
  -H "Authorization: Bearer $TOKEN"

# Через SIGHUP
kill -HUP $(pidof bypass-server)
```

---

## Сборка из исходников

### Требования

- **Go 1.23+**
- **Node.js 22+** (для Windows-клиента)
- **JDK 17** + **Android SDK** + **NDK 26.1** (для Android-клиента)
- **Wails CLI v2** (для Windows-клиента)
- **mingw-w64** (для кросс-компиляции Windows на Linux)

### Сервер (Linux amd64)

```bash
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o bypass-server ./cmd/server/
```

### Windows-клиент (с Windows-хоста)

```bash
# Установка Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Сборка frontend
cd client-windows/frontend
npm install
npm run build
cd ../..

# Сборка Wails-приложения
cd client-windows
wails build -platform windows/amd64
# Результат: build/bin/bypass-vpn.exe
```

### Windows-клиент (кросс-компиляция с Linux)

```bash
# Установить mingw-w64
sudo apt-get install -y mingw-w64

# Установка Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Кросс-компиляция
cd client-windows
wails build -platform windows/amd64
```

### Android-клиент

```bash
# 1. Сборка Go-библиотеки для Android
cd client-core
gomobile init
gomobile bind -target=android -o ../client-android/app/libs/bypass-lib.aar .

# 2. Сборка APK
cd ../client-android
chmod +x gradlew
./gradlew assembleDebug
# Результат: app/build/outputs/apk/debug/app-debug.apk

# 3. Release APK (требуется keystore)
./gradlew assembleRelease
```

---

## CI/CD (GitHub Actions)

Проект содержит 3 workflow в `.github/workflows/`:

### build-server.yml — Сборка сервера

- **Триггеры:** push в `server/**`, тег `v*`, ручной запуск
- **Среда:** ubuntu-latest, Go 1.23
- **Действие:** статическая компиляция `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`
- **Артефакт:** `bypass-server-linux-amd64` (Linux бинарник, 30 дней)

### build-windows.yml — Сборка Windows-клиента

- **Триггеры:** push в `client-windows/**` или `client-core/**`, тег `v*`, ручной запуск
- **Среда:** ubuntu-latest, Go 1.23, Node.js 22, Wails CLI, mingw-w64
- **Действие:** `npm ci` → `wails build -platform windows/amd64`
- **Артефакт:** `bypass-vpn-windows` (EXE-файл, 30 дней)

### build-android.yml — Сборка Android-клиента

- **Триггеры:** push в `client-android/**`, тег `v*`, ручной запуск
- **Среда:** ubuntu-latest, JDK 17, Android SDK, NDK 26.1
- **Действие:** `./gradlew assembleDebug` + `assembleRelease`
- **Артефакты:** `bypass-vpn-android` (debug, 30 дней), `bypass-vpn-android-release` (release, 90 дней)

### Настройка

1. Загрузите проект в GitHub-репозиторий
2. Для release-сборки Android добавьте secrets: `KEYSTORE_PASSWORD`, `KEY_ALIAS`, `KEY_PASSWORD`
3. Перейдите в **Actions** и запустите нужный workflow вручную, либо сделайте push в соответствующие директории

---

## API Reference

### Публичные эндпоинты (без токена)

| Метод | Путь | Описание |
|-------|------|----------|
| `POST` | `/api/auth` | Аутентификация клиента. Body: `{"password": "...", "device_id": "..."}`. Возвращает конфигурацию подключения |
| `GET` | `/api/lists` | Получение текущих DPI и Geo списков доменов |
| `GET` | `/api/health` | Проверка здоровья сервера (статус, количество пользователей, записей в списках) |

### Административные эндпоинты (требуют `Authorization: Bearer <token>`)

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/api/users` | Список всех пользователей |
| `POST` | `/api/users` | Добавление пользователя. Body: JSON-объект пользователя |
| `PUT` | `/api/users/{id}` | Обновление пользователя (пароль, active, expires и др.) |
| `DELETE` | `/api/users/{id}` | Удаление пользователя |
| `POST` | `/api/lists/update` | Перезагрузка списков блокировок из файлов |
| `POST` | `/api/reload` | Полная перезагрузка конфигурации (аналог SIGHUP) |

### Ошибки аутентификации (`/api/auth`)

| Код | Ответ | Описание |
|-----|-------|----------|
| 403 | `DENIED:wrong_password` | Неверный пароль |
| 403 | `DENIED:expired` | Срок действия истёк |
| 403 | `DENIED:device_mismatch` | Привязка к другому устройству |
| 403 | `DENIED:unknown` | Неизвестная ошибка (пользователь не найден, неактивен) |

---

## Безопасность

- **Шифрование:** весь трафик между клиентом и РФ-сервером шифруется WireGuard (ChaCha20-Poly1305). Трафик между РФ и ЕС серверами — VLESS+Reality (XTLS Vision + TLS 1.3 с камуфляжем).
- **Обфускация WDTT:** RTP-фреймы с PT=111 дополнительно шифруются ChaCha20-Poly1305 (WRAP). DTLS обеспечивает защиту от прослушивания и модификации.
- **Reality TLS:** VLESS-трафик неотличим от обычного HTTPS к легитимному сайту (по умолчанию www.microsoft.com). Имитирует TLS-рукопожатие Chrome.
- **Аутентификация:** каждый клиент аутентифицируется по паролю. Поддерживается привязка к device_id и сроки действия (expires).
- **Административный API:** защищён Bearer-токеном. Передаётся через заголовок `Authorization: Bearer <token>` или query-параметр `?token=`.
- **Нет логов:** сервер не логирует содержимое трафика, DNS-запросы или URL. Логируются только события авторизации и ошибки.
- **Kill switch:** при разрыве туннеля весь трафик блокируется, предотвращая утечки.
- **DNS-защита:** клиент блокирует DNS-запросы вне туннеля, предотвращая DNS-утечки к провайдеру.

---

## Лицензия

GNU General Public License v3.0 (GPL-3.0)

Этот проект является свободным программным обеспечением: вы можете распространять и/или модифицировать его на условиях GNU General Public License, опубликованной Free Software Foundation (версия 3 или, на ваше усмотрение, любой более поздней версии).