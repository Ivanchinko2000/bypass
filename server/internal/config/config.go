package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load загружает конфигурацию из YAML-файла.
func Load(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение конфига %s: %w", path, err)
	}

	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("парсинг конфига %s: %w", path, err)
	}

	// Устанавливаем значения по умолчанию
	setDefaults(&cfg)

	// Валидация
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("валидация конфига: %w", err)
	}

	return &cfg, nil
}

// setDefaults устанавливает значения по умолчанию для неуказанных полей.
func setDefaults(cfg *ServerConfig) {
	// Режим по умолчанию
	if cfg.Mode == "" {
		cfg.Mode = "rf"
	}

	// WireGuard defaults
	if cfg.WireGuard.InterfaceName == "" {
		cfg.WireGuard.InterfaceName = "bypass0"
	}
	if cfg.WireGuard.Subnet == "" {
		cfg.WireGuard.Subnet = "10.66.0.0/16"
	}
	if cfg.WireGuard.ServerIP == "" {
		cfg.WireGuard.ServerIP = "10.66.66.1"
	}
	if cfg.WireGuard.MTU == 0 {
		cfg.WireGuard.MTU = 1280
	}
	if cfg.WireGuard.Keepalive == 0 {
		cfg.WireGuard.Keepalive = 25
	}
	if cfg.WireGuard.InternalPort == 0 {
		cfg.WireGuard.InternalPort = 56001
	}

	// WDTT defaults (для РФ-сервера)
	if cfg.Mode == "rf" && cfg.WDTT.DTLSAddr == "" {
		cfg.WDTT.DTLSAddr = ":56000"
	}

	// VLESS defaults
	if cfg.VLESS.Server.Listen == "" {
		cfg.VLESS.Server.Listen = ":443"
	}
	if cfg.Zapret.QNum == 0 {
		cfg.Zapret.QNum = 200
	}

	// DNS defaults
	if cfg.DNS.Listen == "" {
		cfg.DNS.Listen = ":53"
	}
	if len(cfg.DNS.Upstream) == 0 {
		cfg.DNS.Upstream = []string{"1.1.1.1", "8.8.8.8"}
	}
	if cfg.DNS.CacheTTL == 0 {
		cfg.DNS.CacheTTL = 300
	}

	// API defaults
	if cfg.API.Listen == "" {
		cfg.API.Listen = ":8080"
	}

	// Lists defaults
	if cfg.Lists.DPIBlocklist == "" {
		cfg.Lists.DPIBlocklist = "configs/dpi_blocklist.txt"
	}
	if cfg.Lists.GeoBlocklist == "" {
		cfg.Lists.GeoBlocklist = "configs/geo_blocklist.txt"
	}

	// Users file default
	if cfg.UsersFile == "" {
		cfg.UsersFile = "configs/users.json"
	}

	// Log defaults
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	// SOCKS5 для маршрутизации к Xray
	if cfg.Listen.SOCKS5Addr == "" {
		cfg.Listen.SOCKS5Addr = "127.0.0.1:1080"
	}
}

// validate проверяет корректность конфигурации.
func validate(cfg *ServerConfig) error {
	// Режим
	if cfg.Mode != "rf" && cfg.Mode != "eu" {
		return fmt.Errorf("некорректный режим: %s (допустимо: rf, eu)", cfg.Mode)
	}

	// WireGuard
	if cfg.WireGuard.PrivateKey == "" {
		return fmt.Errorf("не указан wireguard.private_key")
	}

	// РФ-сервер: WDTT
	if cfg.Mode == "rf" && cfg.WDTT.Enabled {
		if cfg.WDTT.Password == "" {
			return fmt.Errorf("не указан wdtt.password")
		}
	}

	// ЕС-сервер: VLESS сервер
	if cfg.Mode == "eu" {
		if cfg.VLESS.Server.PrivateKey == "" {
			return fmt.Errorf("не указан vless.server.private_key")
		}
		if len(cfg.VLESS.Server.ServerNames) == 0 {
			return fmt.Errorf("не указаны vless.server.server_names")
		}
	}

	// РФ-сервер: VLESS клиент к ЕС
	if cfg.Mode == "rf" && cfg.VLESS.Client.Enabled {
		if cfg.VLESS.Client.RemoteAddr == "" {
			return fmt.Errorf("не указан vless.client.remote_addr")
		}
		if cfg.VLESS.Client.UUID == "" {
			return fmt.Errorf("не указан vless.client.uuid")
		}
		if cfg.VLESS.Client.PublicKey == "" {
			return fmt.Errorf("не указан vless.client.public_key")
		}
	}

	// Zapret
	if cfg.Mode == "rf" && cfg.Zapret.Enabled {
		if cfg.Zapret.NfqwsPath == "" {
			return fmt.Errorf("не указан zapret.nfqws_path (обычно /usr/bin/nfqws)")
		}
	}

	return nil
}

// DefaultConfig возвращает конфигурацию по умолчанию (для генерации шаблона).
func DefaultConfig() *ServerConfig {
	cfg := &ServerConfig{}
	setDefaults(cfg)
	return cfg
}

// SaveTemplate сохраняет шаблон конфигурации в файл.
func SaveTemplate(path string) error {
	cfg := DefaultConfig()
	cfg.Mode = "rf"
	cfg.WDTT.Enabled = true
	cfg.VLESS.Client.Enabled = true
	cfg.Zapret.Enabled = true
	cfg.DNS.Enabled = true
	cfg.API.Enabled = true

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("маршалинг YAML: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("запись шаблона: %w", err)
	}

	return nil
}