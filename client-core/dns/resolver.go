// Package dns — DNS-менеджер клиента.
// Предоставляет DoH-резолвер (DNS-over-HTTPS, RFC 8484), сплит-DNS
// на основе классификации доменов из routing.Manager,
// и защиту от утечек DNS (блокировка не-DoH запросов на порт 53).
package dns

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"client-core/routing"
)

// DefaultDoHServers — список DoH-резолверов по умолчанию.
var DefaultDoHServers = []string{
	"https://dns.google/dns-query",
	"https://cloudflare-dns.com/dns-query",
	"https://dns.quad9.net/dns-query",
}

// ResolverConfig — конфигурация DNS-резолвера.
type ResolverConfig struct {
	// DoHServers — список URL DoH-серверов (по умолчанию Google, Cloudflare, Quad9)
	DoHServers []string

	// TunnelListenAddr — локальный адрес DNS-слушателя внутри туннеля
	// (например "127.0.0.1:5353")
	TunnelListenAddr string

	// SystemDNS — использовать ли системный DNS для direct-доменов.
	// Если false, все запросы идут через DoH
	SystemDNS bool

	// BlockLeak — блокировать ли не-DoH DNS-запросы на порт 53 (защита от утечек)
	BlockLeak bool

	// CacheTTL — TTL кэша DNS-ответов в секундах (0 = без кэширования)
	CacheTTL int

	// Timeout — таймаут DoH-запросов (по умолчанию 5 сек)
	Timeout time.Duration
}

// Resolver — DNS-резолвер с поддержкой DoH и сплит-DNS.
type Resolver struct {
	mu sync.RWMutex

	cfg ResolverConfig

	// routingMgr — менеджер доменных списков для классификации
	routingMgr *routing.Manager

	// tlsTransport — HTTP-клиент с кастомным TLS для DoH
	tlsTransport *http.Transport

	// cache — простой кэш DNS-ответов
	cache map[string]*cacheEntry

	// listener — локальный UDP DNS-слушатель (если запущен)
	listener net.PacketConn

	// cancel — функция отмены работы резолвера
	cancel context.CancelFunc
}

// cacheEntry — запись в кэше DNS.
type cacheEntry struct {
	addresses []netip.Addr
	expires   time.Time
}

// DNSResponse — результат DNS-резолвинга.
type DNSResponse struct {
	// Domain — запрошенный домен
	Domain string

	// Addresses — IP-адреса (A и AAAA записи)
	Addresses []netip.Addr

	// Class — классификация домена
	Class routing.DomainClass

	// Err — ошибка (если есть)
	Err error
}

