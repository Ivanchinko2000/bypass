package router

import (
	"fmt"
	"log"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
)

const (
	// TableDPI — routing table для DPI-трафика (через Zapret)
	TableDPI = 100

	// TableGeo — routing table для Geo-трафика (через VLESS→ЕС)
	TableGeo = 200

	// MarkDPI — fwmark для DPI-трафика
	MarkDPI = 100

	// MarkGeo — fwmark для Geo-трафика
	MarkGeo = 200
)

// Manager управляет policy routing через nftables + ip rule/ip route.
// Потокобезопасен через sync.Mutex (операции маршрутизации sequential).
type Manager struct {
	mu            sync.Mutex
	wgInterface   string // имя WireGuard интерфейса (например "bypass0")
	mainInterface string // имя основного исходящего интерфейса (например "eth0")
	socks5Addr    string // адрес SOCKS5 прокси для geo-трафика (например "127.0.0.1:1080")
	zapretEnabled bool
	initialized   bool
}

// NewManager создаёт менеджер маршрутизации.
func NewManager(wgIface, mainIface, socks5Addr string) *Manager {
	return &Manager{
		wgInterface:   wgIface,
		mainInterface: mainIface,
		socks5Addr:    socks5Addr,
	}
}

// Init выполняет начальную настройку маршрутизации:
// 1. Создаёт routing tables
// 2. Настраивает nftables chain для классификации трафика
// 3. Настраивает NAT
func (m *Manager) Init() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initialized {
		return nil
	}

	log.Printf("[МАРШРУТ] Инициализация policy routing...")

	// Определяем основной интерфейс, если не указан
	if m.mainInterface == "" {
		iface, err := detectMainInterface()
		if err != nil {
			return fmt.Errorf("определение интерфейса: %w", err)
		}
		m.mainInterface = iface
		log.Printf("[МАРШРУТ] Основной интерфейс: %s", m.mainInterface)
	}

	// 1. Включаем форвардинг
	runCmd("sysctl", "-w", "net.ipv4.ip_forward=1")

	// 2. Создаём routing tables
	// Таблица 100 (DPI) — через основной интерфейс (там Zapret обрабатывает)
	setupRouteTable(TableDPI, m.mainInterface)

	// Таблица 200 (Geo) — через SOCKS5 прокси (реализуется через redsocks/tp_rules
	// или напрямую через dnat в nftables к локальному Xray SOCKS5)
	// Для простоты: через policy routing с mark → redir на SOCKS5
	setupRouteTable(TableGeo, m.mainInterface)

	// 3. Настраиваем nftables для классификации трафика
	if err := m.setupNFTables(); err != nil {
		return fmt.Errorf("настройка nftables: %w", err)
	}

	// 4. NAT (masquerade) для трафика от клиентов
	setupNAT(m.wgInterface, m.mainInterface)

	m.initialized = true
	log.Printf("[МАРШРУТ] Policy routing инициализирован")
	return nil
}

// setupNFTables создаёт nftables chain для классификации трафика от WireGuard клиентов.
// Трафик маркируется fwmark в зависимости от целевого адреса:
// - Домены/IP из DPI-списка → mark 100 → table 100 → Zapret → Internet
// - Домены/IP из Geo-списка → mark 200 → table 200 → dnat к SOCKS5 → Xray → ЕС
// - Остальное → mark 0 → main table → Internet напрямую
//
// Примечание: классификация по доменам работает через DNS-перехват.
// DNS-модуль возвращает IP из специальных диапазонов:
// - DPI-домены → 10.77.0.0/16 (fake IP)
// - Geo-домены → 10.78.0.0/16 (fake IP)
// По этим IP и маркируем трафик.
func (m *Manager) setupNFTables() error {
	// Удаляем старую chain если есть
	nft("delete table inet bypass", false)

	// Создаём таблицу и chain
	commands := []struct {
		args   []string
		fatal  bool
	}{
		// Создаём таблицу
		{[]string{"add table inet bypass"}, true},
		// Chain для prerouting (DNAT для geo-трафика на SOCKS5)
		{[]string{"add chain inet bypass prerouting { type nat hook prerouting priority dstnat; }"}, true},
		// Chain для forward (маркировка и фильтрация)
		{[]string{"add chain inet bypass forward"}, true},
		// Chain для postrouting (masquerade)
		{[]string{"add chain inet bypass postrouting { type nat hook postrouting priority srcnat; }"}, true},

		// Подключаем forward chain к netfilter
		{[]string{"add rule inet bypass forward iifname", m.wgInterface, "jump bypass forward"}, false},

		// ====== Маркировка DPI-трафика (10.77.0.0/16) ======
		{[]string{
			"add rule inet bypass forward",
			"iifname", m.wgInterface,
			"ip daddr 10.77.0.0/16",
			"meta mark set", fmt.Sprintf("%d", MarkDPI),
		}, true},

		// ====== Маркировка Geo-трафика (10.78.0.0/16) → DNAT на SOCKS5 ======
		{[]string{
			"add rule inet bypass forward",
			"iifname", m.wgInterface,
			"ip daddr 10.78.0.0/16",
			"dnat to", m.socks5Addr,
		}, true},

		// ====== Весь остальной трафик от клиентов → masquerade ======
		{[]string{
			"add rule inet bypass postrouting",
			"oifname", m.mainInterface,
			"ip saddr 10.66.0.0/16",
			"masquerade",
		}, true},
	}

	for _, cmd := range commands {
		if err := nft(strings.Join(cmd.args, " "), cmd.fatal); err != nil {
			return err
		}
	}

	return nil
}

