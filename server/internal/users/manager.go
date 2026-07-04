package users

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// User представляет одного пользователя системы.
// Хранится в users.json на сервере.
type User struct {
	// ID — уникальный идентификатор пользователя (например "friend_ivan")
	ID string `json:"id"`

	// Password — пароль для WDTT аутентификации (хэшируется при хранении)
	Password string `json:"password"`

	// DeviceID — привязка к устройству (опционально, пустое = не привязан)
	DeviceID string `json:"device_id,omitempty"`

	// VLESSUUID — UUID для VLESS аутентификации
	VLESSUUID string `json:"vless_uuid"`

	// WGPublicKey — публичный ключ WireGuard клиента
	WGPublicKey string `json:"wg_public_key,omitempty"`

	// WGIP — назначенный IP в WireGuard подсети (например "10.66.66.2")
	WGIP string `json:"wg_ip"`

	// Active — флаг активности (можно отключить без удаления)
	Active bool `json:"active"`

	// Created — дата создания
	Created string `json:"created"`

	// Expires — дата истечения (пустое = бессрочно)
	Expires string `json:"expires,omitempty"`

	// Description — комментарий (например "Иван, телефон")
	Description string `json:"description,omitempty"`

	// TrafficUsedMB — использованный трафик (МБ), обновляется периодически
	TrafficUsedMB float64 `json:"traffic_used_mb,omitempty"`

	// LastSeen — последний раз онлайн
	LastSeen string `json:"last_seen,omitempty"`
}

// usersFile — структура JSON-файла пользователей.
type usersFile struct {
	Users []User `json:"users"`
}

// Manager управляет списком пользователей.
// Потокобезопасен через sync.RWMutex.
type Manager struct {
	mu    sync.RWMutex
	path  string
	users map[string]*User // key = ID
}

// NewManager создаёт менеджер пользователей и загружает данные из файла.
func NewManager(path string) (*Manager, error) {
	m := &Manager{
		path:  path,
		users: make(map[string]*User),
	}

	if err := m.load(); err != nil {
		// Если файл не существует — это нормально, создаём пустой
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("загрузка пользователей: %w", err)
		}
		// Создаём пустой файл
		if err := m.save(); err != nil {
			return nil, fmt.Errorf("создание файла пользователей: %w", err)
		}
	}

	return m, nil
}

// load загружает пользователей из JSON-файла.
func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}

	var uf usersFile
	if err := json.Unmarshal(data, &uf); err != nil {
		return fmt.Errorf("парсинг users.json: %w", err)
	}

	m.users = make(map[string]*User, len(uf.Users))
	for i := range uf.Users {
		u := &uf.Users[i]
		m.users[u.ID] = u
	}

	return nil
}

// save сохраняет пользователей в JSON-файл.
func (m *Manager) save() error {
	users := make([]User, 0, len(m.users))
	for _, u := range m.users {
		users = append(users, *u)
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return fmt.Errorf("маршалинг users: %w", err)
	}

	if err := os.WriteFile(m.path, data, 0o600); err != nil {
		return fmt.Errorf("запись users.json: %w", err)
	}

	return nil
}

// AuthenticateByPassword проверяет пароль для WDTT аутентификации.
// Возвращает пользователя или ошибку.
func (m *Manager) AuthenticateByPassword(password, deviceID string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, u := range m.users {
		if !u.Active {
			continue
		}

		// Проверяем пароль
		if u.Password != password {
			continue
		}

		// Проверяем привязку к устройству
		if u.DeviceID != "" && u.DeviceID != deviceID {
			return nil, fmt.Errorf("device_mismatch")
		}

		// Проверяем срок действия
		if u.Expires != "" {
			expires, err := time.Parse("2006-01-02", u.Expires)
			if err == nil && time.Now().After(expires) {
				return nil, fmt.Errorf("expired")
			}
		}

		return u, nil
	}

	return nil, fmt.Errorf("wrong_password")
}

// AuthenticateByUUID проверяет UUID для VLESS аутентификации.
func (m *Manager) AuthenticateByUUID(uuid string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, u := range m.users {
		if !u.Active {
			continue
		}
		if u.VLESSUUID == uuid {
			// Проверяем срок действия
			if u.Expires != "" {
				expires, err := time.Parse("2006-01-02", u.Expires)
				if err == nil && time.Now().After(expires) {
					return nil, fmt.Errorf("expired")
				}
			}
			return u, nil
		}
	}

	return nil, fmt.Errorf("unknown_uuid")
}

// GetByWGKey находит пользователя по публичному ключу WireGuard.
func (m *Manager) GetByWGKey(pubKey string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, u := range m.users {
		if u.WGPublicKey == pubKey && u.Active {
			return u, nil
		}
	}

	return nil, fmt.Errorf("unknown_peer")
}

// Get возвращает пользователя по ID.
func (m *Manager) Get(id string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.users[id]
	if !ok {
		return nil, fmt.Errorf("пользователь %s не найден", id)
	}
	return u, nil
}

// List возвращает всех пользователей.
func (m *Manager) List() []User {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]User, 0, len(m.users))
	for _, u := range m.users {
		result = append(result, *u)
	}
	return result
}

// Add добавляет нового пользователя.
func (m *Manager) Add(u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.users[u.ID]; exists {
		return fmt.Errorf("пользователь %s уже существует", u.ID)
	}

	if u.Created == "" {
		u.Created = time.Now().Format("2006-01-02")
	}

	u.TrafficUsedMB = 0
	m.users[u.ID] = &u

	return m.save()
}

// Update обновляет данные пользователя.
func (m *Manager) Update(id string, updateFn func(*User)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("пользователь %s не найден", id)
	}

	updateFn(u)
	return m.save()
}

// Delete удаляет пользователя.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.users[id]; !ok {
		return fmt.Errorf("пользователь %s не найден", id)
	}

	delete(m.users, id)
	return m.save()
}

// UpdateTraffic обновляет счётчик трафика пользователя.
func (m *Manager) UpdateTraffic(id string, additionalMB float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.users[id]; ok {
		u.TrafficUsedMB += additionalMB
	}
}

// UpdateLastSeen обновляет время последнего подключения.
func (m *Manager) UpdateLastSeen(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, ok := m.users[id]; ok {
		u.LastSeen = time.Now().Format("2006-01-02 15:04:05")
	}
}

// Count возвращает количество активных пользователей.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, u := range m.users {
		if u.Active {
			count++
		}
	}
	return count
}

// GetWGPeerConfigs возвращает конфигурации WireGuard peer'ов для всех активных пользователей.
func (m *Manager) GetWGPeerConfigs() []WGPeerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	configs := make([]WGPeerConfig, 0)
	for _, u := range m.users {
		if u.Active && u.WGPublicKey != "" && u.WGIP != "" {
			configs = append(configs, WGPeerConfig{
				PublicKey:  u.WGPublicKey,
				AllowedIPs: u.WGIP + "/32",
			})
		}
	}
	return configs
}

// WGPeerConfig — конфигурация одного WireGuard peer'а.
type WGPeerConfig struct {
	PublicKey  string `json:"public_key"`
	AllowedIPs string `json:"allowed_ips"`
}