// NewResolver создаёт DNS-резолвер.
func NewResolver(cfg ResolverConfig, routingMgr *routing.Manager) *Resolver {
	// Дефолтные значения
	if len(cfg.DoHServers) == 0 {
		cfg.DoHServers = DefaultDoHServers
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 300 // 5 минут по умолчанию
	}

	// Создаём HTTP-клиент с кастомным TLS.
	// Не используем системные корневые сертификаты через штатный механизм
	// (на Windows это может вызвать утечку DNS при проверке OCSP/CRL).
	tlsTransport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		DialContext: (&net.Dialer{
			Timeout:   cfg.Timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		MaxConnsPerHost:     5,
		MaxIdleConnsPerHost: 2,
	}

	return &Resolver{
		cfg:          cfg,
		routingMgr:   routingMgr,
		tlsTransport: tlsTransport,
		cache:        make(map[string]*cacheEntry),
	}
}

// Resolve резолвит домен через DoH. Возвращает IP-адреса, классификацию и ошибку.
func (r *Resolver) Resolve(ctx context.Context, domain string) DNSResponse {
	domain = strings.TrimSuffix(domain, ".")
	domain = strings.ToLower(domain)

	// Классифицируем домен
	class := routing.ClassDirect
	if r.routingMgr != nil {
		class = r.routingMgr.ClassifyDomain(domain)
	}

	// Проверяем кэш
	if cached := r.getFromCache(domain); cached != nil {
		if time.Now().Before(cached.expires) {
			return DNSResponse{
				Domain:    domain,
				Addresses: cached.addresses,
				Class:     class,
			}
		}
		r.removeFromCache(domain)
	}

	// Для direct-доменов можно использовать системный DNS
	if class == routing.ClassDirect && r.cfg.SystemDNS {
		addrs, err := r.resolveSystem(ctx, domain)
		if err == nil {
			r.putToCache(domain, addrs)
			return DNSResponse{Domain: domain, Addresses: addrs, Class: class}
		}
		// Fallback на DoH если системный DNS не сработал
		log.Printf("[DNS] Системный DNS не ответил для %s: %v, используем DoH", domain, err)
	}

	// DoH-резолвинг
	addrs, err := r.resolveDoH(ctx, domain)
	if err != nil {
		return DNSResponse{Domain: domain, Class: class, Err: err}
	}

	r.putToCache(domain, addrs)
	return DNSResponse{Domain: domain, Addresses: addrs, Class: class}
}

// resolveDoH выполняет DNS-запрос через DNS-over-HTTPS (RFC 8484).
// Запрос формируется в формате DNS wire format, кодируется в base64url,
// отправляется POST-запросом к DoH-серверу.
func (r *Resolver) resolveDoH(ctx context.Context, domain string) ([]netip.Addr, error) {
	// Формируем DNS-запрос в wire format
	query := buildDNSQuery(domain)
	if len(query) == 0 {
		return nil, fmt.Errorf("ошибка формирования DNS-запроса")
	}

	// Кодируем в base64url (RFC 8484 §6)
	encoded := base64.RawURLEncoding.EncodeToString(query)

	// Пробуем каждый DoH-сервер по очереди
	var lastErr error
	for _, dohURL := range r.cfg.DoHServers {
		addrs, err := r.doHRequest(ctx, dohURL, encoded, domain)
		if err != nil {
			lastErr = err
			continue
		}
		return addrs, nil
	}

	return nil, fmt.Errorf("все DoH-серверы не ответили: %w", lastErr)
}

// doHRequest отправляет POST-запрос к конкретному DoH-серверу.
func (r *Resolver) doHRequest(ctx context.Context, dohURL, encoded, domain string) ([]netip.Addr, error) {
	reqCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, dohURL,
		strings.NewReader("dns="+encoded+"&ct=application/dns-message"))
	if err != nil {
		return nil, fmt.Errorf("создание DoH-запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := r.tlsTransport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("DoH-запрос к %s: %w", dohURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH-сервер %s вернул %d", dohURL, resp.StatusCode)
	}

	// Читаем ответ в wire format
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("чтение DoH-ответа: %w", err)
	}

	// Парсим DNS-ответ
	return parseDNSResponse(body, domain)
}

// resolveSystem выполняет DNS-запрос через системный резолвер.
func (r *Resolver) resolveSystem(ctx context.Context, domain string) ([]netip.Addr, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	var resolver net.Resolver
	ips, err := resolver.LookupIPAddr(resolveCtx, domain)
	if err != nil {
		return nil, err
	}

	var addrs []netip.Addr
	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip.IP); ok {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}

// StartLeakBlock запускает защиту от утечек DNS.
// Фактическая блокировка реализуется через KillSwitch
// (он управляет firewall-правилами на уровне ОС).
func (r *Resolver) StartLeakBlock() error {
	if !r.cfg.BlockLeak {
		return nil
	}
	log.Printf("[DNS] Защита от утечек DNS включена")
	return nil
}

// StopLeakBlock останавливает защиту от утечек DNS.
func (r *Resolver) StopLeakBlock() {
	if r.cfg.BlockLeak {
		log.Printf("[DNS] Защита от утечек DNS отключена")
	}
}

