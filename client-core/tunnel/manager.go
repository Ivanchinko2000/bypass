// Package tunnel — главный оркестратор туннеля.
// Управляет жизненным циклом подключения: режимы WDTT, VLESS, Auto.
// Координирует: kill switch, DNS, сплит-туннелирование, маршрутизацию.
// Предоставляет callback-интерфейс для уведомлений о событиях.
package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"client-core/auth"
	"client-core/dns"
	"client-core/killswitch"
	"client-core/routing"
)

// TunnelMode — режим работы туннеля.
type TunnelMode string

const (
	// ModeWDTT — только WDTT (WireGuard over DTLS через TURN к РФ-серверу).
	ModeWDTT TunnelMode = "wdtt"

	// ModeVLESS — только VLESS+Reality (к ЕС-серверу).
	ModeVLESS TunnelMode = "vless"

	// ModeAuto — автоматический выбор на основе доступности (пинг Google).
	// Если прямой доступ OK — VLESS (через ЕС).
	// Если заблокирован — WDTT (напрямую к РФ).
	ModeAuto TunnelMode = "auto"
)

// TunnelState — состояние туннеля.
type TunnelState string

const (
	// StateIdle — туннель не подключён
	StateIdle TunnelState = "idle"

	// StateConnecting — идёт подключение
	StateConnecting TunnelState = "connecting"

	// StateConnected — туннель активен
	StateConnected TunnelState = "connected"

	// StateDisconnecting — идёт отключение
	StateDisconnecting TunnelState = "disconnecting"

	// StateReconnecting — переподключение (fallback)
	StateReconnecting TunnelState = "reconnecting"

	// StateError — фатальная ошибка
	StateError TunnelState = "error"
)

// TunnelStats — статистика туннеля (трафик, время подключения).
type TunnelStats struct {
	// RxBytes — получено байт
	RxBytes int64

	// TxBytes — отправлено байт
	TxBytes int64

	// ConnectedSince — время подключения
	ConnectedSince time.Time

	// ActiveMode — текущий активный режим
	ActiveMode TunnelMode
}

// ConnectParams — параметры подключения.
type ConnectParams struct {
	// Mode — режим туннеля (wdtt, vless, auto)
	Mode TunnelMode

	// Password — пароль пользователя
	Password string

	// ServerURL — URL API сервера (например "https://1.2.3.4:8080")
	ServerURL string

	// DeviceID — идентификатор устройства (если пусто — используется из auth)
	DeviceID string

	// MTU — MTU для WireGuard (0 = использовать значение от сервера)
	MTU int

	// Workers — количество рабочих потоков WDTT (0 = автоматически)
	Workers int
}

// Callbacks — функции обратного вызова для событий туннеля.
type Callbacks struct {
	// OnStateChange вызывается при изменении состояния туннеля
	OnStateChange func(state TunnelState, message string)

	// OnLog вызывается при новом сообщении в логе
	OnLog func(level, message string)

	// OnStats вызывается периодически со статистикой трафика
	OnStats func(stats TunnelStats)

	// OnError вызывается при ошибке (фатальной или с возможностью восстановления)
	OnError func(err error, recoverable bool)
}

// Backend — интерфейс платформенно-зависимой части туннеля.
// Реализуется в клиентском приложении (Windows, Linux, macOS).
// Для WDTT — управляет wg-turn-client core.
// Для VLESS — управляет Xray-core процессом.
type Backend interface {
	// StartWDTT запускает WDTT-туннель.
	// Вызывается оркестратором, когда выбран режим WDTT.
	StartWDTT(ctx context.Context, params WDTTParams, cb BackendCallbacks) error

	// StartVLESS запускает VLESS-туннель.
	StartVLESS(ctx context.Context, params VLESSParams, cb BackendCallbacks) error

	// Stop останавливает активный туннель.
	Stop() error

	// IsRunning возвращает true, если туннель активен.
	IsRunning() bool

	// ApplyWGConfig применяет WireGuard-конфигурацию (для WDTT режима).
	ApplyWGConfig(wgConfig string) error

	// TeardownWG удаляет WireGuard-конфигурацию.
	TeardownWG() error
}

