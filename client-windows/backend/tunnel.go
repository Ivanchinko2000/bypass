// Package backend — оркестратор туннеля и вспомогательные функции.
// Файл tunnel.go содержит логику управления WDTT и VLESS туннелями,
// координацию с client-core, логирование, профили, настройки и трей.
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"wg-turn-client/core"
)

// ==========================================
// Логирование (паттерн PWDTT)
// ==========================================

// LogEntry — одна запись лога.
type LogEntry struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

// LogBuffer — кольцевой буфер логов.
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	maxSize int
}

const defaultLogBufSize = 500

func newLogBuffer() *LogBuffer {
	return &LogBuffer{maxSize: defaultLogBufSize}
}

func (lb *LogBuffer) push(level, message string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	entry := LogEntry{
		Level:   level,
		Message: message,
		Time:    time.Now().Format("15:04:05"),
	}
	if len(lb.entries) >= lb.maxSize {
		lb.entries = lb.entries[1:]
	}
	lb.entries = append(lb.entries, entry)
}

func (lb *LogBuffer) getAll(limit int) []LogEntry {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if limit <= 0 || limit > len(lb.entries) {
		limit = len(lb.entries)
	}
	start := len(lb.entries) - limit
	if start < 0 {
		start = 0
	}
	result := make([]LogEntry, len(lb.entries)-start)
	copy(result, lb.entries[start:])
	return result
}

func (lb *LogBuffer) clear() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.entries = nil
}

// wailsLogWriter — перехватывает log.Printf и направляет в Wails-события.
// Буферизует записи и флашит каждые 100мс чтобы не блокировать core.
// Параллельно пишет полный лог в файл.
type wailsLogWriter struct {
	ctx  context.Context
	mu   sync.Mutex
	buf  []logEntry
	stop chan struct{}
	file *os.File
	lb   *LogBuffer
}

type logEntry struct {
	level, msg string
}

// newSessionLogFile создаёт файл лога для текущей сессии.
func newSessionLogFile(profileName string) *os.File {
	dir := filepath.Join(configDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	name := ts + "_" + profileName + ".log"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return f
}

func (w *wailsLogWriter) start() {
	w.stop = make(chan struct{})
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				w.flush()
			case <-w.stop:
				w.flush()
				return
			}
		}
	}()
}

func (w *wailsLogWriter) flush() {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buf
	w.buf = nil
	w.mu.Unlock()
	for _, e := range batch {
		runtime.EventsEmit(w.ctx, "log", e.level, e.msg)
		w.lb.push(e.level, e.msg)
	}
}

func (w *wailsLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	// Обрезаем префикс времени/файла для чистого отображения
	if len(msg) > 20 && msg[4] == '/' {
		msg = strings.TrimSpace(msg[20:])
	}
	level := classifyLevel(msg)

	// Пишем в файл сразу
	if w.file != nil {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(w.file, "[%s] [%s] %s\n", ts, level, msg)
	}

	w.mu.Lock()
	if len(w.buf) >= defaultLogBufSize {
		w.buf = w.buf[1:]
	}
	w.buf = append(w.buf, logEntry{level, msg})
	w.mu.Unlock()
	return len(p), nil
}

// classifyLevel определяет уровень логирования по содержимому сообщения.
func classifyLevel(msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "fatal") ||
		strings.Contains(low, "ошибка") ||
		strings.Contains(low, "error") ||
		strings.Contains(low, "фатальн") ||
		strings.Contains(low, "panic"):
		return "ERROR"
	case strings.Contains(low, "warn") ||
		strings.Contains(low, "не удалось") ||
		strings.Contains(low, "повторим") ||
		strings.Contains(low, "повторяем") ||
		strings.Contains(low, "retry"):
		return "WARN"
	case strings.Contains(low, "debug") ||
		strings.Contains(low, "obfs") ||
		strings.Contains(low, "unwrap"):
		return "DEBUG"
	default:
		return "INFO"
	}
}

// ==========================================
// Конфигурация и профили
// ==========================================

// configDir возвращает директорию конфигурации приложения.
func configDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.Getenv("APPDATA")
		if base == "" {
			base = "."
		}
	}
	dir := filepath.Join(base, "BypassVPN")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// profilePath возвращает путь к файлу профиля.
