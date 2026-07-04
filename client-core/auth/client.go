// Package auth — клиент аутентификации для подключения к серверу.
// Отправляет пароль + deviceID на /api/auth, получает конфигурацию
// (маршрутные списки, параметры WireGuard, UUID VLESS).
// Сохраняет конфигурацию локально для оффлайн-доступа.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// ServerConfig — конфигурация, полученная от сервера после авторизации.
type ServerConfig struct {
	// Mode — режим сервера ("rf" или "eu")
	Mode string `json:"mode"`

	// ServerIP — адрес DTLS для WDTT-подключения (формат "host:port")
	ServerIP string `json:"server_ip"`

	// WGMTU — MTU для WireGuard
	WGMTU int `json:"wg_mtu"`

	// VLESSUUID — UUID для VLESS-подключения
	VLESSUUID string `json:"vless_uuid"`

	// VLESSAddr — адрес VLESS-сервера (host:port), если предоставлен сервером
	VLESSAddr string `json:"vless_addr,omitempty"`

	// RealityPubKey — Reality публичный ключ для VLESS
	RealityPubKey string `json:"reality_pub_key,omitempty"`

	// RealityShortID — Reality shortId для VLESS
	RealityShortID string `json:"reality_short_id,omitempty"`

	// RealitySNI — SNI для камуфляжа VLESS
	RealitySNI string `json:"reality_sni,omitempty"`

	// DPIDomains — список DPI-блокируемых доменов
	DPIDomains []string `json:"dpi_domains"`

	// GeoDomains — список гео-блокируемых доменов
	GeoDomains []string `json:"geo_domains"`
}

// AuthResponse — ответ сервера на /api/auth.
type AuthResponse struct {
	Status string       `json:"status"`
	UserID string       `json:"user_id"`
	Config ServerConfig `json:"config"`
}

// LocalConfig — конфигурация, сохранённая локально.
type LocalConfig struct {
	// ServerURL — базовый URL API сервера
	ServerURL string `json:"server_url"`

	// UserID — идентификатор пользователя
	UserID string `json:"user_id"`

	// DeviceID — идентификатор устройства
	DeviceID string `json:"device_id"`

	// Password — пароль (хранится локально для повторных подключений)
	Password string `json:"password"`

	// ServerConfig — последняя полученная конфигурация
	ServerConfig ServerConfig `json:"config"`

	// LastAuth — время последней успешной авторизации
	LastAuth time.Time `json:"last_auth"`
}

// Client — клиент аутентификации.
type Client struct {
	// serverURL — базовый URL сервера (например "https://1.2.3.4:8080")
	serverURL string

	// deviceID — уникальный идентификатор устройства
	deviceID string

	// configDir — директория для хранения локальной конфигурации
	configDir string

	// httpClient — HTTP-клиент с таймаутами
	httpClient *http.Client

	// localConfig — загруженная локальная конфигурация
	localConfig *LocalConfig
}

// ClientConfig — конфигурация клиента аутентификации.
type ClientConfig struct {
	// ServerURL — базовый URL API сервера
	ServerURL string

	// DeviceID — идентификатор устройства (если пусто — генерируется UUID)
	DeviceID string

	// ConfigDir — директория для хранения конфигурации.
	// По умолчанию: <OS Config Dir>/bypassvpn
	ConfigDir string

	// RequestTimeout — таймаут HTTP-запросов (по умолчанию 10 сек)
	RequestTimeout time.Duration
}

// NewClient создаёт клиент аутентификации.
func NewClient(cfg ClientConfig) *Client {
	if cfg.ConfigDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = os.Getenv("HOME")
			if base == "" {
				base = os.Getenv("APPDATA")
				if base == "" {
					base = "."
				}
			}
		}
		cfg.ConfigDir = filepath.Join(base, "bypassvpn")
	}

	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.New().String()
	}

	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 10 * time.Second
	}

	c := &Client{
		serverURL: cfg.ServerURL,
		deviceID:  cfg.DeviceID,
		configDir: cfg.ConfigDir,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
			// Не следуем редиректам (безопасность)
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("редиректы запрещены")
			},
		},
	}

	// Пробуем загрузить сохранённую конфигурацию
	c.loadLocalConfig()

	return c
}

// Authenticate отправляет запрос на авторизацию к серверу.
// Возвращает полученную конфигурацию и сохраняет её локально.
func (c *Client) Authenticate(ctx context.Context, password string) (*ServerConfig, error) {
	if c.serverURL == "" {
		return nil, fmt.Errorf("URL сервера не указан")
	}

	log.Printf("[AUTH] Авторизация: сервер=%s, device=%s", c.serverURL, c.deviceID)

	// Формируем запрос
	reqBody := map[string]string{
		"password":  password,
		"device_id": c.deviceID,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации запроса: %w", err)
	}

	url := c.serverURL + "/api/auth"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BypassVPN/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса к серверу: %w", err)
	}
	defer resp.Body.Close()

	// Читаем тело ответа
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // до 1 МБ
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	// Проверяем статус-код
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, &AuthError{
				StatusCode: resp.StatusCode,
				Message:    errResp.Error,
			}
		}
		return nil, &AuthError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("сервер вернул статус %d", resp.StatusCode),
		}
	}

	// Парсим ответ
	var authResp AuthResponse
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return nil, fmt.Errorf("ошибка парсинга ответа: %w", err)
	}

	if authResp.Status != "ok" {
		return nil, fmt.Errorf("сервер вернул статус: %s", authResp.Status)
	}

	// Сохраняем конфигурацию локально
	localCfg := &LocalConfig{
		ServerURL:   c.serverURL,
		UserID:      authResp.UserID,
		DeviceID:    c.deviceID,
		Password:    password,
		ServerConfig: authResp.Config,
		LastAuth:    time.Now(),
	}
	if err := c.saveLocalConfig(localCfg); err != nil {
		log.Printf("[AUTH] Предупреждение: не удалось сохранить конфигурацию: %v", err)
	}

	c.localConfig = localCfg
	log.Printf("[AUTH] Успешная авторизация: user=%s, mode=%s", authResp.UserID, authResp.Config.Mode)

	return &authResp.Config, nil
}