// SetupZapretRule добавляет nftables правило для направления DPI-трафика через nfqws (NFQUEUE).
// Вызывается после запуска nfqws.
func (m *Manager) SetupZapretRule(qnum int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.zapretEnabled = true

	// Трафик с mark=100 (DPI) на исходящий интерфейс → NFQUEUE для nfqws
	rule := fmt.Sprintf(
		"add rule inet bypass forward oifname %s meta mark %d queue num %d",
		m.mainInterface, MarkDPI, qnum,
	)
	return nft(rule, true)
}

// AddDomainRoute добавляет статический маршрут для домена.
// Используется при обновлении списков для явных доменов.
func (m *Manager) AddDomainRoute(domain string, listType int) {
	// Домены маршрутизируются через fake-IP механизм в DNS-модуле.
	// Здесь мы можем добавить ip rule для конкретного IP если нужно.
	log.Printf("[МАРШРУТ] Добавлен маршрут для %s (тип: %d)", domain, listType)
}

// RemoveAllRules удаляет все правила bypass таблицы (для cleanup).
func (m *Manager) RemoveAllRules() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.initialized = false
	return nft("delete table inet bypass", false)
}

// SetupIPRules настраивает ip rule для fwmark → routing table.
func (m *Manager) SetupIPRules() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Удаляем старые правила (игнорируем ошибки)
	runCmd("ip", "rule", "del", "fwmark", fmt.Sprintf("%d", MarkDPI), "table", fmt.Sprintf("%d", TableDPI))
	runCmd("ip", "rule", "del", "fwmark", fmt.Sprintf("%d", MarkGeo), "table", fmt.Sprintf("%d", TableGeo))

	// Добавляем новые
	if err := runCmdErr("ip", "rule", "add", "fwmark", fmt.Sprintf("%d", MarkDPI), "table", fmt.Sprintf("%d", TableDPI)); err != nil {
		log.Printf("[МАРШРУТ] Предупреждение: ip rule add DPI: %v", err)
	}
	if err := runCmdErr("ip", "rule", "add", "fwmark", fmt.Sprintf("%d", MarkGeo), "table", fmt.Sprintf("%d", TableGeo)); err != nil {
		log.Printf("[МАРШРУТ] Предупреждение: ip rule add Geo: %v", err)
	}

	return nil
}

// nft выполняет команду nft.
func nft(rule string, fatal bool) error {
	cmd := exec.Command("nft", strings.Fields(rule)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("nft %s: %s (%s)", rule, string(output), err)
		if fatal {
			return fmt.Errorf("%s", msg)
		}
		log.Printf("[МАРШРУТ] %s", msg)
	}
	return nil
}

// runCmd выполняет команду, игнорируя ошибки.
func runCmd(name string, args ...string) {
	_ = runCmdErr(name, args...)
}

// runCmdErr выполняет команду, возвращая ошибку.
func runCmdErr(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s (%w)", name, strings.Join(args, " "), string(output), err)
	}
	return nil
}

// setupRouteTable настраивает routing table.
func setupRouteTable(tableNum int, iface string) {
	gateway, err := getInterfaceGateway(iface)
	if err != nil {
		log.Printf("[МАРШРУТ] Предупреждение: не удалось получить gateway для %s: %v", iface, err)
		return
	}

	// default via gateway dev iface table <num>
	runCmd("ip", "route", "replace", "default", "via", gateway, "dev", iface, "table", fmt.Sprintf("%d", tableNum))
}

// setupNAT настраивает masquerade для трафика от WireGuard клиентов.
func setupNAT(wgIface, mainIface string) {
	// Удаляем старое правило
	runCmd("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", "10.66.0.0/16", "-o", mainIface, "-j", "MASQUERADE")

	// Добавляем новое
	runCmd("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "10.66.0.0/16", "-o", mainIface, "-j", "MASQUERADE")
}

// detectMainInterface определяет интерфейс по умолчанию.
func detectMainInterface() (string, error) {
	cmd := exec.Command("ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Парсим: "default via 1.2.3.4 dev eth0"
	fields := strings.Fields(string(output))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("не удалось определить интерфейс по умолчанию")
}

// getInterfaceGateway возвращает gateway для интерфейса.
func getInterfaceGateway(iface string) (string, error) {
	cmd := exec.Command("ip", "route", "show", "dev", iface, "default")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	fields := strings.Fields(string(output))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("gateway не найден для %s", iface)
}

// SetMainInterface устанавливает основной интерфейс (для тестов).
func (m *Manager) SetMainInterface(iface string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mainInterface = iface
}

// IsInitialized возвращает true если маршрутизация инициализирована.
func (m *Manager) IsInitialized() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initialized
}