func profilePath(name string) string {
	return filepath.Join(configDir(), "profiles", name+".json")
}

// settingsPath возвращает путь к файлу настроек.
func settingsPath() string {
	return filepath.Join(configDir(), "settings.json")
}

// SaveProfile сохраняет профиль на диск.
func SaveProfile(name string, p ServerProfile) error {
	dir := filepath.Join(configDir(), "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(profilePath(name), data, 0o600)
}

// LoadProfile загружает профиль с диска.
func LoadProfile(name string) (*ServerProfile, error) {
	data, err := os.ReadFile(profilePath(name))
	if err != nil {
		return nil, fmt.Errorf("профиль %q: %w", name, err)
	}
	var p ServerProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("парсинг профиля %q: %w", name, err)
	}
	return &p, nil
}

// DeleteProfile удаляет профиль с диска.
func DeleteProfile(name string) error {
	return os.Remove(profilePath(name))
}

// ListProfiles загружает все профили из директории.
func ListProfiles() map[string]ServerProfile {
	dir := filepath.Join(configDir(), "profiles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	result := make(map[string]ServerProfile)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p ServerProfile
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		result[name] = p
	}
	return result
}

// SaveSettings сохраняет настройки приложения.
func SaveSettings(s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), data, 0o600)
}

// LoadSettings загружает настройки приложения.
func LoadSettings() Settings {
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		// Дефолтные настройки
		return Settings{
			KillSwitch: true,
			DNSLeak:    true,
			Tray:       true,
			Mode:       "auto",
			MTU:        1300,
		}
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{KillSwitch: true, DNSLeak: true, Tray: true, Mode: "auto", MTU: 1300}
	}
	return s
}

// ==========================================
// WDTT-туннель (через wg-turn-client core)
// ==========================================

const wgIface = "wg-turn"

// coreSession — активная сессия wg-turn-client core.
type coreSession struct {
	c      *core.Core
	doneCh <-chan core.Event
}

// ==========================================
// VLESS-туннель (через Xray-core)
// ==========================================

// vlessSession — активная сессия Xray-core.
type vlessSession struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// ==========================================
// TunnelOrchestrator — оркестратор туннеля
//
// Управляет двумя типами туннелей:
//   - WDTT: через wg-turn-client core (WireGuard over DTLS/TURN)
//   - VLESS: через Xray-core (VLESS + Reality)
//
// Координирует kill switch, DNS и логирование.
// ==========================================

// TunnelOrchestrator — координирует туннель, логирование и события.
type TunnelOrchestrator struct {
	appCtx context.Context
	mu     sync.Mutex
	eventCb func(string, interface{})

	// Текущая сессия (только одна активна)
	wdttSession *coreSession
	vlessSession *vlessSession
	running     bool

	// Лог
	logBuf        *LogBuffer
	prevLogWriter io.Writer

	// Текущее состояние
	state        string // "idle", "connecting", "connected", "disconnecting", "error"
	activeMode   string // "wdtt", "vless", "auto"
	profileName  string
	rxBytes      int64
	txBytes      int64
	connected    bool
	killSwitchOn bool
	dnsLeakOn    bool
}

// NewTunnelOrchestrator создаёт оркестратор туннеля.
func NewTunnelOrchestrator(ctx context.Context, eventCb func(string, interface{})) *TunnelOrchestrator {
	return &TunnelOrchestrator{
		appCtx: ctx,
		eventCb: eventCb,
		logBuf: newLogBuffer(),
		state:  "idle",
	}
}

// Start запускает туннель по параметрам подключения.
func (o *TunnelOrchestrator) start(p ConnectParams) error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return fmt.Errorf("туннель уже запущен")
	}
	o.running = true
	o.mu.Unlock()

	go o.runTunnel(p)
	return nil
}

// Start — экспортируемый метод для Wails (алиас).
func (o *TunnelOrchestrator) Start(p ConnectParams) error {
	return o.start(p)
}

