package lists

import (
	"bufio"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

// ListType — тип списка блокировок.
type ListType int

const (
	// DPI — список ресурсов, блокируемых DPI (YouTube, Instagram...)
	DPI ListType = iota
	// Geo — список сервисов, недоступных в РФ (ChatGPT, Claude...)
	Geo
)

// String возвращает строковое представление типа списка.
func (lt ListType) String() string {
	switch lt {
	case DPI:
		return "dpi"
	case Geo:
		return "geo"
	default:
		return "unknown"
	}
}

// Entry представляет одну запись в списке.
// Может быть доменом (с wildcard) или IP/CIDR.
type Entry struct {
	// Original — исходная строка из файла
	Original string

	// IsIP — true если это IP/CIDR, false если домен
	IsIP bool

	// Domain — домен (если !IsIP)
	Domain string

	// Prefix — домен с wildcard (если есть) для быстрого match
	DomainSuffix string

	// PrefixNet — IP-префикс (если IsIP)
	PrefixNet netip.Prefix
}

// Manager управляет списками DPI и Geo блокировок.
// Потокобезопасен через sync.RWMutex.
type Manager struct {
	mu       sync.RWMutex
	dpiPath  string
	geoPath  string
	dpiList  []Entry
	geoList  []Entry
	dpiNFT   *nftablesSet // nftables set для DPI-доменов
	geoNFT   *nftablesSet // nftables set для Geo-доменов
	lastLoad time.Time
}

// NewManager создаёт менеджер списков.
func NewManager(dpiPath, geoPath string) *Manager {
	return &Manager{
		dpiPath: dpiPath,
		geoPath: geoPath,
	}
}

// Load загружает оба списка из файлов.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Загружаем DPI-список
	if m.dpiPath != "" {
		entries, err := loadListFile(m.dpiPath)
		if err != nil {
			log.Printf("[СПИСКИ] Ошибка загрузки DPI-списка %s: %v", m.dpiPath, err)
		} else {
			m.dpiList = entries
			log.Printf("[СПИСКИ] Загружено %d записей DPI-списка", len(entries))
		}
	}

	// Загружаем Geo-список
	if m.geoPath != "" {
		entries, err := loadListFile(m.geoPath)
		if err != nil {
			log.Printf("[СПИСКИ] Ошибка загрузки Geo-списка %s: %v", m.geoPath, err)
		} else {
			m.geoList = entries
			log.Printf("[СПИСКИ] Загружено %d записей Geo-списка", len(entries))
		}
	}

	m.lastLoad = time.Now()
	return nil
}

// Reload перезагружает списки из файлов (вызывается по SIGHUP).
func (m *Manager) Reload() error {
	log.Printf("[СПИСКИ] Перезагрузка списков...")
	return m.Load()
}

// loadListFile загружает и парсит один файл списка.
// Формат: по одному домену/IP на строку.
// Пустые строки и строки начинающиеся с # — игнорируются.
// Поддерживаются wildcard: *.youtube.com
func loadListFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Пропускаем пустые строки и комментарии
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, err := parseEntry(line)
		if err != nil {
			log.Printf("[СПИСКИ] Строка %d: %v (пропуск)", lineNum, err)
			continue
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("чтение файла: %w", err)
	}

	return entries, nil
}

// parseEntry парсит строку в Entry.
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

	// Это домен (возможно с wildcard)
	domain := strings.TrimPrefix(s, "*.")
	e.Domain = domain

	// Для wildcard: suffx для match по окончанию
	if strings.HasPrefix(s, "*.") {
		e.DomainSuffix = "." + domain
	} else {
		e.DomainSuffix = "." + domain
		e.Domain = domain
	}

	return e, nil
}

// ClassifyDomain определяет тип блокировки домена.
// Возвращает DPI, Geo или -1 если домен не в списках.
func (m *Manager) ClassifyDomain(domain string) ListType {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lowerDomain := strings.ToLower(domain)

	// Проверяем Geo-список (приоритетнее — если ресурс и в DPI, и в Geo,
	// лучше отправить через ЕС, так как там нет DPI проблем)
	for _, e := range m.geoList {
		if e.IsIP {
			continue // пропускаем IP при поиске по домену
		}
		if domainMatches(lowerDomain, e) {
			return Geo
		}
	}

	// Проверяем DPI-список
	for _, e := range m.dpiList {
		if e.IsIP {
			continue
		}
		if domainMatches(lowerDomain, e) {
			return DPI
		}
	}

	return -1
}

// ClassifyIP определяет тип блокировки IP-адреса.
func (m *Manager) ClassifyIP(ip netip.Addr) ListType {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Проверяем Geo-список
	for _, e := range m.geoList {
		if !e.IsIP {
			continue
		}
		if e.PrefixNet.Contains(ip) {
			return Geo
		}
	}

	// Проверяем DPI-список
	for _, e := range m.dpiList {
		if !e.IsIP {
			continue
		}
		if e.PrefixNet.Contains(ip) {
			return DPI
		}
	}

	return -1
}

// domainMatches проверяет, подходит ли домен под запись списка.
func domainMatches(domain string, e Entry) bool {
	// Точное совпадение
	if domain == e.Domain {
		return true
	}

	// Поддомен (suffix match)
	if strings.HasSuffix(domain, e.DomainSuffix) {
		return true
	}

	return false
}

// GetDPIList возвращает копию DPI-списка.
func (m *Manager) GetDPIList() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Entry, len(m.dpiList))
	copy(result, m.dpiList)
	return result
}

// GetGeoList возвращает копию Geo-списка.
func (m *Manager) GetGeoList() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Entry, len(m.geoList))
	copy(result, m.geoList)
	return result
}

// GetDPIDomains возвращает только домены из DPI-списка (без IP).
func (m *Manager) GetDPIDomains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var domains []string
	for _, e := range m.dpiList {
		if !e.IsIP {
			domains = append(domains, e.Original)
		}
	}
	return domains
}

// GetGeoDomains возвращает только домены из Geo-списка (без IP).
func (m *Manager) GetGeoDomains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var domains []string
	for _, e := range m.geoList {
		if !e.IsIP {
			domains = append(domains, e.Original)
		}
	}
	return domains
}

// nftablesSet — заглушка для интеграции с nftables.
// Реальная реализация будет через exec.Command("nft", ...).
type nftablesSet struct {
	name string
}

// DPIListPath возвращает путь к файлу DPI-списка.
func (m *Manager) DPIListPath() string {
	return m.dpiPath
}

// GeoListPath возвращает путь к файлу Geo-списка.
func (m *Manager) GeoListPath() string {
	return m.geoPath
}

// LastLoad возвращает время последней загрузки.
func (m *Manager) LastLoad() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.lastLoad
}

// Stats возвращает статистику по спискам.
func (m *Manager) Stats() (dpiCount, geoCount int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.dpiList), len(m.geoList)
}