// WDTTParams — параметры для WDTT-подключения.
type WDTTParams struct {
	// PeerAddr — адрес пира (host:port)
	PeerAddr string

	// Password — пароль WDTT
	Password string

	// DeviceID — идентификатор устройства
	DeviceID string

	// Workers — количество рабочих потоков
	Workers int

	// MTU — MTU для WireGuard
	MTU int

	// ServerConfig — конфигурация от сервера
	ServerConfig *auth.ServerConfig
}

// VLESSParams — параметры для VLESS-подключения.
type VLESSParams struct {
	// RemoteAddr — адрес ЕС-сервера (host:port)
	RemoteAddr string

	// UUID — UUID для VLESS аутентификации
	UUID string

	// PublicKey — Reality публичный ключ
	PublicKey string

	// ShortID — Reality shortId
	ShortID string

	// Fingerprint — TLS fingerprint
	Fingerprint string

	// ServerName — SNI для камуфляжа
	ServerName string

	// ServerConfig — конфигурация от сервера
	ServerConfig *auth.ServerConfig
}

// BackendCallbacks — callback'и для платформенного бэкенда.
type BackendCallbacks struct {
	// OnConnected вызывается, когда туннель полностью подключён
	OnConnected func(tunnelIP string)

	// OnDisconnected вызывается, когда туннель отключён
	OnDisconnected func()

	// OnLog вызывается для логирования
	OnLog func(level, message string)

	// OnStats вызывается со статистикой трафика
	OnStats func(rx, tx int64)
}

// Manager — главный оркестратор туннеля.
// Потокобезопасен. Управляет подключением, переключением режимов,
// автореконнектом и координацией всех подсистем.
type Manager struct {
	mu sync.Mutex

	// cfg — параметры последнего подключения
	cfg ConnectParams

	// callbacks — функции обратного вызова
	callbacks Callbacks

	// state — текущее состояние
	state TunnelState

	// activeMode — текущий активный режим (при Auto — решается автоматически)
	activeMode TunnelMode

	// backend — платформенный бэкенд (WDTT/VLESS)
	backend Backend

	// authClient — клиент аутентификации
	authClient *auth.Client

	// routingMgr — менеджер доменных списков
	routingMgr *routing.Manager

	// dnsResolver — DNS-резолвер
	dnsResolver *dns.Resolver

	// killSwitch — менеджер kill switch
	killSwitch *killswitch.Manager

	// serverConfig — конфигурация полученная от сервера
	serverConfig *auth.ServerConfig

	// stats — текущая статистика
	stats TunnelStats

	// cancel — функция отмены текущего подключения
	cancel context.CancelFunc

	// reconnectAttempts — количество попыток реконнекта
	reconnectAttempts int

	// maxReconnectAttempts — максимальное количество попыток реконнекта
	maxReconnectAttempts int

	// reconnectDelay — базовая задержка между попытками реконнекта
	reconnectDelay time.Duration

	// configDir — директория конфигурации
	configDir string
}

// ManagerConfig — конфигурация менеджера туннеля.
type ManagerConfig struct {
	// Backend — платформенный бэкенд (обязательный)
	Backend Backend

	// Callbacks — функции обратного вызова
	Callbacks Callbacks

	// ServerURL — URL API сервера
	ServerURL string

	// DeviceID — идентификатор устройства
	DeviceID string

	// ConfigDir — директория конфигурации
	ConfigDir string

	// MaxReconnectAttempts — макс. попыток реконнекта (по умолчанию 5)
	MaxReconnectAttempts int

	// ReconnectDelay — задержка реконнекта (по умолчанию 5 сек)
	ReconnectDelay time.Duration

	// KillSwitchEnabled — kill switch включён
	KillSwitchEnabled bool

	// DoHServers — кастомные DoH-серверы (пусто = дефолтные)
	DoHServers []string

	// BlockDNSLeak — блокировать утечки DNS
	BlockDNSLeak bool

	// AutoReloadLists — автоперезагрузка списков (интервал)
	AutoReloadLists time.Duration
}

