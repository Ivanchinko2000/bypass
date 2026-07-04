#!/bin/bash
# ============================================================
# deploy_rf.sh — Развертывание РФ-сервера (WDTT + WireGuard + Zapret)
# Для Ubuntu 22.04/24.04 и Debian 12+
# ============================================================
set -euo pipefail

# Цвета для вывода
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_step()  { echo -e "${CYAN}[STEP]${NC}  $*"; }

# ============================================================
# 1. Проверка ОС
# ============================================================
log_step "Проверка операционной системы..."

if [ ! -f /etc/os-release ]; then
    log_error "Не удалось определить ОС (/etc/os-release не найден)"
    exit 1
fi

. /etc/os-release

case "$ID" in
    ubuntu|debian)
        log_info "ОС: $PRETTY_NAME — поддерживается"
        ;;
    *)
        log_error "Неподдерживаемая ОС: $PRETTY_NAME"
        log_error "Скрипт рассчитан на Ubuntu 22.04/24.04 или Debian 12+"
        exit 1
        ;;
esac

# Проверяем root
if [ "$(id -u)" -ne 0 ]; then
    log_error "Скрипт должен запускаться от root"
    exit 1
fi

# ============================================================
# 2. Обновление пакетов и установка зависимостей
# ============================================================
log_step "Обновление пакетов..."
apt-get update -qq

log_step "Установка базовых зависимостей..."
apt-get install -y -qq \
    curl \
    wget \
    gnupg2 \
    ca-certificates \
    lsb-release \
    iproute2 \
    iptables \
    nftables \
    iputils-ping \
    dnsutils \
    jq \
    unzip \
    make \
    gcc \
    git \
    systemctl

# ============================================================
# 3. Установка WireGuard
# ============================================================
log_step "Установка WireGuard..."
if command -v wg &>/dev/null; then
    log_info "WireGuard уже установлен: $(wg --version 2>/dev/null | head -1)"
else
    apt-get install -y -qq wireguard wireguard-tools
    log_info "WireGuard установлен"
fi

# Проверяем модуль ядра
if modprobe wireguard 2>/dev/null; then
    log_info "Модуль ядра wireguard загружен"
else
    log_warn "Модуль ядра wireguard не найден — используем userspace WireGuard (для WDTT этого достаточно)"
fi

# ============================================================
# 4. Включение IP forwarding
# ============================================================
log_step "Настройка IP forwarding..."
if ! grep -q "^net.ipv4.ip_forward.*1" /etc/sysctl.conf 2>/dev/null; then
    echo "net.ipv4.ip_forward = 1" >> /etc/sysctl.conf
fi
echo 1 > /proc/sys/net/ipv4/ip_forward
sysctl -p -q 2>/dev/null || true
log_info "IP forwarding включён"

# ============================================================
# 5. Оптимизация сетевого стека (BBR)
# ============================================================
log_step "Оптимизация TCP (BBR)..."
cat >> /etc/sysctl.conf <<'SYSCTL'

# Оптимизация для VPN/прокси
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 25165824
net.core.wmem_max = 25165824
net.ipv4.tcp_rmem = 4096 87380 25165824
net.ipv4.tcp_wmem = 4096 65536 25165824
net.core.netdev_max_backlog = 5000
SYSCTL

sysctl -p -q 2>/dev/null || true
log_info "TCP BBR и буферы настроены"

# ============================================================
# 6. Установка Zapret (обход DPI)
# ============================================================
log_step "Установка Zapret..."

ZAPRET_DIR="/opt/zapret"
ZAPRET_NFQWS="/usr/bin/nfqws"

if [ -x "$ZAPRET_NFQWS" ]; then
    log_info "Zapret nfqws уже установлен: $ZAPRET_NFQWS"