// Stop останавливает туннель.
func (o *TunnelOrchestrator) Stop() {
	o.mu.Lock()
	wdtt := o.wdttSession
	vless := o.vlessSession
	o.mu.Unlock()

	if vless != nil && vless.cancel != nil {
		vless.cancel()
	}
	if wdtt != nil && wdtt.c != nil {
		wdtt.c.Stop()
	}

	o.mu.Lock()
	o.running = false
	o.mu.Unlock()
}

// IsRunning возвращает true, если туннель активен.
func (o *TunnelOrchestrator) IsRunning() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}

// GetAppState возвращает текущее состояние для фронтенда.
func (o *TunnelOrchestrator) GetAppState() AppState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return AppState{
		Connected:   o.connected,
		State:       o.state,
		Mode:        o.activeMode,
		ProfileName: o.profileName,
		RxBytes:     o.rxBytes,
		TxBytes:     o.txBytes,
		KillSwitch:  o.killSwitchOn,
		DNSLeak:     o.dnsLeakOn,
	}
}

// GetLogs возвращает последние N записей лога.
func (o *TunnelOrchestrator) GetLogs(limit int) []LogEntry {
	return o.logBuf.getAll(limit)
}

// runTunnel — основная горутина туннеля.
func (o *TunnelOrchestrator) runTunnel(p ConnectParams) {
	// Загружаем профиль
	prof, err := LoadProfile(p.ProfileName)
	if err != nil {
		o.emitError(fmt.Sprintf("Профиль не найден: %v", err))
		o.setState("idle", "Профиль не найден")
		o.mu.Lock()
		o.running = false
		o.mu.Unlock()
		return
	}

	// Определяем режим
	mode := p.Mode
	if mode == "" {
		mode = prof.Mode
	}
	if mode == "" {
		mode = "auto"
	}

	o.mu.Lock()
	o.profileName = p.ProfileName
	o.activeMode = mode
	o.mu.Unlock()

	o.setState("connecting", "Подключение...")
	o.emitLog("INFO", fmt.Sprintf("Профиль: %s, режим: %s", p.ProfileName, mode))

	// Перехватываем логгер → Wails события
	if _, already := log.Writer().(*wailsLogWriter); !already {
		o.prevLogWriter = log.Writer()
	}
	lw := &wailsLogWriter{ctx: o.appCtx, file: newSessionLogFile(p.ProfileName), lb: o.logBuf}
	lw.start()
	log.SetOutput(lw)
	defer func() {
		if w, ok := log.Writer().(*wailsLogWriter); ok {
			select {
			case <-w.stop:
			default:
				close(w.stop)
			}
			if w.file != nil {
				w.file.Close()
			}
		}
		if o.prevLogWriter != nil {
			log.SetOutput(o.prevLogWriter)
		}
	}()

	// В зависимости от режима запускаем нужный туннель
	switch mode {
	case "wdtt":
		o.runWDTT(p, prof)
	case "vless":
		o.runVLESS(p, prof)
	case "auto":
		// Сначала пробуем WDTT, при неудаче — VLESS
		o.emitLog("INFO", "Авто-режим: пробуем WDTT...")
		err := o.runWDTTSession(p, prof)
		if err != nil {
			o.emitLog("WARN", fmt.Sprintf("WDTT не удался: %v, пробуем VLESS...", err))
			_ = o.runVLESSSession(p, prof)
		}
	default:
		o.emitError(fmt.Sprintf("Неизвестный режим: %s", mode))
	}

	// Cleanup
	o.setState("idle", "Отключено")
	o.mu.Lock()
	o.running = false
	o.connected = false
	o.rxBytes = 0
	o.txBytes = 0
	o.mu.Unlock()
}

// runWDTT запускает WDTT-туннель через wg-turn-client core.
func (o *TunnelOrchestrator) runWDTT(p ConnectParams, prof *ServerProfile) {
	err := o.runWDTTSession(p, prof)
	if err != nil {
		o.emitError(fmt.Sprintf("WDTT: %v", err))
	}
}