// NewManager создаёт оркестратор туннеля.
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.MaxReconnectAttempts == 0 {
		cfg.MaxReconnectAttempts = 5
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 5 * time.Second
	}

	m := &Manager{
		backend:              cfg.Backend,
		callbacks:           cfg.Callbacks,
		maxReconnectAttempts: cfg.MaxReconnectAttempts,
		reconnectDelay:      cfg.ReconnectDelay,
		state:               StateIdle,
	}

	// Инициализируем клиент аутентификации
	m.authClient = auth.NewClient(auth.ClientConfig{
		ServerURL: cfg.ServerURL,
		DeviceID:  cfg.DeviceID,
		ConfigDir: cfg.ConfigDir,
	})

	// Инициализируем менеджер доменных списков
	m.routingMgr = routing.NewManager(routing.ManagerConfig{
		ServerURL:          cfg.ServerURL,
		LocalDir:           cfg.ConfigDir,
		AutoReloadInterval: cfg.AutoReloadLists,
	})

	// Инициализируем DNS-резолвер
	m.dnsResolver = dns.NewResolver(dns.ResolverConfig{
		DoHServers: cfg.DoHServers,
		BlockLeak:  cfg.BlockDNSLeak,
	}, m.routingMgr)

	// Инициализируем kill switch
	m.killSwitch = killswitch.NewManager(killswitch.ManagerConfig{
		Enabled: cfg.KillSwitchEnabled,
	})

	m.configDir = cfg.ConfigDir

	return m
}

// Connect запускает подключение к серверу.
func (m *Manager) Connect(params ConnectParams) error {
	m.mu.Lock()

	// Проверяем, не подключены ли уже
	if m.state == StateConnected || m.state == StateConnecting || m.state == StateReconnecting {
		m.mu.Unlock()
		return fmt.Errorf("туннель уже подключается или подключён")
	}

	// Сохраняем параметры
	m.cfg = params
	m.reconnectAttempts = 0

	// Обновляем URL сервера, если передан
	if params.ServerURL != "" {
		m.authClient.SetServerURL(params.ServerURL)
		m.routingMgr.SetServerURL(params.ServerURL)
	}

	m.mu.Unlock()

	// Запускаем в горутине
	go m.connectLoop()

	return nil
}

// connectLoop — основной цикл подключения (с автореконнектом).
func (m *Manager) connectLoop() {
	for {
		// Выбираем режим
		mode := m.cfg.Mode
		if mode == ModeAuto {
			mode = m.detectBestMode()
			m.mu.Lock()
			m.activeMode = mode
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			m.activeMode = mode
			m.mu.Unlock()
		}

		m.setState(StateConnecting, fmt.Sprintf("Подключение (%s)...", mode))
		m.log("INFO", fmt.Sprintf("Режим: %s", mode))

		// Авторизация на сервере
		authCtx, authCancel := context.WithTimeout(context.Background(), 30*time.Second)
		serverConfig, err := m.authClient.Authenticate(authCtx, m.cfg.Password)
		authCancel()

		if err != nil {
			m.handleConnectError(err, "авторизация")
			// Фатальная ошибка — не реконнектим
			if authErr, ok := err.(*auth.AuthError); ok && authErr.IsFatalAuthError() {
				return
			}
			continue
		}

		m.mu.Lock()
		m.serverConfig = serverConfig
		m.mu.Unlock()

		// Загружаем доменные списки
		m.routingMgr.LoadDomainsDirect(serverConfig.DPIDomains, serverConfig.GeoDomains)
		m.routingMgr.StartAutoReload()

		// Запускаем туннель в выбранном режиме
		backendCtx, backendCancel := context.WithCancel(context.Background())
		m.mu.Lock()
		m.cancel = backendCancel
		m.mu.Unlock()

		err = m.startTunnel(backendCtx, mode, serverConfig)
		if err != nil {
			backendCancel()
			m.handleConnectError(err, string(mode))
			continue
		}

		// Туннель подключён — ожидаем отключения
		m.waitDisconnect(backendCtx)
		backendCancel()

		// Очищаем ресурсы
		m.cleanup()

		// Выходим из цикла, если это было штатное отключение
		m.mu.Lock()
		state := m.state
		reconnectAttempts := m.reconnectAttempts
		m.mu.Unlock()

		if state == StateDisconnecting {
			m.setState(StateIdle, "Отключено")
			return
		}

		// Проверяем лимит реконнекта
		if reconnectAttempts >= m.maxReconnectAttempts {
			m.setState(StateError, fmt.Sprintf("Превышен лимит реконнекта (%d попыток)", m.maxReconnectAttempts))
			return
		}

		// Задержка перед реконнектом (экспоненциальная)
		delay := m.reconnectDelay * time.Duration(1<<uint(reconnectAttempts-1))
		if delay > 60*time.Second {
			delay = 60 * time.Second
		}

		m.setState(StateReconnecting, fmt.Sprintf("Переподключение через %v...", delay))
		m.log("WARN", fmt.Sprintf("Реконнект #%d через %v", reconnectAttempts, delay))

		select {
		case <-time.After(delay):
			// продолжаем цикл
		case <-backendCtx.Done():
			return
		}
	}
}