else
    log_info "Загрузка Zapret из repo.zapret.info..."

    # Определяем архитектуру
    ARCH=$(dpkg --print-architecture)
    log_info "Архитектура: $ARCH"

    # Скачиваем и устанавливаем
    mkdir -p "$ZAPRET_DIR"
    cd "$ZAPRET_DIR"

    # Клонируем репозиторий
    if [ ! -d "$ZAPRET_DIR/.git" ]; then
        git clone https://github.com/bol-van/zapret.git "$ZAPRET_DIR" 2>/dev/null || {
            log_warn "git clone не удался, пробуем через zip..."
            cd /tmp
            wget -q "https://github.com/bol-van/zapret/archive/refs/heads/master.zip" -O zapret.zip
            unzip -qo zapret.zip
            rm -rf "$ZAPRET_DIR"
            mv /tmp/zapret-master "$ZAPRET_DIR"
            rm -f /tmp/zapret.zip
        }
    fi

    # Собираем nfqws
    cd "$ZAPRET_DIR"
    make -j$(nproc) nfqws 2>&1 | tail -5

    if [ -f "$ZAPRET_DIR/nfqws/nfqws" ]; then
        cp -f "$ZAPRET_DIR/nfqws/nfqws" "$ZAPRET_NFQWS"
        chmod +x "$ZAPRET_NFQWS"
        log_info "Zapret nfqws установлен: $ZAPRET_NFQWS"
    else
        log_error "Сборка nfqws не удалась. Установите вручную."
        log_error "Смотрите: https://github.com/bol-van/zapret"
    fi
fi

# ============================================================
# 7. Установка Xray-core
# ============================================================
log_step "Установка Xray-core..."

XRAY_PATH="/usr/local/bin/xray-core"

if [ -x "$XRAY_PATH" ]; then
    log_info "Xray-core уже установлен: $XRAY_PATH"
    $XRAY_PATH version 2>/dev/null | head -1 || true
else
    log_info "Загрузка Xray-core..."

    # Определяем архитектуру для Xray
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)  XRAY_ARCH="amd64" ;;
        aarch64) XRAY_ARCH="arm64-v8a" ;;
        *)       XRAY_ARCH="amd64" ;;
    esac

    # Получаем последнюю версию
    XRAY_VERSION=$(curl -sL "https://github.com/XTLS/Xray-core/releases/latest" -o /dev/null -w '%{url_effective}' 2>/dev/null | sed 's|.*/||')
    if [ -z "$XRAY_VERSION" ]; then
        XRAY_VERSION="v25.1.5"
        log_warn "Не удалось определить последнюю версию, используем $XRAY_VERSION"
    fi

    XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/Xray-linux-${XRAY_ARCH}.zip"
    log_info "Скачивание: $XRAY_URL"

    cd /tmp
    wget -q "$XRAY_URL" -O xray-core.zip
    unzip -qo xray-core.zip xray
    mv xray "$XRAY_PATH"
    chmod +x "$XRAY_PATH"
    rm -f xray-core.zip

    log_info "Xray-core $XRAY_VERSION установлен: $XRAY_PATH"
    $XRAY_PATH version 2>/dev/null | head -1 || true
fi

# ============================================================
# 8. Генерация WireGuard ключей (если не существуют)
# ============================================================
log_step "Проверка WireGuard ключей..."

WG_KEYS_DIR="/etc/bypass-server"
WG_KEYS_FILE="$WG_KEYS_DIR/wg-keys.dat"
WG_PRIV_KEY=""
WG_PUB_KEY=""

mkdir -p "$WG_KEYS_DIR"

if [ -f "$WG_KEYS_FILE" ]; then
    log_info "Ключи WireGuard найдены: $WG_KEYS_FILE"
    WG_PRIV_KEY=$(head -1 "$WG_KEYS_FILE")
    WG_PUB_KEY=$(head -2 "$WG_KEYS_FILE" | tail -1)
    log_info "Публичный ключ: ${WG_PUB_KEY:0:10}..."
else
    log_info "Генерация WireGuard ключей..."

    if command -v wg &>/dev/null; then
        # Используем системный wg genkey
        WG_PRIV_KEY=$(wg genkey)
        WG_PUB_KEY=$(echo "$WG_PRIV_KEY" | wg pubkey)
    else
        # Fallback: генерируем через openssl
        WG_PRIV_KEY=$(openssl rand -base64 32)
        # Для реального использования нужен Curve25519 —.warn и просим установить wireguard
        log_warn "wg genkey недоступен. Сгенерирован ключ через openssl (для продакшена установите wireguard-tools)"
        WG_PUB_KEY="GENERATE_WITH_wg_pubkey"
    fi

    cat > "$WG_KEYS_FILE" <<EOF
$WG_PRIV_KEY
$WG_PUB_KEY
EOF
    chmod 600 "$WG_KEYS_FILE"

    log_info "Ключи сохранены: $WG_KEYS_FILE"
    log_info "Публичный ключ: $WG_PUB_KEY"
fi