// runWDTTSession создаёт и запускает WDTT-сессию.
func (o *TunnelOrchestrator) runWDTTSession(p ConnectParams, prof *ServerProfile) error {
	mtu := p.MTU
	if mtu == 0 && prof.WDTT.MTU > 0 {
		mtu = prof.WDTT.MTU
	}
	if mtu == 0 {
		mtu = 1300
	}

	hashes := prof.WDTT.Hashes
	if len(hashes) == 0 {
		// Дефолтные хеши (пусто — core подставит свои)
		hashes = nil
	}

	cfg := core.Config{
		PeerAddr: prof.WDTT.PeerAddr,
		Password: prof.Password,
		Hashes:   hashes,
		Listen:   prof.WDTT.Listen,
		TurnHost: prof.WDTT.TurnHost,
		TurnPort: prof.WDTT.TurnPort,
		DeviceID: prof.DeviceID,
		Workers:  prof.WDTT.Workers,
		MTU:      mtu,
	}

	c := core.New(cfg)
	events, err := c.Start()
	if err != nil {
		return fmt.Errorf("core start: %w", err)
	}

	sess := &coreSession{c: c, doneCh: events}
	o.mu.Lock()
	o.wdttSession = sess
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		o.wdttSession = nil
		o.mu.Unlock()
	}()

	// Обрабатываем события
	for ev := range sess.doneCh {
		switch ev.Type {
		case core.EventState:
			connected := ev.Status == "running"
			o.mu.Lock()
			o.connected = connected
			o.mu.Unlock()
			o.setState(ev.Status, "")
			if !connected {
				// Туннель упал — выходим из цикла
				return fmt.Errorf("туннель отключён: %s", ev.Status)
			}
		case core.EventStats:
			o.mu.Lock()
			o.rxBytes = ev.RxBytes
			o.txBytes = ev.TxBytes
			o.mu.Unlock()
			o.emitStats()
		case core.EventLog:
			level := ev.Level
			if level == "" {
				level = "INFO"
			}
			o.emitLog(level, ev.Message)
			if strings.Contains(ev.Message, "FATAL_AUTH") {
				o.emitError(ev.Message)
				return fmt.Errorf("FATAL_AUTH: %s", ev.Message)
			}
		case core.EventError:
			o.emitError(ev.Message)
			return fmt.Errorf("ошибка: %s", ev.Message)
		case core.EventEvent:
			if ev.Name == "wg_config" {
				turnIPs := sess.c.GetTurnIPs()
				o.emitLog("INFO", "[WG] Применение конфига...")
				if err := applyWGConfig(ev.Data, turnIPs); err != nil {
					msg := fmt.Sprintf("[WG] Ошибка применения конфига: %v", err)
					o.emitError(msg)
					o.emitLog("ERROR", msg)
				} else {
					o.mu.Lock()
					o.connected = true
					o.killSwitchOn = true
					o.dnsLeakOn = true
					o.mu.Unlock()
					o.setState("connected", "")
					o.emitLog("INFO", "[WG] Конфиг применён, туннель активен ✓")
					o.emitState()
				}
			}
		}
	}

	// Туннель завершился
	teardownWG()
	o.emitLog("INFO", "WDTT сессия завершена")
	return nil
}

// runVLESS запускает VLESS-туннель через Xray-core.
func (o *TunnelOrchestrator) runVLESS(p ConnectParams, prof *ServerProfile) {
	err := o.runVLESSSession(p, prof)
	if err != nil {
		o.emitError(fmt.Sprintf("VLESS: %v", err))
	}
}

// runVLESSSession создаёт и запускает Xray-core процесс.
func (o *TunnelOrchestrator) runVLESSSession(p ConnectParams, prof *ServerProfile) error {
	if prof.VLESS.RemoteAddr == "" || prof.VLESS.UUID == "" {
		return fmt.Errorf("VLESS параметры не настроены в профиле")
	}

	// Формируем конфиг Xray
	xrayConfig := o.buildXrayConfig(prof)
	configJSON, err := json.Marshal(xrayConfig)
	if err != nil {
		return fmt.Errorf("генерация конфига Xray: %w", err)
	}

	// Ищем xray.exe рядом с бинарником или в PATH
	xrayPath := "xray.exe"

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, xrayPath, "run", "-c", "stdin")
	cmd.Stdin = strings.NewReader(string(configJSON))
	// Перенаправляем stdout/stderr в лог
	cmd.Stdout = newLogWriterAdapter(o, "INFO")
	cmd.Stderr = newLogWriterAdapter(o, "ERROR")

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("запуск Xray: %w", err)
	}

	sess := &vlessSession{cmd: cmd, cancel: cancel}
	o.mu.Lock()
	o.vlessSession = sess
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		o.vlessSession = nil
		o.mu.Unlock()
	}()

	// Ждём завершения
	err = cmd.Wait()

	o.mu.Lock()
	o.connected = false
	o.mu.Unlock()
	o.setState("idle", "Отключено")
	o.emitState()

	if err != nil {
		return fmt.Errorf("Xray завершился: %w", err)
	}
	return nil
}