// startTunnel запускает туннель в заданном режиме.
func (m *Manager) startTunnel(ctx context.Context, mode TunnelMode, serverConfig *auth.ServerConfig) error {
	backendCb := BackendCallbacks{
		OnConnected: func(tunnelIP string) {
			m.mu.Lock()
			m.stats.ConnectedSince = time.Now()
			m.mu.Unlock()

			// Активируем kill switch (передаём IP сервера для исключения)
			_ = m.killSwitch.Activate(tunnelIP, serverConfig.ServerIP, nil)

			// Запускаем блокировку DNS-утечек
			_ = m.dnsResolver.StartLeakBlock()

			m.setState(StateConnected, "Подключено")
			m.log("INFO", fmt.Sprintf("Туннель активен ✓ (IP: %s, режим: %s)", tunnelIP, mode))
		},
		OnDisconnected: func() {
			m.log("INFO", "Туннель отключён")
		},
		OnLog: func(level, message string) {
			m.log(level, message)
		},
		OnStats: func(rx, tx int64) {
			m.mu.Lock()
			m.stats.RxBytes = rx
			m.stats.TxBytes = tx
			m.stats.ActiveMode = mode
			m.mu.Unlock()
			if m.callbacks.OnStats != nil {
				m.callbacks.OnStats(m.stats)
			}
		},
	}

	switch mode {
	case ModeWDTT:
		mtu := m.cfg.MTU
		if mtu == 0 && serverConfig.WGMTU > 0 {
			mtu = serverConfig.WGMTU
		}
		if mtu == 0 {
			mtu = 1300
		}

		workers := m.cfg.Workers
		if workers <= 0 {
			workers = 9
		}

		params := WDTTParams{
			PeerAddr:     serverConfig.ServerIP,
			Password:     m.cfg.Password,
			DeviceID:     m.authClient.GetDeviceID(),
			Workers:      workers,
			MTU:          mtu,
			ServerConfig: serverConfig,
		}

		m.log("INFO", fmt.Sprintf("Запуск WDTT: peer=%s, workers=%d, mtu=%d",
			params.PeerAddr, params.Workers, params.MTU))
		return m.backend.StartWDTT(ctx, params, backendCb)

	case ModeVLESS:
		params := VLESSParams{
			RemoteAddr:  serverConfig.VLESSAddr,
			UUID:        serverConfig.VLESSUUID,
			PublicKey:   serverConfig.RealityPubKey,
			ShortID:     serverConfig.RealityShortID,
			ServerName:  serverConfig.RealitySNI,
			ServerConfig: serverConfig,
		}

		m.log("INFO", fmt.Sprintf("Запуск VLESS: uuid=%s...", params.UUID[:min(8, len(params.UUID))]))
		return m.backend.StartVLESS(ctx, params, backendCb)

	default:
		return fmt.Errorf("неизвестный режим: %s", mode)
	}
}

// waitDisconnect ожидает отключения туннеля (контекст отменён).
func (m *Manager) waitDisconnect(ctx context.Context) {
	<-ctx.Done()
}

// handleConnectError обрабатывает ошибку подключения.
func (m *Manager) handleConnectError(err error, stage string) {
	m.mu.Lock()
	m.reconnectAttempts++
	attempts := m.reconnectAttempts
	m.mu.Unlock()

	msg := fmt.Sprintf("Ошибка (%s): %v", stage, err)
	m.log("ERROR", msg)

	// Определяем, восстановима ли ошибка
	recoverable := true
	if authErr, ok := err.(*auth.AuthError); ok {
		if authErr.IsFatalAuthError() {
			recoverable = false
		}
	}

	if !recoverable {
		m.setState(StateError, msg)
		if m.callbacks.OnError != nil {
			m.callbacks.OnError(err, false)
		}
		return
	}

	// Восстановимая ошибка — продолжаем цикл реконнекта
	if m.callbacks.OnError != nil {
		m.callbacks.OnError(err, true)
	}
}

