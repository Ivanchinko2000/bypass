package vless

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Manager управляет Xray-core процессом.
// На РФ-сервере: запускает Xray как VLESS+Reality клиент к ЕС-серверу,
// предоставляя SOCKS5 inbound для маршрутизации geo-трафика.
// На ЕС-сервере: запускает Xray как VLESS+Reality сервер.
type Manager struct {
	mu          sync.Mutex
	role        string // "client" (РФ) или "server" (ЕС)
	xrayPath    string // путь к Xray-core бинарнику
	configPath  string // путь к сгенерированному конфигу Xray
	cmd         *exec.Cmd
	running     bool
	socks5Addr  string // локальный SOCKS5 адрес (для РФ: inbound, для ЕС: не используется)
	externalCfg XrayExternalConfig // конфигурация из server.yaml
}

// XrayExternalConfig — конфигурация VLESS из server.yaml.
type XrayExternalConfig struct {
	Role   string
	Server struct {
		Listen      string
		PrivateKey  string
		ShortID     string
		Dest        string
		ServerNames []string
	}
	Client struct {
		Enabled     bool
		RemoteAddr  string
		UUID        string
		PublicKey   string
		ShortID     string
		Fingerprint string
		ServerName  string
	}
}

// NewManager создаёт менеджер VLESS/Xray.
func NewManager(role, xrayPath, configDir string, cfg XrayExternalConfig, socks5Addr string) *Manager {
	configPath := filepath.Join(configDir, "xray_config.json")
	return &Manager{
		role:        role,
		xrayPath:    xrayPath,
		configPath:  configPath,
		socks5Addr:  socks5Addr,
		externalCfg: cfg,
	}
}

// Start генерирует конфиг Xray и запускает процесс.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		log.Printf("[XRAY] Уже запущен")
		return nil
	}

	// Проверяем бинарник
	if _, err := os.Stat(m.xrayPath); os.IsNotExist(err) {
		return fmt.Errorf("xray-core не найден: %s", m.xrayPath)
	}

	// Генерируем конфиг
	var xrayCfg interface{}
	var err error

	switch m.role {
	case "client":
		xrayCfg, err = m.generateClientConfig()
	case "server":
		xrayCfg, err = m.generateServerConfig()
	default:
		return fmt.Errorf("неизвестная роль: %s", m.role)
	}

	if err != nil {
		return fmt.Errorf("генерация конфига: %w", err)
	}

	// Записываем конфиг
	configData, err := json.MarshalIndent(xrayCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("маршалинг конфига: %w", err)
	}

	if err := os.WriteFile(m.configPath, configData, 0o600); err != nil {
		return fmt.Errorf("запись конфига: %w", err)
	}

	log.Printf("[XRAY] Конфиг записан: %s", m.configPath)

	// Запускаем Xray
	log.Printf("[XRAY] Запуск xray-core: %s -c %s", m.xrayPath, m.configPath)

	m.cmd = exec.Command(m.xrayPath, "-c", m.configPath)
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("запуск xray-core: %w", err)
	}

	m.running = true

	// Следим за процессом
	go m.watchdog()

	log.Printf("[XRAY] Запущен (PID %d), роль: %s", m.cmd.Process.Pid, m.role)
	return nil
}

// Stop останавливает Xray процесс.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		return nil
	}

	log.Printf("[XRAY] Остановка (PID %d)...", m.cmd.Process.Pid)

	if err := m.cmd.Process.Kill(); err != nil {
		log.Printf("[XRAY] Kill ошибка: %v", err)
	}

	m.running = false
	return nil
}

// IsRunning возвращает статус.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// GetConfigPath возвращает путь к конфигу Xray.
func (m *Manager) GetConfigPath() string {
	return m.configPath
}

// watchdog следит за процессом.
func (m *Manager) watchdog() {
	err := m.cmd.Wait()
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
	log.Printf("[XRAY] Процесс завершён: %v", err)
}

// ==========================================
// Генерация конфигов Xray
// ==========================================

// generateClientConfig генерирует конфиг для РФ-сервера (VLESS+Reality клиент → ЕС).
// Предоставляет SOCKS5 inbound для маршрутизации geo-трафика.
func (m *Manager) generateClientConfig() (interface{}, error) {
	cfg := m.externalCfg

	// Парсим remote addr
	remoteAddr := cfg.Client.RemoteAddr
	// Если есть порт — разделяем на address и port
	address := remoteAddr
	port := "443"
	for i := len(remoteAddr) - 1; i >= 0; i-- {
		if remoteAddr[i] == ':' {
			address = remoteAddr[:i]
			port = remoteAddr[i+1:]
			break
		}
	}

	xrayCfg := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":      "socks",
				"protocol": "socks",
				"listen":   "127.0.0.1",
				"port":     m.parsePort(m.socks5Addr),
				"settings": map[string]interface{}{
					"auth":   "noauth",
					"udp":    true,
					"ip":     "127.0.0.1",
				},
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"tag":      "vless-eu",
				"protocol": "vless",
				"settings": map[string]interface{}{
					"vnext": []interface{}{
						map[string]interface{}{
							"address": address,
							"port":    port,
							"users": []interface{}{
								map[string]interface{}{
									"id":       cfg.Client.UUID,
									"encryption": "none",
									"flow":       "xtls-rprx-vision",
								},
							},
						},
					},
					"decryption": "none",
				},
				"streamSettings": map[string]interface{}{
					"network":  "tcp",
					"security": "reality",
					"realitySettings": map[string]interface{}{
						"serverName":    cfg.Client.ServerName,
						"fingerprint":   cfg.Client.Fingerprint,
						"publicKey":     cfg.Client.PublicKey,
						"shortId":       cfg.Client.ShortID,
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
			"domainStrategy": "IPIfNonMatch",
			"rules": []interface{}{
				map[string]interface{}{
					"type":        "field",
					"outboundTag": "vless-eu",
					"port":        "0-65535",
				},
			},
		},
	}

	return xrayCfg, nil
}

// generateServerConfig генерирует конфиг для ЕС-сервера (VLESS+Reality сервер).
func (m *Manager) generateServerConfig() (interface{}, error) {
	cfg := m.externalCfg

	xrayCfg := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"tag":      "vless-in",
				"protocol": "vless",
				"listen":   cfg.Server.Listen,
				"settings": map[string]interface{}{
					"clients": []interface{}{
						map[string]interface{}{
							"id":       "auto-gen-uuid", // будет заменён при генерации
							"flow":     "xtls-rprx-vision",
						},
					},
					"decryption": "none",
				},
				"streamSettings": map[string]interface{}{
					"network":  "tcp",
					"security": "reality",
					"realitySettings": map[string]interface{}{
						"dest":         cfg.Server.Dest,
						"serverNames":  cfg.Server.ServerNames,
						"privateKey":   cfg.Server.PrivateKey,
						"shortIds":     []string{cfg.Server.ShortID},
					},
				},
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"tag":      "direct",
				"protocol": "freedom",
			},
		},
	}

	return xrayCfg, nil
}

// parsePort извлекает порт из адреса "host:port".
func (m *Manager) parsePort(addr string) int {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			port := 0
			for _, c := range addr[i+1:] {
				port = port*10 + int(c-'0')
			}
			return port
		}
	}
	return 1080
}