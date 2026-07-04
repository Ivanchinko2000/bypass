// Package backend — основное Wails-приложение BypassVPN.
// Содержит структуру App, привязанную к фронтенду через Wails,
// управляет профилями, состоянием подключения и системным треем.
package backend

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App — главное приложение Wails. Привязано к фронтенду.
// Предоставляет методы для подключения/отключения,
// управления профилями и настройки приложения.
type App struct {
	ctx         context.Context     // Wails контекст для отправки событий
	orch        *TunnelOrchestrator // Оркестратор туннеля
	trayEnabled atomic.Bool         // Включён ли системный трей
	quitting    atomic.Bool         // Идёт ли завершение приложения
	trayIcon    []byte              // Иконка для системного трея
}

// ServerProfile — профиль сервера для подключения.
// Хранится в %APPDATA%/BypassVPN/profiles/<name>.json
type ServerProfile struct {
	// Name — отображаемое имя профиля
	Name string `json:"name"`

	// ServerURL — URL API сервера (например "https://1.2.3.4:8080")
	ServerURL string `json:"server_url"`

	// Password — пароль пользователя
	Password string `json:"password"`

	// DeviceID — идентификатор устройства
	DeviceID string `json:"device_id,omitempty"`

	// Mode — предпочитаемый режим ("wdtt", "vless", "auto")
	Mode string `json:"mode,omitempty"`

	// WDTT — параметры WDTT
	WDTT WDTTProfileConfig `json:"wdtt,omitempty"`

	// VLESS — параметры VLESS
	VLESS VLESSProfileConfig `json:"vless,omitempty"`
}

// WDTTProfileConfig — параметры WDTT в профиле.
type WDTTProfileConfig struct {
	PeerAddr string   `json:"peer,omitempty"`
	Hashes   []string `json:"hashes,omitempty"`
	Listen   string   `json:"listen,omitempty"`
	TurnHost string   `json:"turn,omitempty"`
	TurnPort string   `json:"port,omitempty"`
	Workers  int      `json:"workers,omitempty"`
	MTU      int      `json:"mtu,omitempty"`
}

// VLESSProfileConfig — параметры VLESS в профиле.
type VLESSProfileConfig struct {
	RemoteAddr  string `json:"remote_addr,omitempty"`
	UUID        string `json:"uuid,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	ShortID     string `json:"short_id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	ServerName  string `json:"server_name,omitempty"`
}

// ConnectParams — параметры подключения от фронтенда.
type ConnectParams struct {
	// ProfileName — имя профиля для подключения
	ProfileName string `json:"profileName"`

	// Mode — режим подключения ("auto", "wdtt", "vless")
	Mode string `json:"mode"`

	// MTU — MTU для WireGuard (0 = из профиля или дефолт)
	MTU int `json:"mtu,omitempty"`
}

// AppState — состояние приложения для фронтенда.
type AppState struct {
	Connected  bool   `json:"connected"`
	State      string `json:"state"`
	Mode       string `json:"mode"`
	ProfileName string `json:"profileName"`
	RxBytes    int64  `json:"rxBytes"`
	TxBytes    int64  `json:"txBytes"`
	KillSwitch bool   `json:"killSwitch"`
	DNSLeak    bool   `json:"dnsLeak"`
	DPIDomains int    `json:"dpiDomains"`
	GeoDomains int    `json:"geoDomains"`
}

// Settings — настройки приложения.
type Settings struct {
	KillSwitch  bool     `json:"killSwitch"`
	DNSLeak     bool     `json:"dnsLeak"`
	AutoConnect bool     `json:"autoConnect"`
	AutoStart   bool     `json:"autoStart"`
	Tray        bool     `json:"tray"`
	Mode        string   `json:"mode"`
	MTU         int      `json:"mtu"`
	DoHServers  []string `json:"dohServers,omitempty"`
}

// NewApp создаёт новый экземпляр приложения.
func NewApp(trayIcon []byte) *App {
	return &App{trayIcon: trayIcon}
}

// Startup вызывается Wails при запуске приложения.
// Инициализирует оркестратор туннеля, загружает настройки,
// запускает системный трей.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// Инициализируем оркестратор туннеля
	a.orch = NewTunnelOrchestrator(ctx, a.onTunnelEvent)

	// Запускаем системный трей
	startTray(a.trayIcon,
		// Клик по "Показать"
		func() { runtime.WindowShow(ctx) },
		// Клик по иконке: показать окно или подключить/отключить
		func() {
			if a.orch.IsRunning() {
				a.orch.Stop()
			} else {
				runtime.WindowShow(ctx)
			}
		},
		// Выход
		func() {
			a.quitting.Store(true)
			a.orch.Stop()
			os.Exit(0)
		},
	)

	log.Println("[APP] BypassVPN запущен")
}