# ============================================================
# 9. Создание конфигурационных файлов
# ============================================================
log_step "Создание конфигурационных файлов..."

APP_DIR="/opt/bypass-server"
CONFIG_DIR="$APP_DIR/configs"

mkdir -p "$APP_DIR"
mkdir -p "$CONFIG_DIR"

# Копируем пример конфига если нет рабочего
if [ ! -f "$CONFIG_DIR/server.yaml" ]; then
    if [ -f "$(dirname "$0")/server/configs/server.yaml.example" ]; then
        cp "$(dirname "$0")/server/configs/server.yaml.example" "$CONFIG_DIR/server.yaml"
    else
        cat > "$CONFIG_DIR/server.yaml" <<'YAML'
mode: rf
listen:
  api_addr: ":8080"
  dns_addr: ":53"
  wdtt_dtls_addr: "0.0.0.0:56000"
  socks5_addr: "127.0.0.1:1080"
wireguard:
  interface_name: "bypass0"
  private_key: "ВАШ_КЛЮЧ"
  subnet: "10.66.0.0/16"
  server_ip: "10.66.66.1"
  mtu: 1280
  keepalive: 25
  internal_port: 56001
wdtt:
  enabled: true
  dtls_addr: "0.0.0.0:56000"
  password: "СГЕНЕРИРУЙТЕ_ПАРОЛЬ"
vless:
  role: "client"
  server:
    listen: ":443"
    private_key: ""
    short_id: "abcd1234"
    dest: "www.microsoft.com:443"
    server_names:
      - "www.microsoft.com"
  client:
    enabled: true
    remote_addr: "1.2.3.4:443"
    uuid: "UUID_ВСТАВЬТЕ"
    public_key: "ПУБЛИЧНЫЙ_КЛЮЧ"
    short_id: "abcd1234"
    fingerprint: "chrome"
    server_name: "www.microsoft.com"
    xray_path: "/usr/local/bin/xray-core"
    xray_config_path: "configs/xray_config.json"
zapret:
  enabled: true
  nfqws_path: "/usr/bin/nfqws"
  nfqws_args: "--warp-crypto=auto --host-another=sni.yandex.net --host-pfx=2"
  qnum: 200
dns:
  enabled: true
  listen: ":53"
  upstream:
    - "1.1.1.1"
    - "8.8.8.8"
  eu_upstream:
    - "1.1.1.1"
  enable_doh: false
  cache_ttl: 300
api:
  enabled: true
  listen: ":8080"
  auth_token: "СГЕНЕРИРУЙТЕ_ТОКЕН"
users_file: "configs/users.json"
lists:
  dpi_blocklist: "configs/dpi_blocklist.txt"
  geo_blocklist: "configs/geo_blocklist.txt"
log_level: "info"
log_file: ""
YAML
    fi

    # Подставляем сгенерированный приватный ключ
    if [ -n "$WG_PRIV_KEY" ] && command -v sed &>/dev/null; then
        sed -i "s|private_key:.*|private_key: \"$WG_PRIV_KEY\"|" "$CONFIG_DIR/server.yaml"
    fi

    log_info "Конфиг создан: $CONFIG_DIR/server.yaml"
    log_warn "!!! Отредактируйте $CONFIG_DIR/server.yaml перед запуском !!!"
else
    log_info "Конфиг уже существует: $CONFIG_DIR/server.yaml"
fi

# Копируем блоклисты если нет
for f in dpi_blocklist.txt geo_blocklist.txt; do
    if [ ! -f "$CONFIG_DIR/$f" ]; then
        if [ -f "$(dirname "$0")/server/configs/$f" ]; then
            cp "$(dirname "$0")/server/configs/$f" "$CONFIG_DIR/$f"
        fi
    fi
done

# Создаём пустой users.json если нет
if [ ! -f "$CONFIG_DIR/users.json" ]; then
    echo '[]' > "$CONFIG_DIR/users.json"
    log_info "Создан пустой users.json: $CONFIG_DIR/users.json"
fi

# ============================================================
# 10. Сборка и установка bypass-server
# ============================================================
log_step "Сборка bypass-server..."

BINARY_PATH="/usr/local/bin/bypass-server"

