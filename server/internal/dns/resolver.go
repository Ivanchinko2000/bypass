package dns

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Resolver предоставляет DNS-резолвинг с split-horizon:
// - DPI-домены → fake IP из 10.77.0.0/16 (для маркировки трафика)
// - Geo-домены → fake IP из 10.78.0.0/16 (для маркировки трафика)
// - Остальные → реальный upstream DNS
//
// Fake IP подход:
// DNS возвращает клиенту "фейковый" IP из специального диапазона.
// Когда клиент подключается к этому IP, nftables маркирует пакет
// и направляет в нужную routing table.
// Реальный DNS-запрос выполняется параллельно и IP маппится.
type Resolver struct {
	mu          sync.RWMutex
	upstream    []string      // upstream DNS для обычных запросов
	euUpstream  []string      // upstream DNS для geo-запросов (опционально)
	geoDomains  map[string]bool // множество geo-доменов (для быстрого lookup)
	dpiDomains  map[string]bool // множество dpi-доменов
	fakeIPMap   map[string]string // fake IP → real IP (для NAT)
	realIPMap   map[string]string // real IP → fake IP (для ответов)
	dpiCounter  int             // счётчик для DPI fake IP
	geoCounter  int             // счётчик для Geo fake IP
	cache       map[string]*cacheEntry
	cacheTTL    time.Duration
	listen      string
	server      *dns.Server
}

type cacheEntry struct {
	answer  *dns.Msg
	expires time.Time
}

// DPIFakeIPRange — диапазон fake IP для DPI-доменов
const DPIFakeIPRange = "10.77.0.0/16"

// GeoFakeIPRange — диапазон fake IP для Geo-доменов
const GeoFakeIPRange = "10.78.0.0/16"

// NewResolver создаёт DNS-резолвер.
func NewResolver(listen string, upstream, euUpstream []string, cacheTTL int) *Resolver {
	geoUpstream := euUpstream
	if len(geoUpstream) == 0 {
		geoUpstream = upstream
	}

	return &Resolver{
		listen:     listen,
		upstream:   upstream,
		euUpstream: geoUpstream,
		geoDomains: make(map[string]bool),
		dpiDomains: make(map[string]bool),
		fakeIPMap:  make(map[string]string),
		realIPMap:  make(map[string]string),
		cache:      make(map[string]*cacheEntry),
		cacheTTL:   time.Duration(cacheTTL) * time.Second,
	}
}

// SetGeoDomains обновляет набор geo-доменов.
func (r *Resolver) SetGeoDomains(domains []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.geoDomains = make(map[string]bool, len(domains))
	for _, d := range domains {
		r.geoDomains[strings.ToLower(d)] = true
	}
	log.Printf("[DNS] Обновлён geo-список: %d доменов", len(domains))
}

// SetDPIDomains обновляет набор DPI-доменов.
func (r *Resolver) SetDPIDomains(domains []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.dpiDomains = make(map[string]bool, len(domains))
	for _, d := range domains {
		r.dpiDomains[strings.ToLower(d)] = true
	}
	log.Printf("[DNS] Обновлён DPI-список: %d доменов", len(domains))
}

// Start запускает DNS-сервер (UDP + TCP).
func (r *Resolver) Start() error {
	// Создаём DNS handler
	dns.HandleFunc(".", r.handleDNS)

	// UDP сервер
	r.server = &dns.Server{Addr: r.listen, Net: "udp"}
	go func() {
		if err := r.server.ListenAndServe(); err != nil {
			log.Printf("[DNS] UDP сервер ошибка: %v", err)
		}
	}()

	// TCP сервер
	tcpServer := &dns.Server{Addr: r.listen, Net: "tcp"}
	go func() {
		if err := tcpServer.ListenAndServe(); err != nil {
			log.Printf("[DNS] TCP сервер ошибка: %v", err)
		}
	}()

	log.Printf("[DNS] Сервер запущен на %s (UDP+TCP)", r.listen)
	return nil
}

// Stop останавливает DNS-сервер.
func (r *Resolver) Stop() {
	if r.server != nil {
		_ = r.server.Shutdown()
	}
}

// handleDNS обрабатывает DNS-запрос.
func (r *Resolver) handleDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 0 {
		return
	}

	q := req.Question[0]
	domain := strings.ToLower(q.Name)
	domain = strings.TrimSuffix(domain, ".")

	log.Printf("[DNS] Запрос: %s %s", q.Name, dns.TypeToString[q.Qtype])

	// A / AAAA запросы — основной кейс
	if q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA {
		r.mu.RLock()
		isDPI := r.dpiDomains[domain]
		isGeo := r.geoDomains[domain]
		r.mu.RUnlock()

		switch {
		case isGeo:
			r.handleFakeIP(w, req, domain, GeoFakeIPRange, q.Qtype)
			return
		case isDPI:
			r.handleFakeIP(w, req, domain, DPIFakeIPRange, q.Qtype)
			return
		}
	}

	// Обычный запрос — проксируем в upstream
	r.proxyRequest(w, req)
}