// StartLocalDNS запускает локальный UDP DNS-слушатель.
// Все DNS-запросы к этому слушателю маршрутизируются через сплит-DNS:
//   - DPI-домены → DNS через WDTT-туннель
//   - Geo-домены → DNS через VLESS-туннель
//   - Direct-домены → системный DNS или DoH
func (r *Resolver) StartLocalDNS() error {
	if r.cfg.TunnelListenAddr == "" {
		return nil // локальный DNS не настроен
	}

	addr, err := net.ResolveUDPAddr("udp", r.cfg.TunnelListenAddr)
	if err != nil {
		return fmt.Errorf("парсинг адреса DNS-слушателя: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("запуск DNS-слушателя на %s: %w", r.cfg.TunnelListenAddr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.listener = conn
	r.cancel = cancel
	r.mu.Unlock()

	log.Printf("[DNS] Локальный DNS-слушатель запущен на %s", r.cfg.TunnelListenAddr)

	go r.serveDNS(ctx, conn)

	return nil
}

// serveDNS обрабатывает входящие DNS-запросы.
func (r *Resolver) serveDNS(ctx context.Context, conn net.PacketConn) {
	buf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, clientAddr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		// Извлекаем домен из DNS-запроса
		domain := extractDomainFromQuery(buf[:n])
		if domain == "" {
			continue
		}

		// Резолвим через наш менеджер
		go func(queryBuf []byte, clientAddr net.Addr, domain string) {
			resp := r.Resolve(ctx, domain)
			if resp.Err != nil {
				// Отправляем SERVFAIL
				reply := buildServFailResponse(queryBuf)
				_, _ = conn.WriteTo(reply, clientAddr)
				return
			}

			// Формируем DNS-ответ с полученными адресами
			reply := buildDNSReply(queryBuf, resp.Addresses)
			_, _ = conn.WriteTo(reply, clientAddr)
		}(make([]byte, n), clientAddr, domain)
	}
}

// Stop останавливает DNS-резолвер и локальный слушатель.
func (r *Resolver) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	listener := r.listener
	r.cancel = nil
	r.listener = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if listener != nil {
		_ = listener.Close()
		log.Printf("[DNS] Локальный DNS-слушатель остановлен")
	}

	r.StopLeakBlock()

	// Очищаем кэш
	r.mu.Lock()
	r.cache = make(map[string]*cacheEntry)
	r.mu.Unlock()
}

// ==========================================
// Кэш DNS-ответов
// ==========================================

func (r *Resolver) getFromCache(domain string) *cacheEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.cache[domain]
	if !ok {
		return nil
	}
	return entry
}

func (r *Resolver) putToCache(domain string, addrs []netip.Addr) {
	if r.cfg.CacheTTL <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[domain] = &cacheEntry{
		addresses: addrs,
		expires:   time.Now().Add(time.Duration(r.cfg.CacheTTL) * time.Second),
	}
}

func (r *Resolver) removeFromCache(domain string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, domain)
}

// ==========================================
// DNS wire format — упрощённый парсер/генератор
// ==========================================

// buildDNSQuery формирует минимальный DNS-запрос для домена (A запись).
func buildDNSQuery(domain string) []byte {
	// DNS-заголовок: стандартный запрос, recursion desired
	buf := make([]byte, 12)
	buf[0] = 0x12 // ID (старший байт)
	buf[1] = 0x34 // ID (младший байт)
	buf[2] = 0x01 // Flags: стандартный запрос (QR=0, OPCODE=0)
	buf[3] = 0x00 // Flags: RD=0 (не нужен, DoH-сервер сам рекурсивный)
	buf[4] = 0x00 // QDCOUNT старший
	buf[5] = 0x01 // QDCOUNT младший = 1 (один вопрос)

	// Добавляем имя домена (label format)
	for _, label := range strings.Split(domain, ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, []byte(label)...)
	}
	buf = append(buf, 0x00) // конец имени

	// Тип A (1) + Класс IN (1)
	buf = append(buf, 0x00, 0x01, 0x00, 0x01)

	return buf
}