# Проверяем исходники
SOURCE_DIR="$(dirname "$0")/server"
if [ -d "$SOURCE_DIR" ]; then
    cd "$SOURCE_DIR"
    if command -v go &>/dev/null; then
        log_info "Сборка из исходников..."
        CGO_ENABLED=0 go build -o "$BINARY_PATH" -ldflags="-s -w" ./cmd/server/
        chmod +x "$BINARY_PATH"
        log_info "Бинарник собран: $BINARY_PATH"
    else
        log_warn "Go не установлен. Пропуск сборки."
        log_warn "Установите Go: apt-get install -y golang-go"
        log_warn "Или скопируйте бинарник вручную в $BINARY_PATH"
    fi
elif [ -f "$(dirname "$0")/bypass-server" ]; then
    cp "$(dirname "$0")/bypass-server" "$BINARY_PATH"
    chmod +x "$BINARY_PATH"
    log_info "Бинарник скопирован: $BINARY_PATH"
else
    log_warn "Исходники не найдены. Скопируйте бинарник вручную в $BINARY_PATH"
fi

# ============================================================
# 11. Создание systemd service
# ============================================================
log_step "Настройка systemd службы..."

cat > /etc/systemd/system/bypass-server.service <<SYSTEMD
[Unit]
Description=Bypass Server (WDTT + WireGuard + Zapret)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BINARY_PATH -config $CONFIG_DIR/server.yaml
WorkingDirectory=$APP_DIR
Restart=always
RestartSec=5
LimitNOFILE=65535
# Безопасность: не даём доступ к /home и другим системным директориям
ProtectSystem=strict
ReadWritePaths=$APP_DIR $WG_KEYS_DIR /run
NoNewPrivileges=false
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW

[Install]
WantedBy=multi-user.target
SYSTEMD

systemctl daemon-reload
systemctl enable bypass-server.service
log_info "systemd служба создана и включена: bypass-server.service"

# ============================================================
# 12. Настройка firewall (nftables/iptables)
# ============================================================
log_step "Настройка firewall..."

# Открытие портов
if command -v nft &>/dev/null; then
    # Проверяем, есть ли уже таблица
    if ! nft list tables 2>/dev/null | grep -q "bypass_fw"; then
        nft add table ip bypass_fw 2>/dev/null || true
    fi
    # Правила для WDTT DTLS
    nft add chain ip bypass_fw input '{ type filter hook input priority 0; }' 2>/dev/null || true
    nft add rule ip bypass_fw input udp dport 56000 accept 2>/dev/null || true
    # Правила для API
    nft add rule ip bypass_fw input tcp dport 8080 accept 2>/dev/null || true
    log_info "nftables правила добавлены (DTLS:56000, API:8080)"
fi

# ============================================================
# 13. Финализация
# ============================================================
echo ""
echo "═══════════════════════════════════════════════════════════"
log_info "Развертывание РФ-сервера завершено!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo -e "${CYAN}Что установлено:${NC}"
echo "  - WireGuard:          $(command -v wg || echo 'не найден')"
echo "  - nfqws (Zapret):     $([ -x "$ZAPRET_NFQWS" ] && echo 'установлен' || echo 'НЕ УСТАНОВЛЕН')"
echo "  - Xray-core:          $([ -x "$XRAY_PATH" ] && echo 'установлен' || echo 'НЕ УСТАНОВЛЕН')"
echo "  - bypass-server:      $([ -x "$BINARY_PATH" ] && echo 'установлен' || echo 'НЕ УСТАНОВЛЕН')"
echo "  - WG ключи:           $WG_KEYS_FILE"
echo "  - Конфиг:             $CONFIG_DIR/server.yaml"
echo ""
echo -e "${CYAN}Публичный ключ WireGuard:${NC}"
echo "  $WG_PUB_KEY"
echo ""
echo -e "${YELLOW}Дальнейшие шаги:${NC}"
echo "  1. Отредактируйте конфиг: nano $CONFIG_DIR/server.yaml"
echo "     - Укажите пароль WDTT (wdtt.password)"
echo "     - Настройте VLESS подключение к ЕС-серверу"
echo "     - Укажите API токен"
echo ""
echo "  2. Добавьте пользователей: nano $CONFIG_DIR/users.json"
echo ""
echo "  3. Запустите: systemctl start bypass-server"
echo "  4. Логи:     journalctl -u bypass-server -f"
echo "  5. Статус:   systemctl status bypass-server"
echo ""
echo -e "${YELLOW}Внимание:${NC} UDP-порт 56000 должен быть открыт в firewall провайдера!"
echo "═══════════════════════════════════════════════════════════"