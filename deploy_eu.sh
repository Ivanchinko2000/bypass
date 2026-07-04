#!/bin/bash
# ============================================================
# deploy_eu.sh — Развертывание ЕС-сервера (VLESS+Reality через Xray)
# Простой скрипт: Xray-core + конфиг + systemd
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
# 2. Базовые зависимости
# ============================================================
log_step "Обновление пакетов..."
apt-get update -qq

log_step "Установка базовых зависимостей..."
apt-get install -y -qq \
    curl \
    wget \
    ca-certificates \
    jq \
    unzip \
    systemd

# ============================================================
# 3. Установка Xray-core
# ============================================================
log_step "Установка Xray-core..."

XRAY_PATH="/usr/local/bin/xray-core"
XRAY_DIR="/opt/xray"
XRAY_CONFIG="$XRAY_DIR/config.json"

mkdir -p "$XRAY_DIR"

if [ -x "$XRAY_PATH" ]; then
    log_info "Xray-core уже установлен: $XRAY_PATH"
    $XRAY_PATH version 2>/dev/null | head -1 || true
else
    log_info "Загрузка Xray-core..."

    # Определяем архитектуру
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
# 4. Генерация ключей Reality
# ============================================================
log_step "Генерация Reality ключей..."

REALITY_KEYS_DIR="$XRAY_DIR/keys"
mkdir -p "$REALITY_KEYS_DIR"

if [ -f "$REALITY_KEYS_DIR/private.key" ] && [ -f "$REALITY_KEYS_DIR/public.key" ]; then
    REALITY_PRIV=$(cat "$REALITY_KEYS_DIR/private.key")
    REALITY_PUB=$(cat "$REALITY_KEYS_DIR/public.key")
    log_info "Reality ключи найдены"
else
    log_info "Генерация пары Reality ключей..."
    $XRAY_PATH x25519 -i "$REALITY_KEYS_DIR/private.key" -o "$REALITY_KEYS_DIR/public.key" 2>/dev/null || {
        # Fallback: генерируем через xray x25519
        OUTPUT=$($XRAY_PATH x25519 2>/dev/null)
        REALITY_PRIV=$(echo "$OUTPUT" | head -1 | awk '{print $3}')
        REALITY_PUB=$(echo "$OUTPUT" | tail -1 | awk '{print $3}')

        if [ -n "$REALITY_PRIV" ] && [ -n "$REALITY_PUB" ]; then
            echo "$REALITY_PRIV" > "$REALITY_KEYS_DIR/private.key"
            echo "$REALITY_PUB" > "$REALITY_KEYS_DIR/public.key"
        fi
    }

    if [ -f "$REALITY_KEYS_DIR/private.key" ]; then
        REALITY_PRIV=$(cat "$REALITY_KEYS_DIR/private.key")
        REALITY_PUB=$(cat "$REALITY_KEYS_DIR/public.key")
    fi
fi

# ============================================================
# 5. Генерация UUID
# ============================================================
log_step "Генерация UUID..."
if command -v uuidgen &>/dev/null; then
    VLESS_UUID=$(uuidgen)
else
    # Fallback генерация UUID v4
    VLESS_UUID=$(cat /proc/sys/kernel/random/uuid 2>/dev/null || echo "СГЕНЕРИРУЙТЕ_UUID_РУЧНО")
fi

# Генерируем shortId
SHORT_ID=$(head -c 8 /dev/urandom | xxd -p | head -c 8)

# ============================================================
# 6. Создание конфигурации Xray
# ============================================================
log_step "Создание конфигурации Xray..."

DEST_SITE="www.microsoft.com"
DEST_PORT="443"
LISTEN_PORT="443"

# Определяем публичный IP
PUBLIC_IP=$(curl -sL --connect-timeout 5 "https://api.ipify.org" 2>/dev/null || echo "YOUR_PUBLIC_IP")
if [ "$PUBLIC_IP" = "YOUR_PUBLIC_IP" ]; then
    PUBLIC_IP=$(curl -sL --connect-timeout 5 "https://ifconfig.me" 2>/dev/null || echo "YOUR_PUBLIC_IP")
fi

# Читаем ключи
if [ -f "$REALITY_KEYS_DIR/private.key" ]; then
    REALITY_PRIV=$(cat "$REALITY_KEYS_DIR/private.key")
    REALITY_PUB=$(cat "$REALITY_KEYS_DIR/public.key")
fi