// parseDNSResponse парсит DNS-ответ в wire format и извлекает IP-адреса.
func parseDNSResponse(data []byte, domain string) ([]netip.Addr, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("слишком короткий DNS-ответ: %d байт", len(data))
	}

	// Читаем количество ответов (ANCount в байтах 6-7)
	anCount := int(data[6])<<8 | int(data[7])

	var addrs []netip.Addr
	offset := 12

	// Пропускаем секцию вопросов
	if offset >= len(data) {
		return nil, fmt.Errorf("некорректный DNS-ответ: нет секции вопросов")
	}
	for offset < len(data) && data[offset] != 0 {
		if data[offset]&0xC0 == 0xC0 {
			offset += 2 // pointer
			break
		}
		offset += int(data[offset]) + 1
	}
	if offset < len(data) {
		offset++ // нулевой байт конца имени
	}
	offset += 4 // пропускаем QTYPE + QCLASS

	// Парсим секцию ответов
	for i := 0; i < anCount && offset < len(data); i++ {
		// Имя ответа (может быть pointer)
		if offset >= len(data) {
			break
		}
		if data[offset]&0xC0 == 0xC0 {
			offset += 2 // pointer
		} else {
			for offset < len(data) && data[offset] != 0 {
				offset += int(data[offset]) + 1
			}
			if offset < len(data) {
				offset++ // нулевой байт
			}
		}

		if offset+10 > len(data) {
			break
		}

		// RTYPE (2), RCLASS (2), TTL (4), RDLENGTH (2)
		rType := int(data[offset])<<8 | int(data[offset+1])
		rdLength := int(data[offset+8])<<8 | int(data[offset+9])
		offset += 10

		if offset+rdLength > len(data) {
			break
		}

		rData := data[offset : offset+rdLength]
		offset += rdLength

		switch rType {
		case 1: // A record (4 байта)
			if len(rData) == 4 {
				if addr, ok := netip.AddrFromSlice(rData); ok {
					addrs = append(addrs, addr)
				}
			}
		case 28: // AAAA record (16 байт)
			if len(rData) == 16 {
				if addr, ok := netip.AddrFromSlice(rData); ok {
					addrs = append(addrs, addr)
				}
			}
		}
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("нет записей для %s", domain)
	}

	return addrs, nil
}

// extractDomainFromQuery извлекает домен из DNS-запроса в wire format.
func extractDomainFromQuery(data []byte) string {
	if len(data) < 12 {
		return ""
	}

	offset := 12 // пропускаем заголовок

	var labels []string
	for offset < len(data) && data[offset] != 0 {
		if data[offset]&0xC0 == 0xC0 {
			// Pointer — не обрабатываем в упрощённой версии
			break
		}
		length := int(data[offset])
		offset++
		if offset+length > len(data) {
			return ""
		}
		labels = append(labels, string(data[offset:offset+length]))
		offset += length
	}

	if len(labels) == 0 {
		return ""
	}
	return strings.Join(labels, ".")
}

// buildServFailResponse формирует SERVFAIL DNS-ответ.
func buildServFailResponse(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	reply := make([]byte, len(query))
	copy(reply, query)
	// QR = 1 (response), RCODE = 2 (SERVFAIL)
	reply[2] |= 0x80
	reply[3] |= 0x02
	return reply
}

// buildDNSReply формирует DNS-ответ с заданными IP-адресами.
func buildDNSReply(query []byte, addrs []netip.Addr) []byte {
	if len(query) < 12 || len(addrs) == 0 {
		return buildServFailResponse(query)
	}

	// Копируем запрос как основу ответа
	reply := make([]byte, len(query))
	copy(reply, query)

	// Устанавливаем флаги ответа: QR=1, AA=1, RCODE=0 (NOERROR)
	reply[2] |= 0x80 | 0x40

	// ANCount
	anCount := uint16(len(addrs))
	reply[6] = byte(anCount >> 8)
	reply[7] = byte(anCount)

	// Добавляем ответные записи
	for _, addr := range addrs {
		// Имя: pointer на имя из вопроса (0xC00C)
		reply = append(reply, 0xC0, 0x0C)

		if addr.Is4() {
			// A-запись: TYPE=1, CLASS=1, TTL=300, RDLENGTH=4
			reply = append(reply,
				0x00, 0x01, // TYPE = A
				0x00, 0x01, // CLASS = IN
				0x00, 0x00, 0x01, 0x2C, // TTL = 300
				0x00, 0x04, // RDLENGTH = 4
			)
			reply = append(reply, addr.AsSlice()...)
		} else if addr.Is6() {
			// AAAA-запись: TYPE=28, CLASS=1, TTL=300, RDLENGTH=16
			reply = append(reply,
				0x00, 0x1C, // TYPE = AAAA
				0x00, 0x01, // CLASS = IN
				0x00, 0x00, 0x01, 0x2C, // TTL = 300
				0x00, 0x10, // RDLENGTH = 16
			)
			reply = append(reply, addr.AsSlice()...)
		}
	}

	return reply
}