// GetLocalConfig возвращает последнюю сохранённую конфигурацию.
// Возвращает nil, если конфигурация не найдена.
func (c *Client) GetLocalConfig() *ServerConfig {
	if c.localConfig == nil {
		return nil
	}
	return &c.localConfig.ServerConfig
}

// GetDeviceID возвращает идентификатор устройства.
func (c *Client) GetDeviceID() string {
	return c.deviceID
}

// GetUserID возвращает идентификатор пользователя (из последней авторизации).
func (c *Client) GetUserID() string {
	if c.localConfig == nil {
		return ""
	}
	return c.localConfig.UserID
}

// GetServerURL возвращает URL сервера.
func (c *Client) GetServerURL() string {
	return c.serverURL
}

// SetServerURL устанавливает URL сервера.
func (c *Client) SetServerURL(url string) {
	c.serverURL = url
}

// SavePassword сохраняет пароль локально для автоматического подключения.
func (c *Client) SavePassword(password string) error {
	if c.localConfig == nil {
		c.localConfig = &LocalConfig{
			ServerURL: c.serverURL,
			DeviceID:  c.deviceID,
		}
	}
	c.localConfig.Password = password
	return c.saveLocalConfig(c.localConfig)
}

// GetSavedPassword возвращает сохранённый пароль.
func (c *Client) GetSavedPassword() string {
	if c.localConfig == nil {
		return ""
	}
	return c.localConfig.Password
}

// ClearLocalConfig удаляет локальную конфигурацию.
func (c *Client) ClearLocalConfig() error {
	c.localConfig = nil
	configPath := c.configPath()
	if _, err := os.Stat(configPath); err == nil {
		return os.Remove(configPath)
	}
	return nil
}

// configPath возвращает путь к файлу локальной конфигурации.
func (c *Client) configPath() string {
	return filepath.Join(c.configDir, "config.json")
}

// loadLocalConfig загружает конфигурацию из локального файла.
func (c *Client) loadLocalConfig() {
	configPath := c.configPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return // файла нет или ошибка чтения — это нормально
	}

	var cfg LocalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("[AUTH] Ошибка парсинга локальной конфигурации: %v", err)
		return
	}

	c.localConfig = &cfg
	c.deviceID = cfg.DeviceID // восстанавливаем DeviceID из сохранённой конфигурации

	// Восстанавливаем serverURL из сохранённого, если не был передан
	if c.serverURL == "" && cfg.ServerURL != "" {
		c.serverURL = cfg.ServerURL
	}

	log.Printf("[AUTH] Локальная конфигурация загружена: user=%s", cfg.UserID)
}

// saveLocalConfig сохраняет конфигурацию в локальный файл.
func (c *Client) saveLocalConfig(cfg *LocalConfig) error {
	if err := os.MkdirAll(c.configDir, 0o700); err != nil {
		return fmt.Errorf("создание директории: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("сериализация: %w", err)
	}

	return os.WriteFile(c.configPath(), data, 0o600)
}

// AuthError — ошибка аутентификации с детальной информацией.
type AuthError struct {
	// StatusCode — HTTP статус-код ответа
	StatusCode int

	// Message — сообщение об ошибке
	Message string
}

// Error реализует интерфейс error.
func (e *AuthError) Error() string {
	return fmt.Sprintf("авторизация (HTTP %d): %s", e.StatusCode, e.Message)
}

// Unwrap для совместимости с errors.Is/As.
func (e *AuthError) Unwrap() error {
	return nil
}

// IsWrongPassword проверяет, является ли ошибка неверным паролем.
func (e *AuthError) IsWrongPassword() bool {
	return e.Message == "DENIED:wrong_password"
}

// IsExpired проверяет, истёк ли срок действия пароля.
func (e *AuthError) IsExpired() bool {
	return e.Message == "DENIED:expired"
}

// IsDeviceMismatch проверяет, привязан ли пароль к другому устройству.
func (e *AuthError) IsDeviceMismatch() bool {
	return e.Message == "DENIED:device_mismatch"
}

// IsFatalAuthError проверяет, является ли ошибка фатальной (нельзя реконнектить).
func (e *AuthError) IsFatalAuthError() bool {
	return e.IsWrongPassword() || e.IsExpired() || e.IsDeviceMismatch()
}