// handleFakeIP возвращает fake IP и параллельно резолвит реальный IP.
func (r *Resolver) handleFakeIP(w dns.ResponseWriter, req *dns.Msg, domain, fakeRange string, qtype uint16) {
	// Генерируем fake IP
	fakeIP := r.allocateFakeIP(domain, fakeRange)

	// Проверяем кэш реального IP
	r.mu.RLock()
	realIP, hasReal := r.realIPMap[domain]
	r.mu.RUnlock()

	if !hasReal {
		// Асинхронно резолвим реальный IP
		go r.resolveRealIP(domain)
	}

	// Формируем ответ с fake IP
	resp := new(dns.Msg)
	resp.SetReply(req)

	if qtype == dns.TypeA && fakeIP != "" {
		rr, err := dns.NewRR(fmt.Sprintf("%s 300 IN A %s", q.Name, fakeIP))
		if err == nil {
			resp.Answer = append(resp.Answer, rr)
		}
	}

	_ = w.WriteMsg(resp)

	listType := "DPI"
	if fakeRange == GeoFakeIPRange {
		listType = "Geo"
	}
	log.Printf("[DNS] %s домен %s → fake IP %s (real: %s)", listType, domain, fakeIP, realIP)
}

// allocateFakeIP выделяет fake IP для домена из указанного диапазона.
func (r *Resolver) allocateFakeIP(domain, fakeRange string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Проверяем, уже есть ли fake IP для этого домена
	for fake, real := range r.fakeIPMap {
		if real == domain {
			return fake
		}
	}

	// Генерируем новый fake IP
	var baseIP net.IP
	var counter *int

	switch fakeRange {
	case DPIFakeIPRange:
		baseIP = net.ParseIP("10.77.0.0").To4()
		counter = &r.dpiCounter
	case GeoFakeIPRange:
		baseIP = net.ParseIP("10.78.0.0").To4()
		counter = &r.geoCounter
	default:
		return ""
	}

	*counter++
	ip := make(net.IP, len(baseIP))
	copy(ip, baseIP)

	// 10.X.0.N где N = counter (0-255 в третьем октете, или разбиваем дальше)
	ip[2] = byte((*counter - 1) / 256)
	ip[3] = byte((*counter - 1) % 256)

	fake := ip.String()

	r.fakeIPMap[fake] = domain
	r.realIPMap[domain] = fake

	return fake
}

// resolveRealIP выполняет реальный DNS-запрос для домена и кэширует результат.
// Результат используется в NAT-таблице для подмены fake IP → real IP.
func (r *Resolver) resolveRealIP(domain string) {
	r.mu.RLock()
	upstream := r.upstream
	r.mu.RUnlock()

	if len(upstream) == 0 {
		upstream = []string{"1.1.1.1"}
	}

	// Пробуем каждый upstream
	for _, server := range upstream {
		realIP, err := dnsLookup(domain, server)
		if err != nil {
			log.Printf("[DNS] Ошибка резолвинга %s через %s: %v", domain, server, err)
			continue
		}

		r.mu.Lock()
		r.realIPMap[domain] = realIP
		r.mu.Unlock()

		log.Printf("[DNS] Реальный IP %s = %s", domain, realIP)
		return
	}

	log.Printf("[DNS] Не удалось зарезолвить %s ни через один upstream", domain)
}

// proxyRequest проксирует запрос в upstream DNS.
func (r *Resolver) proxyRequest(w dns.ResponseWriter, req *dns.Msg) {
	r.mu.RLock()
	upstream := r.upstream
	r.mu.RUnlock()

	if len(upstream) == 0 {
		upstream = []string{"1.1.1.1"}
	}

	// Проверяем кэш
	cacheKey := req.Question[0].Name + dns.TypeToString[req.Question[0].Qtype]
	if cached, ok := r.getCache(cacheKey); ok {
		resp := cached.Copy()
		resp.Id = req.Id
		_ = w.WriteMsg(resp)
		return
	}

	for _, server := range upstream {
		resp, err := dnsExchange(req, server)
		if err != nil {
			continue
		}

		// Кэшируем
		r.setCache(cacheKey, resp)

		_ = w.WriteMsg(resp)
		return
	}

	// Ошибка — возвращаем SERVFAIL
	resp := new(dns.Msg)
	resp.SetRcode(req, dns.RcodeServerFailure)
	_ = w.WriteMsg(resp)
}

// dnsLookup выполняет A-запрос к DNS-серверу, возвращает IP как строку.
func dnsLookup(domain, server string) (string, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)

	resp, err := dnsExchange(m, server)
	if err != nil {
		return "", err
	}

	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			return a.A.String(), nil
		}
	}

	return "", fmt.Errorf("нет A-записи")
}

// dnsExchange отправляет DNS-запрос к серверу.
func dnsExchange(m *dns.Msg, server string) (*dns.Msg, error) {
	// Убираем порт если есть
	addr := server
	if strings.Contains(server, ":") {
		parts := strings.SplitN(server, ":", 2)
		addr = parts[0] + ":" + parts[1]
	} else {
		addr = server + ":53"
	}

	c := new(dns.Client)
	resp, _, err := c.Exchange(m, addr)
	return resp, err
}

// Cache helpers
func (r *Resolver) getCache(key string) (*dns.Msg, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.cache[key]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return entry.answer, true
}

func (r *Resolver) setCache(key string, msg *dns.Msg) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache[key] = &cacheEntry{
		answer:  msg,
		expires: time.Now().Add(r.cacheTTL),
	}
}

// GetFakeIPMap возвращает копию маппинга fake IP → domain.
// Используется для отладки.
func (r *Resolver) GetFakeIPMap() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]string, len(r.fakeIPMap))
	for k, v := range r.fakeIPMap {
		result[k] = v
	}
	return result
}

// Stats возвращает JSON со статистикой DNS.
func (r *Resolver) Stats() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := map[string]interface{}{
		"dpi_domains":  len(r.dpiDomains),
		"geo_domains":  len(r.geoDomains),
		"fake_ip_map":  len(r.fakeIPMap),
		"cache_entries": len(r.cache),
	}
	data, _ := json.MarshalIndent(stats, "", "  ")
	return string(data)
}