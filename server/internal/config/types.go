package config

// ServerConfig — главная конфигурация сервера.
// Загружается из configs/server.yaml.
type ServerConfig struct {
	// Mode: "rf" или "eu"
	Mode string `yaml:"mode"`

	// Listen — адреса для прослушивания
	Listen ListenConfig `yaml:"listen"`

	// WireGuard — настройки WireGuard
	WireGuard WireGuardConfig `yaml:"wireguard"`

	// WDTT — настройки WDTT терминации (только для РФ-сервера)
	WDTT WDTTConfig `yaml:"wdtt"`

	// VLESS — настройки VLESS+Reality
	VLESS VLESSConfig `yaml:"vless"`

	// Zapret — настройки интеграции с Zapret (только для РФ-сервера)
	Zapret ZapretConfig `yaml:"zapret"`

	// DNS — настройки DNS-резолвера
	DNS DNSConfig `yaml:"dns"`

	// API — настройки HTTP API
	API APIConfig `yaml:"api"`

	// Users — путь к файлу пользователей
	UsersFile string `yaml:"users_file"`

	// Lists — пути к файлам списков
	Lists ListsConfig `yaml:"lists"`

	// Logging
	LogLevel string `yaml:"log_level"`
	LogFile  string `yaml:"log_file"`
}

// ListenConfig — адреса для прослушивания
type ListenConfig struct {
	// APIAddr — адрес HTTP API (например ":8080")
	APIAddr string `yaml:"api_addr"`

	// DNSAddr — адрес DoH DNS (например ":8443")
	DNSAddr string `yaml:"dns_addr"`

	// WDTTDTLSAddr — UDP-порт DTLS для WDTT клиентов (например ":56000")
	WDTTDTLSAddr string `yaml:"wdtt_dtls_addr"`

	// VLESSInboundAddr — адрес входящего VLESS (например ":8443")
	// Только если сервер сам терминирует VLESS (альтернатива — Xray бинарник)
	VLESSInboundAddr string `yaml:"vless_inbound_addr"`

	// SOCKS5Addr — локальный SOCKS5 для маршрутизации geo-трафика к Xray (например "127.0.0.1:1080")
	SOCKS5Addr string `yaml:"socks5_addr"`
}

// WireGuardConfig — настройки WireGuard интерфейса
type WireGuardConfig struct {
	// InterfaceName — имя интерфейса (например "bypass0")
	InterfaceName string `yaml:"interface_name"`

	// PrivateKey — приватный ключ сервера (base64)
	PrivateKey string `yaml:"private_key"`

	// Subnet — подсеть клиентов (например "10.66.0.0/16")
	Subnet string `yaml:"subnet"`

	// ServerIP — IP сервера в подсети (например "10.66.66.1")
	ServerIP string `yaml:"server_ip"`

	// MTU — MTU для WireGuard
	MTU int `yaml:"mtu"`

	// Keepalive — интервал keepalive в секундах
	Keepalive int `yaml:"keepalive"`

	// InternalPort — внутренний WireGuard-порт сервера
	InternalPort int `yaml:"internal_port"`
}

// WDTTConfig — настройки WDTT (только для РФ-сервера)
type WDTTConfig struct {
	// Enabled — включить WDTT терминацию
	Enabled bool `yaml:"enabled"`

	// DTLSAddr — UDP-адрес для DTLS (например ":56000")
	DTLSAddr string `yaml:"dtls_addr"`

	// Password — главный пароль WDTT
	Password string `yaml:"password"`
}

// VLESSConfig — настройки VLESS+Reality
type VLESSConfig struct {
	// Role — "server" (ЕС-сервер терминирует) или "client" (РФ-сервер подключается к ЕС)
	Role string `yaml:"role"`

	// Секция для сервера (ЕС)
	Server VLESSServerConfig `yaml:"server"`

	// Секция для клиента (РФ → ЕС)
	Client VLESSClientConfig `yaml:"client"`
}