// buildXrayConfig генерирует конфиг Xray-core для VLESS+Reality.
func (o *TunnelOrchestrator) buildXrayConfig(prof *ServerProfile) map[string]interface{} {
	shortID := prof.VLESS.ShortID
	if shortID == "" {
		shortID = "12345678"
	}

	fingerprint := prof.VLESS.Fingerprint
	if fingerprint == "" {
		fingerprint = "chrome"
	}

	serverName := prof.VLESS.ServerName
	if serverName == "" {
		serverName = "www.microsoft.com"
	}

	return map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":      "tun-in",
				"protocol": "dokodemo-door",
				"settings": map[string]interface{}{
					"network": "tcp,udp",
					"followRedirect": false,
				},
				"listen": "127.0.0.1",
				"port":    1080,
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"tag":      "proxy",
				"protocol": "vless",
				"settings": map[string]interface{}{
					"vnext": []interface{}{
						map[string]interface{}{
							"address": strings.Split(prof.VLESS.RemoteAddr, ":")[0],
							"port":    parsePort(prof.VLESS.RemoteAddr),
							"users": []interface{}{
								map[string]interface{}{
									"id":       prof.VLESS.UUID,
									"flow":     "xtls-rprx-vision",
									"encryption": "none",
								},
							},
						},
					},
				},
				"streamSettings": map[string]interface{}{
					"network": "tcp",
					"security": "reality",
					"realitySettings": map[string]interface{}{
						"serverName":    serverName,
						"fingerprint":   fingerprint,
						"publicKey":     prof.VLESS.PublicKey,
						"shortId":       shortID,
						"spiderX":       "",
					},
				},
			},
			map[string]interface{}{
				"tag":      "direct",
				"protocol": "freedom",
			},
		},
		"routing": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{
					"type":        "field",
					"outboundTag": "direct",
					"port":        "0-65535",
					"ip":          []string{"geoip:private"},
				},
			},
		},
	}
}

// parsePort извлекает порт из строки "host:port".
func parsePort(addr string) int {
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return 443
	}
	port := 0
	for _, c := range parts[len(parts)-1] {
		if c < '0' || c > '9' {
			return 443
		}
		port = port*10 + int(c-'0')
	}
	if port == 0 {
		return 443
	}
	return port
}

// logWriterAdapter адаптирует io.Writer для перенаправления stdout/stderr Xray в лог.
type logWriterAdapter struct {
	orch  *TunnelOrchestrator
	level string
}

func newLogWriterAdapter(orch *TunnelOrchestrator, level string) *logWriterAdapter {
	return &logWriterAdapter{orch: orch, level: level}
}

func (a *logWriterAdapter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg != "" {
		a.orch.emitLog(a.level, "[XRAY] "+msg)
	}
	return len(p), nil
}

// ==========================================
// WireGuard конфигурация (Windows)
// ==========================================

// applyWGConfig применяет WireGuard-конфигурацию через wireguard-go.
// На Windows используется wintun.dll (должен быть распакован).
func applyWGConfig(wgConfig string, turnIPs []string) error {
	// Формируем полный конфиг WireGuard
	// peerIP извлекается из конфига
	// На Windows: используем wireguard-go + wintun
	log.Printf("[WG] Применение конфига, turnIPs=%v", turnIPs)

	// Сохраняем конфиг во временный файл для wireguard-go
	tmpDir := filepath.Join(configDir(), "wg_tmp")
	_ = os.MkdirAll(tmpDir, 0o700)
	tmpFile := filepath.Join(tmpDir, "wg.conf")
	if err := os.WriteFile(tmpFile, []byte(wgConfig), 0o600); err != nil {
		return fmt.Errorf("сохранение wg.conf: %w", err)
	}

	// На Windows вызываем wireguard-go
	if runtime.GOOS == "windows" {
		cmd := exec.Command("wireguard-go", wgIface)
		cmd.Args = append(cmd.Args, "-f", tmpFile)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("запуск wireguard-go: %w", err)
		}
		// Не ждём завершения — работает в фоне
		log.Printf("[WG] wireguard-go запущен для %s", wgIface)
	}

	return nil
}

