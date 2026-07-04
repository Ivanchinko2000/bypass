// Package routing — менеджер доменных списков для сплит-туннелирования.
// Загружает DPI и Geo списки с сервера API или из локальных файлов,
// классифицирует домены по типу блокировки (DPI / Geo / Direct),
// предоставляет потокобезопасный доступ с поддержкой автоперезагрузки.
package routing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DomainClass — классификация домена для маршрутизации.
type DomainClass string

const (
	// ClassDPI — домен блокируется DPI (YouTube, Instagram, и т.д.).
	// Трафик пускается через WDTT-туннель к РФ-серверу.
	ClassDPI DomainClass = "dpi"

	// ClassGeo — домен недоступен географически (ChatGPT, Claude, и т.д.).
	// Трафик пускается через VLESS к ЕС-серверу.
	ClassGeo DomainClass = "geo"

	// ClassDirect — домен доступен напрямую, без туннеля.
	ClassDirect DomainClass = "direct"
)

// Entry представляет одну запись в списке маршрутизации.
// Поддерживает домены (включая wildcard *.example.com) и IP/CIDR.
type Entry struct {
	// Original — исходная строка из файла или API
	Original string

	// IsIP — true если это IP/CIDR, false если домен
	IsIP bool

	// Domain — нормализованный домен (нижний регистр, без *.префикса)
	Domain string

	// DomainSuffix — суффикс для match по окончанию (включая ведущую точку)
	DomainSuffix string

	// PrefixNet — IP-префикс (если IsIP == true)
	PrefixNet netip.Prefix
}

// Manager управляет доменными списками для сплит-туннелирования.
// Потокобезопасен через sync.RWMutex.
// Поддерживает загрузку из локального файла или через HTTP API сервера.
type Manager struct {
	mu sync.RWMutex

	// dpiEntries — список доменов, блокируемых DPI (YouTube и т.д.)
	dpiEntries []Entry

	// geoEntries — список доменов, блокируемых географически (ChatGPT и т.д.)
	geoEntries []Entry

	// serverURL — базовый URL сервера API (например "https://1.2.3.4:8080")
	serverURL string

	// localDir — директория для локального кэша списков
	localDir string

	// autoReloadInterval — интервал автоперезагрузки (0 = отключено)
	autoReloadInterval time.Duration

	// cancelAutoReload — функция отмены таймера автоперезагрузки
	cancelAutoReload context.CancelFunc

	// lastLoad — время последней успешной загрузки
	lastLoad time.Time
}

// ManagerConfig — конфигурация менеджера списков.
type ManagerConfig struct {
	// ServerURL — базовый URL сервера API.
	// Списки загружаются через GET /api/lists
	ServerURL string

	// LocalDir — директория для локального кэша.
	// Если ServerURL недоступен, списки читаются из файлов:
	//   - <LocalDir>/dpi_domains.txt
	//   - <LocalDir>/geo_domains.txt
	LocalDir string

	// AutoReloadInterval — интервал автоперезагрузки (например 30*time.Minute).
	// При 0 автоперезагрузка отключена.
	AutoReloadInterval time.Duration
}

// NewManager создаёт менеджер доменных списков.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		serverURL:          cfg.ServerURL,
		localDir:           cfg.LocalDir,
		autoReloadInterval: cfg.AutoReloadInterval,
	}
}

// Load загружает списки из API сервера (при наличии) или из локальных файлов.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Пытаемся загрузить с сервера
	if m.serverURL != "" {
		err := m.loadFromServer()
		if err != nil {
			log.Printf("[СПИСКИ] Не удалось загрузить с сервера %s: %v, используем локальный кэш", m.serverURL, err)
		} else {
			m.lastLoad = time.Now()
			log.Printf("[СПИСКИ] Загружено с сервера: DPI=%d, Geo=%d", len(m.dpiEntries), len(m.geoEntries))
			return nil
		}
	}

	// Fallback: загружаем из локальных файлов
	err := m.loadFromLocal()
	if err != nil {
		return fmt.Errorf("ошибка загрузки списков: %w", err)
	}

	m.lastLoad = time.Now()
	log.Printf("[СПИСКИ] Загружено из кэша: DPI=%d, Geo=%d", len(m.dpiEntries), len(m.geoEntries))
	return nil
}

