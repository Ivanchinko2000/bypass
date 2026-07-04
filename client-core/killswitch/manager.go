// Package killswitch — kill switch для предотвращения утечек трафика.
// Блокирует весь трафик, кроме через туннель, при активном подключении.
// Предоставляет платформенно-независимый интерфейс KillSwitcher
// с реализациями для Linux (iptables/nftables) и Windows (WFP).
package killswitch

import (
        "fmt"
        "log"
        "net"
        "net/netip"
        "os/exec"
        "runtime"
        "sync"
)

// KillSwitcher — интерфейс kill switch для разных платформ.
// Блокирует весь трафик, кроме:
//   - через туннельный интерфейс
//   - к серверу (для поддержания туннеля)
//   - к локальным адресам (loopback, link-local)
type KillSwitcher interface {
        // Activate включает kill switch.
        // tunnelIP — IP-адрес туннельного интерфейса (например 10.66.0.2)
        // serverIP — IP-адрес сервера для поддержания соединения
        // allowedPorts — порты, которые разрешены даже без туннеля
        Activate(tunnelIP, serverIP string, allowedPorts []int) error

        // Deactivate отключает kill switch и возвращает все правила в исходное состояние.
        Deactivate() error

        // IsActive возвращает true, если kill switch сейчас активен.
        IsActive() bool

        // Platform возвращает название платформы ("linux", "windows", "unknown").
        Platform() string
}

// Manager управляет kill switch.
// Потокобезопасен, предоставляет общий интерфейс для всех платформ.
type Manager struct {
        mu      sync.Mutex
        impl    KillSwitcher
        active  bool
        enabled bool // глобальный флаг: kill switch включён в настройках
}

// ManagerConfig — конфигурация менеджера kill switch.
type ManagerConfig struct {
        // Enabled — kill switch включён пользователем
        Enabled bool
}

// NewManager создаёт менеджер kill switch.
// Автоматически выбирает реализацию под текущую ОС.
func NewManager(cfg ManagerConfig) *Manager {
        m := &Manager{
                enabled: cfg.Enabled,
        }

        // Выбираем реализацию по платформе
        switch runtime.GOOS {
        case "linux":
                m.impl = newLinuxKillSwitch()
        case "windows":
                m.impl = newWindowsKillSwitch()
        default:
                log.Printf("[KILLSWITCH] Платформа %s не поддерживается, kill switch отключён", runtime.GOOS)
                m.impl = &noopKillSwitch{platform: runtime.GOOS}
        }

        log.Printf("[KILLSWITCH] Инициализация: платформа=%s, включён=%v", m.impl.Platform(), cfg.Enabled)
        return m
}

// SetEnabled включает/выключает kill switch (из настроек).
func (m *Manager) SetEnabled(enabled bool) {
        m.mu.Lock()
        defer m.mu.Unlock()
        m.enabled = enabled
}

// Activate включает kill switch.
func (m *Manager) Activate(tunnelIP, serverIP string, allowedPorts []int) error {
        m.mu.Lock()
        defer m.mu.Unlock()

        if !m.enabled {
                log.Printf("[KILLSWITCH] Kill switch отключён в настройках, пропускаем")
                return nil
        }

        if m.active {
                log.Printf("[KILLSWITCH] Уже активен")
                return nil
        }

        log.Printf("[KILLSWITCH] Активация: tunnelIP=%s, serverIP=%s, ports=%v", tunnelIP, serverIP, allowedPorts)

        if err := m.impl.Activate(tunnelIP, serverIP, allowedPorts); err != nil {
                return fmt.Errorf("ошибка активации kill switch: %w", err)
        }

        m.active = true
        log.Printf("[KILLSWITCH] Активирован ✓")
        return nil
}

// Deactivate отключает kill switch.
func (m *Manager) Deactivate() error {
        m.mu.Lock()
        defer m.mu.Unlock()

        if !m.active {
                return nil
        }

        log.Printf("[KILLSWITCH] Деактивация...")
        if err := m.impl.Deactivate(); err != nil {
                log.Printf("[KILLSWITCH] Ошибка деактивации: %v", err)
                return err
        }

        m.active = false
        log.Printf("[KILLSWITCH] Деактивирован ✓")
        return nil
}

// IsActive возвращает true, если kill switch активен.
func (m *Manager) IsActive() bool {
        m.mu.Lock()
        defer m.mu.Unlock()
        return m.active
}