// OnBeforeClose вызывается при закрытии окна.
// Если трей включён — скрывает окно вместо выхода.
func (a *App) OnBeforeClose(ctx context.Context) bool {
	if a.trayEnabled.Load() && !a.quitting.Load() {
		runtime.WindowHide(ctx)
		return true // предотвращаем закрытие
	}
	return false
}

// ==========================================
// Методы, доступные фронтенду
// ==========================================

// Connect подключается к серверу по профилю.
func (a *App) Connect(p ConnectParams) error {
	log.Printf("[APP] Подключение: профиль=%s, режим=%s", p.ProfileName, p.Mode)
	return a.orch.Start(p)
}

// Disconnect отключает туннель.
func (a *App) Disconnect() {
	log.Println("[APP] Отключение...")
	a.orch.Stop()
}

// GetState возвращает текущее состояние приложения.
func (a *App) GetState() AppState {
	if a.orch == nil {
		return AppState{State: "idle"}
	}
	return a.orch.GetAppState()
}

// GetProfiles возвращает все сохранённые профили.
func (a *App) GetProfiles() map[string]ServerProfile {
	return ListProfiles()
}

// SaveProfile сохраняет профиль сервера.
func (a *App) SaveProfile(name string, p ServerProfile) error {
	p.Name = name
	return SaveProfile(name, p)
}

// GetProfile загружает профиль по имени.
func (a *App) GetProfile(name string) (*ServerProfile, error) {
	return LoadProfile(name)
}

// DeleteProfile удаляет профиль.
func (a *App) DeleteProfile(name string) error {
	return DeleteProfile(name)
}

// GetSettings загружает настройки приложения.
func (a *App) GetSettings() Settings {
	return LoadSettings()
}

// SaveSettings сохраняет настройки приложения.
func (a *App) SaveSettings(s Settings) error {
	a.trayEnabled.Store(s.Tray)
	SetTrayEnabled(s.Tray)
	return SaveSettings(s)
}

// GetLogs возвращает последние N записей лога.
func (a *App) GetLogs(limit int) []LogEntry {
	if a.orch == nil {
		return nil
	}
	return a.orch.GetLogs(limit)
}

// SetTrayEnabled управляет видимостью иконки в трее.
func (a *App) SetTrayEnabled(enabled bool) {
	a.trayEnabled.Store(enabled)
	SetTrayEnabled(enabled)
}

// CheckVPN возвращает имена активных VPN-интерфейсов (кроме нашего).
// Используется для предупреждения о конфликте VPN.
func (a *App) CheckVPN() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var found []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		n := strings.ToLower(iface.Name)
		// Пропускаем наш туннельный интерфейс
		if n == "bypass0" || n == "wg-turn" {
			continue
		}
		// Определяем VPN-интерфейсы по префиксу имени
		if strings.HasPrefix(n, "tun") ||
			strings.HasPrefix(n, "tap") ||
			strings.HasPrefix(n, "wg") ||
			strings.HasPrefix(n, "ppp") ||
			strings.HasPrefix(n, "nordlynx") ||
			strings.HasPrefix(n, "proton") ||
			strings.HasPrefix(n, "utun") ||
			strings.HasPrefix(n, "ipsec") ||
			strings.HasPrefix(n, "surfshark") {
			found = append(found, iface.Name)
		}
	}
	return found
}

// ==========================================
// Внутренние callback'и
// ==========================================

// onTunnelEvent обрабатывает события от оркестратора туннеля.
// Отправляет события в фронтенд через Wails Events.
func (a *App) onTunnelEvent(eventType string, data interface{}) {
	if a.ctx == nil {
		return
	}

	switch eventType {
	case "state_changed":
		// Отправляем обновление состояния в фронтенд
		runtime.EventsEmit(a.ctx, "state_changed", data)
		// Обновляем иконку трея
		if stateData, ok := data.(AppState); ok {
			setTrayStatus(stateData.Connected, stateData.RxBytes, stateData.TxBytes, 0)
		}

	case "log":
		// Отправляем запись лога в фронтенд
		if logData, ok := data.([]interface{}); ok && len(logData) >= 2 {
			runtime.EventsEmit(a.ctx, "log", logData[0], logData[1])
		}

	case "error":
		// Отправляем ошибку в фронтенд (показывается как toast)
		runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("%v", data))

	case "stats":
		// Отправляем статистику в фронтенд
		runtime.EventsEmit(a.ctx, "stats", data)
	}
}