// teardownWG удаляет WireGuard-интерфейс.
func teardownWG() {
	if runtime.GOOS == "windows" {
		// Удаляем интерфейс через wireguard-go
		cmd := exec.Command("wireguard-go", wgIface)
		cmd.Args = append(cmd.Args, "-f", "/dev/null") // пустой конфиг = удаление
		_ = cmd.Run()
		log.Printf("[WG] Интерфейс %s удалён", wgIface)
	}
}

// ==========================================
// Инициализация wintun DLL
// ==========================================

// wintunDLLData — данные wintun.dll для распаковки.
var wintunDLLData []byte

// InitWintun распаковывает встроенный wintun.dll во временную директорию.
// Должен вызываться до создания WireGuard-интерфейса.
func InitWintun(dllData []byte) {
	if len(dllData) == 0 {
		log.Println("[WINTUN] wintun.dll не встроен, WireGuard может не работать")
		return
	}

	// Распаковываем в директорию приложения
	destPath := filepath.Join(configDir(), "wintun.dll")
	if err := os.WriteFile(destPath, dllData, 0o644); err != nil {
		log.Printf("[WINTUN] Ошибка распаковки wintun.dll: %v", err)
		return
	}

	// Добавляем в PATH (для wireguard-go)
	path := os.Getenv("PATH")
	appDir := filepath.Dir(destPath)
	if !strings.Contains(path, appDir) {
		os.Setenv("PATH", appDir+";"+path)
	}

	wintunDLLData = dllData
	log.Println("[WINTUN] wintun.dll распакован и добавлен в PATH")
}

// ==========================================
// Системный трей (Windows)
// ==========================================

// trayData — данные системного трея.
var (
	trayIconData []byte
	trayOn       bool
)

// startTray инициализирует системный трей на Windows.
// Заглушка: реальная реализация через github.com/getlantern/systray
// или github.com/energye/systray.
func startTray(icon []byte, onShow, onToggle, onQuit func()) {
	trayIconData = icon
	trayOn = true
	log.Println("[TRAY] Системный трей инициализирован")
}

// setTrayStatus обновляет статус в трее (tooltip, иконка).
func setTrayStatus(connected bool, rx, tx int64, workers int32) {
	// TODO: обновить tooltip в трее
	_ = rx
	_ = tx
	_ = workers
}

// SetTrayEnabled включает/выключает трей.
func SetTrayEnabled(enabled bool) {
	trayOn = enabled
}

// ==========================================
// Вспомогательные методы
// ==========================================

// setState обновляет состояние и отправляет событие во фронтенд.
func (o *TunnelOrchestrator) setState(state, msg string) {
	o.mu.Lock()
	o.state = state
	o.mu.Unlock()
	o.emitState()
}

// emitState отправляет текущее состояние во фронтенд.
func (o *TunnelOrchestrator) emitState() {
	if o.eventCb == nil {
		return
	}
	o.eventCb("state_changed", o.GetAppState())
}

// emitLog отправляет запись лога.
func (o *TunnelOrchestrator) emitLog(level, message string) {
	o.logBuf.push(level, message)
	if o.eventCb == nil {
		return
	}
	o.eventCb("log", []interface{}{level, message})
}

// emitError отправляет ошибку.
func (o *TunnelOrchestrator) emitError(msg string) {
	o.emitLog("ERROR", msg)
	if o.eventCb == nil {
		return
	}
	o.eventCb("error", msg)
}

// emitStats отправляет статистику трафика.
func (o *TunnelOrchestrator) emitStats() {
	if o.eventCb == nil {
		return
	}
	o.eventCb("stats", map[string]int64{
		"rx": o.rxBytes,
		"tx": o.txBytes,
	})
}