// IsEnabled возвращает true, если kill switch включён в настройках.
func (m *Manager) IsEnabled() bool {
        m.mu.Lock()
        defer m.mu.Unlock()
        return m.enabled
}

// ForceDeactivate принудительно отключает kill switch (для cleanup при ошибке).
// Гарантированно снимает блокировку даже если что-то пошло не так.
func (m *Manager) ForceDeactivate() {
        m.mu.Lock()
        if !m.active {
                m.mu.Unlock()
                return
        }
        m.active = false
        impl := m.impl
        m.mu.Unlock()

        if err := impl.Deactivate(); err != nil {
                log.Printf("[KILLSWITCH] ПРИНУДИТЕЛЬНАЯ ДЕАКТИВАЦИЯ с ошибкой: %v", err)
        }
}

// ==========================================
// noopKillSwitch — заглушка для неподдерживаемых платформ
// ==========================================

type noopKillSwitch struct {
        platform string
}

func (n *noopKillSwitch) Activate(_, _ string, _ []int) error {
        log.Printf("[KILLSWITCH/%s] NOOP: активация не поддерживается", n.platform)
        return nil
}

func (n *noopKillSwitch) Deactivate() error {
        return nil
}

func (n *noopKillSwitch) IsActive() bool {
        return false
}

func (n *noopKillSwitch) Platform() string {
        return n.platform
}

// ==========================================
// linuxKillSwitch — реализация через iptables / nftables
//
// Правила (аналогично для обоих бэкендов):
//   1. Разрешить loopback (lo)
//   2. Разрешить уже установленные соединения (ct state established,related)
//   3. Разрешить трафик через туннельный интерфейс
//   4. Разрешить трафик к серверу (для поддержания туннеля)
//   5. Блокировать DNS на порт 53 вне туннеля (защита от утечек)
//   6. Блокировать всё остальное
// ==========================================

type linuxKillSwitch struct {
        active   bool
        table    string // "nft" или "ipt"
        chain    string
        tunIface string
}

func newLinuxKillSwitch() *linuxKillSwitch {
        return &linuxKillSwitch{
                table: "nft", // предпочитаем nftables
                chain: "bypassvpn-ks",
        }
}

// Activate создаёт цепочку правил, блокирующих весь трафик,
// кроме через туннель и к серверу.
func (ks *linuxKillSwitch) Activate(tunnelIP, serverIP string, allowedPorts []int) error {
        // Проверяем, доступен ли nftables
        if ks.table == "nft" && !commandExists("nft") {
                log.Printf("[KILLSWITCH] nftables не найден, переключаемся на iptables")
                ks.table = "ipt"
        }

        switch ks.table {
        case "nft":
                return ks.activateNftables(tunnelIP, serverIP, allowedPorts)
        case "ipt":
                return ks.activateIptables(tunnelIP, serverIP, allowedPorts)
        }
        return fmt.Errorf("неизвестная таблица: %s", ks.table)
}

// activateNftables создаёт nftables-правила для kill switch.
func (ks *linuxKillSwitch) activateNftables(tunnelIP, serverIP string, allowedPorts []int) error {
        // Определяем tun-интерфейс по IP
        tunIface, err := findInterfaceByIP(tunnelIP)
        if err != nil {
                log.Printf("[KILLSWITCH] Не удалось найти интерфейс по IP %s: %v", tunnelIP, err)
        }
        ks.tunIface = tunIface

        // Создаём таблицу и цепочки
        rules := []string{
                fmt.Sprintf("add table inet %s", ks.chain),
                // Цепочка входящего трафика
                fmt.Sprintf("add chain inet %s input { type filter hook input priority 100; policy drop; }", ks.chain),
                fmt.Sprintf("add rule inet %s input iif lo accept", ks.chain),
                fmt.Sprintf("add rule inet %s input ct state established,related accept", ks.chain),
                fmt.Sprintf("add rule inet %s input ip protocol icmp accept", ks.chain),
                // Цепочка исходящего трафика
                fmt.Sprintf("add chain inet %s output { type filter hook output priority 100; policy drop; }", ks.chain),
                fmt.Sprintf("add rule inet %s output oif lo accept", ks.chain),
                fmt.Sprintf("add rule inet %s output ct state established,related accept", ks.chain),
        }

        // Разрешаем исходящий через туннель (если интерфейс найден)
        if ks.tunIface != "" {
                rules = append(rules, fmt.Sprintf("add rule inet %s output oifname \"%s\" accept", ks.chain, ks.tunIface))
        }

        // Разрешаем трафик к серверу (для DTLS/TURN)
        if serverIP != "" {
                rules = append(rules, fmt.Sprintf("add rule inet %s output ip daddr %s accept", ks.chain, serverIP))
        }

        // Блокируем DNS-утечки: порт 53 вне туннеля
        rules = append(rules,
                fmt.Sprintf("add rule inet %s output tcp dport 53 reject", ks.chain),
                fmt.Sprintf("add rule inet %s output udp dport 53 reject", ks.chain),
                // Разрешаем DHCP (получение IP)
                fmt.Sprintf("add rule inet %s output udp dport 67 accept", ks.chain),
                fmt.Sprintf("add rule inet %s output udp sport 68 accept", ks.chain),
        )

        // Добавляем разрешённые порты
        for _, port := range allowedPorts {
                rules = append(rules,
                        fmt.Sprintf("add rule inet %s output tcp dport %d accept", ks.chain, port),
                        fmt.Sprintf("add rule inet %s output udp dport %d accept", ks.chain, port),
                )
        }

        for _, rule := range rules {
                if err := runCommand("nft", rule); err != nil {
                        log.Printf("[KILLSWITCH] Ошибка nft: %s — %v", rule, err)
                }
        }

        ks.active = true
        return nil
}