// VLESSServerConfig — настройки VLESS сервера (ЕС-сервер)
type VLESSServerConfig struct {
	// Listen — адрес входящего VLESS+Reality (например ":443")
	Listen string `yaml:"listen"`

	// PrivateKey — Reality приватный ключ
	PrivateKey string `yaml:"private_key"`

	// ShortID — Reality shortId
	ShortID string `yaml:"short_id"`

	// Dest — камуфляжный сайт (например "www.microsoft.com:443")
	Dest string `yaml:"dest"`

	// ServerNames — список SNI для камуфляжа
	ServerNames []string `yaml:"server_names"`
}

// VLESSClientConfig — настройки VLESS клиента (РФ-сервер → ЕС-сервер)
type VLESSClientConfig struct {
	// Enabled — включить исходящий VLESS+Reality туннель к ЕС-серверу
	Enabled bool `yaml:"enabled"`

	// RemoteAddr — адрес ЕС-сервера (например "1.2.3.4:443")
	RemoteAddr string `yaml:"remote_addr"`

	// UUID — UUID для аутентификации на ЕС-сервере
	UUID string `yaml:"uuid"`

	// PublicKey — Reality публичный ключ ЕС-сервера
	PublicKey string `yaml:"public_key"`

	// ShortID — Reality shortId
	ShortID string `yaml:"short_id"`

	// Fingerprint — TLS fingerprint (например "chrome")
	Fingerprint string `yaml:"fingerprint"`

	// ServerName — SNI (например "www.microsoft.com")
	ServerName string `yaml:"server_name"`

	// XrayPath — путь к Xray-core бинарнику (если используем внешний)
	XrayPath string `yaml:"xray_path"`

	// XrayConfigPath — путь к конфигу Xray
	XrayConfigPath string `yaml:"xray_config_path"`
}

// ZapretConfig — настройки интеграции с Zapret
type ZapretConfig struct {
	// Enabled — включить Zapret
	Enabled bool `yaml:"enabled"`

	// NfqwsPath — путь к nfqws бинарнику
	NfqwsPath string `yaml:"nfqws_path"`

	// NfqwsArgs — аргументы запуска nfqws
	// Пример: "--warp-crypto=auto --host-another=sni1.com --host-pfx=2"
	NfqwsArgs string `yaml:"nfqws_args"`

	// QNum — номер NFQUEUE (по умолчанию 200)
	QNum int `yaml:"qnum"`
}

// DNSConfig — настройки DNS
type DNSConfig struct {
	// Enabled — включить DNS-резолвер
	Enabled bool `yaml:"enabled"`

	// Listen — адрес DoH/DoT слушателя
	Listen string `yaml:"listen"`

	// Upstream — upstream DNS-серверы
	Upstream []string `yaml:"upstream"`

	// EUUpstream — upstream DNS для geo-запросов (через туннель к ЕС)
	// Если пусто — используется Upstream
	EUUpstream []string `yaml:"eu_upstream"`

	// EnableDoH — включить DoH listener
	EnableDoH bool `yaml:"enable_doh"`

	// CacheTTL — TTL кэша DNS (секунды)
	CacheTTL int `yaml:"cache_ttl"`
}

// APIConfig — настройки HTTP API
type APIConfig struct {
	// Enabled — включить API
	Enabled bool `yaml:"enabled"`

	// Listen — адрес (например ":8080")
	Listen string `yaml:"listen"`

	// AuthToken — токен авторизации для административных запросов
	AuthToken string `yaml:"auth_token"`
}

// ListsConfig — пути к файлам списков
type ListsConfig struct {
	// DPIBlocklist — путь к файлу с DPI-блокируемыми доменами
	DPIBlocklist string `yaml:"dpi_blocklist"`

	// GeoBlocklist — путь к файлу с гео-блокируемыми доменами
	GeoBlocklist string `yaml:"geo_blocklist"`
}