// loadFromServer загружает списки через HTTP API.
func (m *Manager) loadFromServer() error {
	url := strings.TrimRight(m.serverURL, "/") + "/api/lists"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("создание запроса: %w", err)
	}
	req.Header.Set("User-Agent", "BypassVPN/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("запрос к серверу: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("сервер вернул статус %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // до 10 МБ
	if err != nil {
		return fmt.Errorf("чтение ответа: %w", err)
	}

	var result struct {
		DPI []string `json:"dpi"`
		Geo []string `json:"geo"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("парсинг JSON: %w", err)
	}

	// Парсим домены в записи
	m.dpiEntries = parseDomainList(result.DPI)
	m.geoEntries = parseDomainList(result.Geo)

	// Сохраняем в локальный кэш
	_ = m.saveToLocalCache()

	return nil
}

// loadFromLocal загружает списки из локальных файлов.
func (m *Manager) loadFromLocal() error {
	dpiPath := filepath.Join(m.localDir, "dpi_domains.txt")
	geoPath := filepath.Join(m.localDir, "geo_domains.txt")

	if data, err := os.ReadFile(dpiPath); err == nil {
		lines := parseLines(string(data))
		m.dpiEntries = parseDomainList(lines)
	}

	if data, err := os.ReadFile(geoPath); err == nil {
		lines := parseLines(string(data))
		m.geoEntries = parseDomainList(lines)
	}

	return nil
}

// saveToLocalCache сохраняет текущие списки в локальные файлы.
func (m *Manager) saveToLocalCache() error {
	if m.localDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.localDir, 0o755); err != nil {
		return err
	}

	// Сохраняем DPI-список
	dpiPath := filepath.Join(m.localDir, "dpi_domains.txt")
	dpiLines := make([]string, len(m.dpiEntries))
	for i, e := range m.dpiEntries {
		dpiLines[i] = e.Original
	}
	if err := os.WriteFile(dpiPath, []byte(strings.Join(dpiLines, "\n")+"\n"), 0o644); err != nil {
		return err
	}

	// Сохраняем Geo-список
	geoPath := filepath.Join(m.localDir, "geo_domains.txt")
	geoLines := make([]string, len(m.geoEntries))
	for i, e := range m.geoEntries {
		geoLines[i] = e.Original
	}
	return os.WriteFile(geoPath, []byte(strings.Join(geoLines, "\n")+"\n"), 0o644)
}

// LoadDomainsDirect загружает списки напрямую из строк (например, из ответа API /api/auth).
func (m *Manager) LoadDomainsDirect(dpiDomains, geoDomains []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.dpiEntries = parseDomainList(dpiDomains)
	m.geoEntries = parseDomainList(geoDomains)
	m.lastLoad = time.Now()

	_ = m.saveToLocalCache()

	log.Printf("[СПИСКИ] Загружено напрямую: DPI=%d, Geo=%d", len(m.dpiEntries), len(m.geoEntries))
}

// ClassifyDomain классифицирует домен по спискам маршрутизации.
// Возвращает "dpi", "geo" или "direct".
func (m *Manager) ClassifyDomain(domain string) DomainClass {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lowerDomain := strings.ToLower(domain)

	// Geo-список приоритетнее: если ресурс в обоих списках,
	// лучше отправить через ЕС (нет DPI-проблем)
	for _, e := range m.geoEntries {
		if e.IsIP {
			continue
		}
		if domainMatches(lowerDomain, e) {
			return ClassGeo
		}
	}

	// DPI-список
	for _, e := range m.dpiEntries {
		if e.IsIP {
			continue
		}
		if domainMatches(lowerDomain, e) {
			return ClassDPI
		}
	}

	return ClassDirect
}

// ClassifyIP классифицирует IP-адрес по спискам маршрутизации.
func (m *Manager) ClassifyIP(ip netip.Addr) DomainClass {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Geo-список приоритетнее
	for _, e := range m.geoEntries {
		if !e.IsIP {
			continue
		}
		if e.PrefixNet.Contains(ip) {
			return ClassGeo
		}
	}

	// DPI-список
	for _, e := range m.dpiEntries {
		if !e.IsIP {
			continue
		}
		if e.PrefixNet.Contains(ip) {
			return ClassDPI
		}
	}

	return ClassDirect
}

// GetDPIDomains возвращает копию текущего DPI-списка доменов.
func (m *Manager) GetDPIDomains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.dpiEntries))
	for _, e := range m.dpiEntries {
		if !e.IsIP {
			result = append(result, e.Original)
		}
	}
	return result
}

// GetGeoDomains возвращает копию текущего Geo-списка доменов.
func (m *Manager) GetGeoDomains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.geoEntries))
	for _, e := range m.geoEntries {
		if !e.IsIP {
			result = append(result, e.Original)
		}
	}
	return result
}

// Stats возвращает количество записей в каждом списке.
func (m *Manager) Stats() (dpiCount, geoCount int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.dpiEntries), len(m.geoEntries)
}

// LastLoad возвращает время последней загрузки.
func (m *Manager) LastLoad() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastLoad
}

// StartAutoReload запускает фоновую автоперезагрузку списков.
func (m *Manager) StartAutoReload() {
	if m.autoReloadInterval <= 0 {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAutoReload = cancel

	go func() {
		ticker := time.NewTicker(m.autoReloadInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Printf("[СПИСКИ] Автоперезагрузка...")
				if err := m.Load(); err != nil {
					log.Printf("[СПИСКИ] Ошибка автоперезагрузки: %v", err)
				}
			}
		}
	}()

	log.Printf("[СПИСКИ] Автоперезагрузка включена (каждые %v)", m.autoReloadInterval)
}

// StopAutoReload останавливает фоновую автоперезагрузку.
func (m *Manager) StopAutoReload() {
	if m.cancelAutoReload != nil {
		m.cancelAutoReload()
		m.cancelAutoReload = nil
		log.Printf("[СПИСКИ] Автоперезагрузка отключена")
	}
}

// SetServerURL обновляет URL сервера для загрузки списков.
func (m *Manager) SetServerURL(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serverURL = url
}

// domainMatches проверяет, подходит ли домен под запись списка.
// Поддерживает точное совпадение и match по поддоменам (suffix).
func domainMatches(domain string, e Entry) bool {
	// Точное совпадение
	if domain == e.Domain {
		return true
	}

	// Поддомен (suffix match): "www.youtube.com" содержит ".youtube.com"
	if strings.HasSuffix(domain, e.DomainSuffix) {
		return true
	}

	return false
}

// parseDomainList парсит массив строк в массив Entry.
func parseDomainList(domains []string) []Entry {
	entries := make([]Entry, 0, len(domains))
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		entry, err := parseEntry(d)
		if err != nil {
			log.Printf("[СПИСКИ] Пропуск записи %q: %v", d, err)
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// parseEntry парсит строку в Entry.
// Поддерживает форматы:
//   - "*.youtube.com" или "youtube.com" → домен с wildcard
//   - "1.2.3.0/24" → CIDR
//   - "1.2.3.4" → одиночный IP
func parseEntry(s string) (Entry, error) {
	e := Entry{Original: s}

	// Пробуем как IP/CIDR
	if prefix, err := netip.ParsePrefix(s); err == nil {
		e.IsIP = true
		e.PrefixNet = prefix
		return e, nil
	}

	// Пробуем как одиночный IP
	if ip, err := netip.ParseAddr(s); err == nil {
		e.IsIP = true
		e.PrefixNet = netip.PrefixFrom(ip, ip.BitLen())
		return e, nil
	}

	// Это домен (возможно с wildcard: *.youtube.com)
	domain := strings.TrimPrefix(s, "*.")
	domain = strings.ToLower(domain)
	e.Domain = domain
	e.DomainSuffix = "." + domain

	return e, nil
}

// parseLines разбивает многострочную строку на массив непустых строк без комментариев.
func parseLines(s string) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}