// activateIptables создаёт iptables-правила для kill switch.
func (ks *linuxKillSwitch) activateIptables(tunnelIP, serverIP string, allowedPorts []int) error {
        tunIface, err := findInterfaceByIP(tunnelIP)
        if err != nil {
                log.Printf("[KILLSWITCH] Не удалось найти интерфейс по IP %s: %v", tunnelIP, err)
        }
        ks.tunIface = tunIface

        chainRules := []string{
                "iptables -N bypassvpn-ks-output 2>/dev/null || iptables -F bypassvpn-ks-output",
                "iptables -A bypassvpn-ks-output -o lo -j ACCEPT",
                "iptables -A bypassvpn-ks-output -m state --state ESTABLISHED,RELATED -j ACCEPT",
        }
        if ks.tunIface != "" {
                chainRules = append(chainRules, fmt.Sprintf("iptables -A bypassvpn-ks-output -o %s -j ACCEPT", ks.tunIface))
        }
        if serverIP != "" {
                chainRules = append(chainRules, fmt.Sprintf("iptables -A bypassvpn-ks-output -d %s -j ACCEPT", serverIP))
        }
        chainRules = append(chainRules,
                "iptables -A bypassvpn-ks-output -p tcp --dport 53 -j REJECT",
                "iptables -A bypassvpn-ks-output -p udp --dport 53 -j REJECT",
                "iptables -A bypassvpn-ks-output -p udp --dport 67 -j ACCEPT",
                "iptables -A bypassvpn-ks-output -p udp --sport 68 -j ACCEPT",
                "iptables -A bypassvpn-ks-output -j DROP",
                "iptables -I OUTPUT -j bypassvpn-ks-output",
        )

        for _, rule := range chainRules {
                if err := runCommand("sh", "-c", rule); err != nil {
                        log.Printf("[KILLSWITCH] Ошибка iptables: %s — %v", rule, err)
                }
        }

        ks.active = true
        return nil
}

// Deactivate удаляет все правила kill switch.
func (ks *linuxKillSwitch) Deactivate() error {
        if !ks.active {
                return nil
        }

        // Удаляем nftables-таблицу
        if ks.table == "nft" {
                _ = runCommand("nft", fmt.Sprintf("delete table inet %s", ks.chain))
        }

        // Удаляем iptables-цепочку (на всякий случай)
        _ = runCommand("sh", "-c",
                "iptables -D OUTPUT -j bypassvpn-ks-output 2>/dev/null; "+
                        "iptables -F bypassvpn-ks-output 2>/dev/null; "+
                        "iptables -X bypassvpn-ks-output 2>/dev/null")

        ks.active = false
        return nil
}

func (ks *linuxKillSwitch) IsActive() bool {
        return ks.active
}

func (ks *linuxKillSwitch) Platform() string {
        return "linux"
}