cat > "$XRAY_CONFIG" <<XRAYJSON
{
  "log": {
    "loglevel": "warning"
  },
  "inbounds": [
    {
      "tag": "vless-in",
      "listen": "0.0.0.0",
      "port": ${LISTEN_PORT},
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": "${VLESS_UUID}",
            "flow": "xtls-rprx-vision"
          }
        ],
        "decryption": "none",
        "fallbacks": []
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "${DEST_SITE}:${DEST_PORT}",
          "xver": 0,
          "serverNames": [
            "${DEST_SITE}"
          ],
          "privateKey": "${REALITY_PRIV}",
          "minClientVer": "",
          "maxClientVer": "",
          "maxTimediff": 0,
          "shortIds": [
            "${SHORT_ID}",
            "abcd1234"
          ],
          "handshake": {
            "server": "${DEST_SITE}",
            "server_port": ${DEST_PORT}
          }
        }
      },
      "sniffing": {
        "enabled": true,
        "destOverride": [
          "http",
          "tls"
        ]
      }
    }
  ],
  "outbounds": [
    {
      "tag": "direct",
      "protocol": "freedom"
    },
    {
      "tag": "block",
      "protocol": "blackhole"
    }
  ]
}
XRAYJSON

chmod 600 "$XRAY_CONFIG"
log_info "Конфиг Xray создан: $XRAY_CONFIG"

# ============================================================
# 7. Создание systemd service
# ============================================================
log_step "Настройка systemd службы..."

cat > /etc/systemd/system/xray-bypass.service <<SYSTEMD
[Unit]
Description=Xray VLESS+Reality Server (Bypass EU)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$XRAY_PATH -c $XRAY_CONFIG
WorkingDirectory=$XRAY_DIR
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SYSTEMD

systemctl daemon-reload
systemctl enable xray-bypass.service
log_info "systemd служба создана: xray-bypass.service"

# ============================================================
# 8. Настройка firewall
# ============================================================
log_step "Настройка firewall (открытие порта $LISTEN_PORT)..."

if command -v ufw &>/dev/null && ufw status | grep -q "active"; then
    ufw allow "$LISTEN_PORT/tcp" comment "VLESS+Reality"
    log_info "UFW: порт $LISTEN_PORT/tcp открыт"
fi

if command -v nft &>/dev/null; then
    nft add table ip bypass_eu 2>/dev/null || true
    nft add chain ip bypass_eu input '{ type filter hook input priority 0; }' 2>/dev/null || true
    nft add rule ip bypass_eu input tcp dport $LISTEN_PORT accept 2>/dev/null || true
    log_info "nftables: порт $LISTEN_PORT/tcp открыт"
fi

# ============================================================
# 9. Финализация
# ============================================================
echo ""
echo "═══════════════════════════════════════════════════════════"
log_info "Развертывание ЕС-сервера завершено!"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo -e "${CYAN}Параметры подключения:${NC}"
echo "  IP адрес:              $PUBLIC_IP"
echo "  Порт:                  $LISTEN_PORT"
echo "  VLESS UUID:            $VLESS_UUID"
echo "  Reality Public Key:    ${REALITY_PUB:---неизвестно---}"
echo "  Reality Short ID:      $SHORT_ID"
echo "  SNI (Server Name):     $DEST_SITE"
echo "  Fingerprint:           chrome"
echo "  Transport:             tcp"
echo "  Security:              reality"
echo ""
echo -e "${CYAN}Файлы:${NC}"
echo "  Конфиг Xray:    $XRAY_CONFIG"
echo "  Приватный ключ:  $REALITY_KEYS_DIR/private.key"
echo "  Публичный ключ:  $REALITY_KEYS_DIR/public.key"
echo ""
echo -e "${YELLOW}Передайте следующие данные для настройки РФ-сервера:${NC}"
echo "  vless.client.remote_addr = \"$PUBLIC_IP:$LISTEN_PORT\""
echo "  vless.client.uuid = \"$VLESS_UUID\""
echo "  vless.client.public_key = \"$REALITY_PUB\""
echo "  vless.client.short_id = \"$SHORT_ID\""
echo "  vless.client.server_name = \"$DEST_SITE\""
echo ""
echo -e "${YELLOW}Управление:${NC}"
echo "  Запуск:  systemctl start xray-bypass"
echo "  Стоп:    systemctl stop xray-bypass"
echo "  Логи:    journalctl -u xray-bypass -f"
echo "  Статус:  systemctl status xray-bypass"
echo ""
echo -e "${YELLOW}Внимание:${NC} TCP-порт $LISTEN_PORT должен быть открыт в firewall провайдера!"
echo "═══════════════════════════════════════════════════════════"