// cleanup очищает ресурсы после отключения.
func (m *Manager) cleanup() {
	// Деактивируем kill switch
	_ = m.killSwitch.Deactivate()

	// Останавливаем DNS
	m.dnsResolver.Stop()

	// Останавливаем автоперезагрузку списков
	m.routingMgr.StopAutoReload()

	// Обнуляем статистику
	m.mu.Lock()
	m.stats = TunnelStats{}
	m.mu.Unlock()
}

// Disconnect отключает туннель.
func (m *Manager) Disconnect() {
	m.mu.Lock()
	if m.state == StateIdle || m.state == StateDisconnecting {
		m.mu.Unlock()
		return
	}

	m.setState(StateDisconnecting, "Отключение...")
	cancel := m.cancel
	m.mu.Unlock()

	// Останавливаем бэкенд
	if m.backend != nil {
		_ = m.backend.Stop()
	}

	// Отменяем контекст
	if cancel != nil {
		cancel()
	}
}

// GetState возвращает текущее состояние туннеля.
func (m *Manager) GetState() TunnelState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// GetActiveMode возвращает текущий активный режим.
func (m *Manager) GetActiveMode() TunnelMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeMode
}

// GetStats возвращает текущую статистику.
func (m *Manager) GetStats() TunnelStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// GetServerConfig возвращает конфигурацию от сервера.
func (m *Manager) GetServerConfig() *auth.ServerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.serverConfig == nil {
		return nil
	}
	cp := *m.serverConfig
	return &cp
}

// GetRoutingManager возвращает менеджер доменных списков.
func (m *Manager) GetRoutingManager() *routing.Manager {
	return m.routingMgr
}

// GetDNSResolver возвращает DNS-резолвер.
func (m *Manager) GetDNSResolver() *dns.Resolver {
	return m.dnsResolver
}

// GetKillSwitch возвращает менеджер kill switch.
func (m *Manager) GetKillSwitch() *killswitch.Manager {
	return m.killSwitch
}

// SetKillSwitchEnabled включает/выключает kill switch.
func (m *Manager) SetKillSwitchEnabled(enabled bool) {
	m.killSwitch.SetEnabled(enabled)
}

// IsConnected возвращает true, если туннель подключён.
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state == StateConnected
}

// GetAuthClient возвращает клиент аутентификации.
func (m *Manager) GetAuthClient() *auth.Client {
	return m.authClient
}

// detectBestMode определяет лучший режим подключения.
// Пингует google.com через TCP-443 напрямую.
// Если прямой доступ OK → VLESS (трафик через ЕС).
// Если заблокирован → WDTT (напрямую к РФ).
func (m *Manager) detectBestMode() TunnelMode {
	m.log("INFO", "Автоматический выбор режима...")

	// Пингуем google.com (или другой доступный хост) напрямую
	reachable := m.pingHost("google.com", 3*time.Second)

	if reachable {
		m.log("INFO", "Прямой доступ в интернет OK → режим VLESS")
		return ModeVLESS
	}

	m.log("INFO", "Прямой доступ заблокирован → режим WDTT")
	return ModeWDTT
}

// pingHost проверяет доступность хоста через TCP-соединение.
func (m *Manager) pingHost(host string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "443"), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// setState устанавливает состояние и вызывает callback.
func (m *Manager) setState(state TunnelState, message string) {
	m.mu.Lock()
	m.state = state
	m.mu.Unlock()

	if m.callbacks.OnStateChange != nil {
		m.callbacks.OnStateChange(state, message)
	}
}

// log отправляет сообщение в лог через callback.
func (m *Manager) log(level, message string) {
	log.Printf("[%s] %s", level, message)
	if m.callbacks.OnLog != nil {
		m.callbacks.OnLog(level, message)
	}
}

// StatsJSON возвращает статистику в JSON-формате (для Wails-фронтенда).
func (m *Manager) StatsJSON() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, _ := json.Marshal(map[string]interface{}{
		"state":       m.state,
		"mode":        m.activeMode,
		"rx_bytes":    m.stats.RxBytes,
		"tx_bytes":    m.stats.TxBytes,
		"connected":   m.state == StateConnected,
		"since":       m.stats.ConnectedSince,
		"reconnects":  m.reconnectAttempts,
		"dpi_domains": len(m.routingMgr.GetDPIDomains()),
		"geo_domains": len(m.routingMgr.GetGeoDomains()),
	})
	return string(data)
}

// min возвращает минимум из двух int (Go 1.21 совместимость).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}