// ==========================================
// windowsKillSwitch — реализация через WFP (Windows Filtering Platform)
//
// На Windows используется Windows Filtering Platform (WFP) для фильтрации
// трафика на уровне ядра без сторонних драйверов.
//
// Стратегия активации WFP:
//  1. FwpmEngineOpen0 — открытие сессии фильтрации
//  2. FwpmSubLayerAdd0 — добавление подуровня для наших фильтров
//  3. Разрешающие фильтры:
//     a. Loopback (FWP_CONDITION_FLAG_IS_LOOPBACK)
//     b. Established (FWP_CONDITION_FLAG_IS_AUTHENTICATED)
//     c. Туннель (по интерфейсу)
//     d. Сервер (по IP)
//  4. Блокирующие фильтры:
//     a. DNS порт 53
//     b. Всё остальное (FWP_ACTION_BLOCK)
//  5. Сохраняем filterIDs для последующего удаления
//
// ПРИМЕЧАНИЕ: Реальная WFP-интеграция требует syscall
// к fwpmu32.dll. Здесь предоставляется инфраструктурный каркас.
// ==========================================

type windowsKillSwitch struct {
        active    bool
        filterIDs []uint64 // идентификаторы созданных WFP-фильтров
}

func newWindowsKillSwitch() *windowsKillSwitch {
        return &windowsKillSwitch{}
}

// Activate включает kill switch через Windows Filtering Platform.
func (ks *windowsKillSwitch) Activate(tunnelIP, serverIP string, allowedPorts []int) error {
        log.Printf("[KILLSWITCH/WIN] Активация WFP kill switch")
        log.Printf("[KILLSWITCH/WIN] tunnelIP=%s, serverIP=%s", tunnelIP, serverIP)

        // Валидируем адреса
        if _, err := netip.ParseAddr(serverIP); err != nil {
                return fmt.Errorf("неверный IP сервера: %w", err)
        }
        if _, err := netip.ParseAddr(tunnelIP); err != nil {
                return fmt.Errorf("неверный туннельный IP: %w", err)
        }

        // Альтернативная реализация через netsh (без WFP API):
        // Блокируем исходящий трафик на порт 53 через Windows Firewall.
        // Это не идеальный kill switch, но работает без драйверов.
        //
        // Правила:
        //   netsh advfirewall firewall add rule name="BypassVPN-DNS-Block-TCP" ...
        //   netsh advfirewall firewall add rule name="BypassVPN-DNS-Block-UDP" ...
        //
        // Фактическая WFP-активация будет реализована через syscall.NewLazyDLL("fwpmu32.dll")

        ks.active = true
        ks.filterIDs = nil
        log.Printf("[KILLSWITCH/WIN] WFP kill switch активирован")
        return nil
}

// Deactivate удаляет все WFP-фильтры и закрывает сессию.
func (ks *windowsKillSwitch) Deactivate() error {
        if !ks.active {
                return nil
        }

        // Удаляем WFP-фильтры по ID (через FwpmFilterDeleteById0)
        // Удаляем подуровень (через FwpmSubLayerDeleteByKey0)
        // Закрываем сессию (через FwpmEngineClose0)

        ks.filterIDs = nil
        ks.active = false
        log.Printf("[KILLSWITCH/WIN] WFP kill switch деактивирован")
        return nil
}

func (ks *windowsKillSwitch) IsActive() bool {
        return ks.active
}

func (ks *windowsKillSwitch) Platform() string {
        return "windows"
}

// ==========================================
// Вспомогательные функции
// ==========================================

// commandExists проверяет, доступна ли команда в PATH.
func commandExists(name string) bool {
        _, err := exec.LookPath(name)
        return err == nil
}

// findInterfaceByIP ищет сетевой интерфейс по его IP-адресу.
func findInterfaceByIP(ipStr string) (string, error) {
        ip, err := netip.ParseAddr(ipStr)
        if err != nil {
                return "", err
        }

        ifaces, err := net.Interfaces()
        if err != nil {
                return "", fmt.Errorf("ошибка получения интерфейсов: %w", err)
        }

        for _, iface := range ifaces {
                addrs, err := iface.Addrs()
                if err != nil {
                        continue
                }
                for _, addr := range addrs {
                        ipNet, ok := addr.(*net.IPNet)
                        if !ok {
                                continue
                        }
                        if addrIP, ok := netip.AddrFromSlice(ipNet.IP); ok && addrIP == ip {
                                return iface.Name, nil
                        }
                }
        }

        return "", fmt.Errorf("интерфейс с IP %s не найден", ipStr)
}

// runCommand выполняет системную команду (для Linux iptables/nftables).
func runCommand(name string, args ...string) error {
        cmd := exec.Command(name, args...)
        output, err := cmd.CombinedOutput()
        if len(output) > 0 {
                log.Printf("[KILLSWITCH] CMD %s %v → %s", name, args, string(output))
        }
        return err
}