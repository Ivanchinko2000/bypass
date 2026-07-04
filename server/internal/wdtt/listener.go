// Package wdtt реализует WDTT (WireGuard-over-DTLS-with-Transport-TLS-fake) сервер.
// Адаптировано из reference implementation: протокол DTLS + WRAP/RTP обфускация
// (ChaCha20-Poly1305 AEAD поверх RTP-фреймов) с проксированием в userspace WireGuard.
//
// Ключевые компоненты:
//   - wrapKeyStore: хранение derive-ключей из паролей пользователей
//   - wrapPacketListener / wrapPacketConn: обёртка UDP для деобфускации RTP
//   - DTLS listener: self-signed сертификат, pion/dtls
//   - handleConn: аутентификация (GETCONF/AUTH/READY) → двусторонний прокси в WG
package wdtt

import (
        "crypto/cipher"
        "context"
        "crypto/rand"
        "crypto/sha256"
        "encoding/base64"
        "encoding/binary"
        "encoding/hex"
        "errors"
        "fmt"
        "io"
        "log"
        "net"
        "os/exec"
        "strings"
        "sync"
        "sync/atomic"
        "time"

        "github.com/pion/dtls/v3"
        "github.com/pion/dtls/v3/pkg/crypto/selfsign"
        "golang.org/x/crypto/chacha20poly1305"
        "golang.org/x/crypto/curve25519"
        "golang.org/x/crypto/hkdf"

        "golang.zx2c4.com/wireguard/conn"
        "golang.zx2c4.com/wireguard/device"
        "golang.zx2c4.com/wireguard/ipc"
        "golang.zx2c4.com/wireguard/tun"

        dtlnet "github.com/pion/dtls/v3/pkg/net"
        pionudp "github.com/pion/transport/v4/udp"

        "bypass-server/internal/config"
        "bypass-server/internal/users"
)

// ==========================================
// Константы протокола
// ==========================================

const (
        // wrapKeyLen — длина ключа ChaCha20-Poly1305 (32 байта)
        wrapKeyLen = 32

        // wrapNonceLen — длина nonce для AEAD (12 байт)
        wrapNonceLen = 12
)

// ==========================================
// Конфигурация обфускации (RTP-фрейм)
// ==========================================

// ObfsConfig содержит параметры RTP-обфускации.
// Генерируется случайно для каждой новой сессии.
type ObfsConfig struct {
        // SSRC — идентификатор источника RTP (4 байта, случайный)
        SSRC uint32
        // PayloadType — тип полезной нагрузки RTP (по стандарту WDTT = 111)
        PayloadType uint8
        // PaddingMax — максимальный размер случайного паддинга
        PaddingMax int
}

// ObfsState хранит счётчики последовательности и таймстампа для RTP-обфускации.
type ObfsState struct {
        mu      sync.Mutex
        initSeq uint16 // начальный порядковый номер
        initTs  uint32 // начальный таймстамп
        count   uint64 // счётчик пакетов
}

// NewObfsConfig создаёт случайную конфигурацию обфускации.
func NewObfsConfig() *ObfsConfig {
        var buf [4]byte
        rand.Read(buf[:])
        return &ObfsConfig{
                SSRC:        binary.BigEndian.Uint32(buf[:]),
                PayloadType: 111, // фиксированный тип полезной нагрузки WDTT
                PaddingMax:  24,  // до 24 байт случайного паддинга
        }
}

// NewObfsState создаёт начальное состояние обфускации с рандомными seq/ts.
func NewObfsState() *ObfsState {
        var buf [6]byte
        rand.Read(buf[:])
        return &ObfsState{
                initSeq: binary.BigEndian.Uint16(buf[0:2]),
                initTs:  binary.BigEndian.Uint32(buf[2:6]),
                count:   0,
        }
}

// ==========================================
// AEAD кэш (ChaCha20-Poly1305)
// ==========================================

// aeadCache кэширует AEAD-инстансы по ключу для ускорения обработки пакетов.
var aeadCache sync.Map

// getAEAD возвращает (или создаёт) ChaCha20-Poly1305 AEAD для ключа.
func getAEAD(key []byte) (cipher.AEAD, error) {
        if len(key) != wrapKeyLen {
                return nil, fmt.Errorf("obfs: ключ должен быть %d байт", wrapKeyLen)
        }
        keyStr := string(key)
        if val, ok := aeadCache.Load(keyStr); ok {
                return val.(cipher.AEAD), nil
        }
        aead, err := chacha20poly1305.New(key)
        if err != nil {
                return nil, err
        }
        aeadCache.Store(keyStr, aead)
        return aead, nil
}

// ==========================================
// WRAP: вывод ключа из пароля
// ==========================================

// deriveWrapKey выводит 32-байтный ключ из пароля через HKDF-SHA256.
// Использует фиксированные salt/info из протокола WDTT.
func deriveWrapKey(password string) ([]byte, error) {
        if password == "" {
                return nil, errors.New("пустой пароль")
        }
        key := make([]byte, wrapKeyLen)
        reader := hkdf.New(
                sha256.New,
                []byte(password),
                []byte("WDTT-WRAP-v1"),
                []byte("rtp-obfs/chacha20poly1305"),
        )
        if _, err := io.ReadFull(reader, key); err != nil {
                return nil, fmt.Errorf("derive wrap key: %w", err)
        }
        return key, nil
}

// ==========================================
// WRAP: RTP обфускация (wrap/unwrap)
// ==========================================

// obfsBuildNonce собирает 12-байтный nonce из SSRC, seq, ts (как в RTP).
func obfsBuildNonce(ssrc uint32, seq uint16, ts uint32) []byte {
        n := make([]byte, 12)
        binary.BigEndian.PutUint32(n[0:4], ssrc)
        binary.BigEndian.PutUint16(n[4:6], seq)
        binary.BigEndian.PutUint32(n[8:12], ts)
        return n
}

// obfsWrapPacket шифрует payload в RTP-подобный фрейм с AEAD.
// Формат: [12 байт RTP header] [AEAD ciphertext] [padding] [1 байт padLen]
func obfsWrapPacket(key, payload []byte, cfg *ObfsConfig, state *ObfsState) ([]byte, error) {
        if len(key) != wrapKeyLen {
                return nil, fmt.Errorf("obfs: ключ должен быть %d байт (получено %d)", wrapKeyLen, len(key))
        }
        if len(payload) == 0 {
                return nil, errors.New("obfs: пустой payload")
        }
        state.mu.Lock()
        c := state.count
        state.count++
        state.mu.Unlock()

        seq := state.initSeq + uint16(c)
        ts := state.initTs + uint32(c)*960 + uint32(c>>16)

        nonce := obfsBuildNonce(cfg.SSRC, seq, ts)

        // Случайный паддинг для изменения размера пакетов
        padRand := 0
        if cfg.PaddingMax > 0 {
                var rndBuf [1]byte
                rand.Read(rndBuf[:])
                padRand = int(rndBuf[0]) % cfg.PaddingMax
        }
        padTotal := padRand + 1
        outLen := 12 + len(payload) + chacha20poly1305.Overhead + padTotal
        out := make([]byte, outLen)

        // RTP v2 header: version=2, marker=0, extension=1, payloadType=111
        out[0] = 0x80 | 0x20 // version 2 + extension bit
        out[1] = cfg.PayloadType & 0x7F
        binary.BigEndian.PutUint16(out[2:4], seq)
        binary.BigEndian.PutUint32(out[4:8], ts)
        binary.BigEndian.PutUint32(out[8:12], cfg.SSRC)

        aead, err := getAEAD(key)
        if err != nil {
                return nil, fmt.Errorf("obfs: cipher init: %w", err)
        }

        // AEAD-Seal с RTP header как AAD
        sealed := aead.Seal(out[12:12], nonce, payload, out[:12])
        padStart := 12 + len(sealed)

        // Заполняем случайный паддинг
        if padRand > 0 {
                rand.Read(out[padStart : padStart+padRand])
        }
        // Последний байт — общая длина паддинга
        out[outLen-1] = byte(padTotal)

        return out, nil
}

// obfsUnwrapPacket расшифровывает RTP-подобный фрейм, извлекая payload.
// Проверяет формат, паддинг, AEAD-тег; использует RTP header как AAD.
func obfsUnwrapPacket(key, wire, dst []byte) (int, error) {
        if len(key) != wrapKeyLen {
                return 0, fmt.Errorf("obfs: ключ должен быть %d байт (получено %d)", wrapKeyLen, len(key))
        }
        if len(wire) < 13 {
                return 0, errors.New("obfs: пакет слишком короткий")
        }
        // Проверяем RTP version 2
        if (wire[0] >> 6) != 2 {
                return 0, errors.New("obfs: не RTP v2")
        }

        seq := binary.BigEndian.Uint16(wire[2:4])
        ts := binary.BigEndian.Uint32(wire[4:8])
        ssrc := binary.BigEndian.Uint32(wire[8:12])

        // Вычисляем конец полезной нагрузки (вычитаем паддинг)
        payloadEnd := len(wire)
        if wire[0]&0x20 != 0 {
                padLen := int(wire[len(wire)-1])
                if padLen == 0 || padLen > payloadEnd-12 {
                        return 0, fmt.Errorf("obfs: некорректная длина паддинга %d", padLen)
                }
                payloadEnd -= padLen
        }

        ciphertextLen := payloadEnd - 12
        if ciphertextLen <= chacha20poly1305.Overhead {
                return 0, errors.New("obfs: нет полезной нагрузки")
        }
        if ciphertextLen-chacha20poly1305.Overhead > len(dst) {
                return 0, errors.New("obfs: буфер назначения слишком мал")
        }

        nonce := obfsBuildNonce(ssrc, seq, ts)
        aead, err := getAEAD(key)
        if err != nil {
                return 0, fmt.Errorf("obfs: cipher init: %w", err)
        }

        // AEAD-Open: RTP header как AAD
        plain, err := aead.Open(dst[:0], nonce, wire[12:payloadEnd], wire[:12])
        if err != nil {
                return 0, fmt.Errorf("obfs: auth: %w", err)
        }
        return len(plain), nil
}

// obfsIsRTPPacket проверяет, что пакет выглядит как RTP-фрейм WDTT:
// минимум 13 байт, version=2, payloadType=111.
func obfsIsRTPPacket(wire []byte) bool {
        if len(wire) < 13 {
                return false
        }
        if (wire[0] >> 6) != 2 {
                return false
        }
        pt := wire[1] & 0x7F
        return pt == 111
}

// ==========================================
// wrapKeyStore — хранилище активных WRAP-ключей
// ==========================================

// wrapKeyEntry связывает идентификатор ключа с 32-байтным ключом.
type wrapKeyEntry struct {
        id  string
        key []byte
}

// wrapKeyStore потокобезопасно хранит набор WRAP-ключей, выведенных из паролей.
// При Unwrap перебирает все ключи пока AEAD не пройдёт.
type wrapKeyStore struct {
        mu      sync.RWMutex
        entries []wrapKeyEntry
}

// newWrapKeyStore создаёт пустое хранилище ключей.
func newWrapKeyStore() *wrapKeyStore {
        return &wrapKeyStore{}
}

// SetPasswords пересоздаёт набор ключей из главного пароля и списка пользовательских.
// Старые ключи безопасно обнуляются (zeroBytes).
func (s *wrapKeyStore) SetPasswords(mainPassword string, userPasswords []string) error {
        next := make([]wrapKeyEntry, 0, len(userPasswords)+1)
        seen := make(map[string]struct{}, len(userPasswords)+1)

        // Главный пароль (если задан)
        if mainPassword != "" {
                key, err := deriveWrapKey(mainPassword)
                if err != nil {
                        return err
                }
                next = append(next, wrapKeyEntry{id: "main", key: key})
                seen["main"] = struct{}{}
        }

        // Пароли пользователей
        for _, password := range userPasswords {
                if password == "" {
                        continue
                }
                id := "pass:" + password
                if _, exists := seen[id]; exists {
                        continue
                }
                key, err := deriveWrapKey(password)
                if err != nil {
                        // Безопасно обнуляем уже созданные ключи при ошибке
                        for _, entry := range next {
                                zeroBytes(entry.key)
                        }
                        return err
                }
                next = append(next, wrapKeyEntry{id: id, key: key})
                seen[id] = struct{}{}
        }

        s.mu.Lock()
        old := s.entries
        s.entries = next
        s.mu.Unlock()
        // Безопасное обнуление старых ключей
        for _, entry := range old {
                zeroBytes(entry.key)
        }
        return nil
}

// AddPassword добавляет WRAP-ключ для нового пароля.
func (s *wrapKeyStore) AddPassword(password string) error {
        key, err := deriveWrapKey(password)
        if err != nil {
                return err
        }
        id := "pass:" + password

        s.mu.Lock()
        defer s.mu.Unlock()
        for _, entry := range s.entries {
                if entry.id == id {
                        zeroBytes(key)
                        return nil // уже есть
                }
        }
        s.entries = append(s.entries, wrapKeyEntry{id: id, key: key})
        return nil
}

// RemovePassword удаляет WRAP-ключ по паролю.
func (s *wrapKeyStore) RemovePassword(password string) {
        id := "pass:" + password

        s.mu.Lock()
        defer s.mu.Unlock()
        for i, entry := range s.entries {
                if entry.id != id {
                        continue
                }
                zeroBytes(entry.key)
                copy(s.entries[i:], s.entries[i+1:])
                s.entries[len(s.entries)-1] = wrapKeyEntry{}
                s.entries = s.entries[:len(s.entries)-1]
                return
        }
}

// Count возвращает количество активных ключей.
func (s *wrapKeyStore) Count() int {
        s.mu.RLock()
        defer s.mu.RUnlock()
        return len(s.entries)
}

// Unwrap пробует расшифровать RTP-пакет всеми активными ключами.
// Возвращает: ключ, длина payload, ошибка.
func (s *wrapKeyStore) Unwrap(raw, dst []byte) ([]byte, int, error) {
        if !obfsIsRTPPacket(raw) {
                return nil, 0, errors.New("wrap: не обфусцированный пакет")
        }

        s.mu.RLock()
        defer s.mu.RUnlock()
        if len(s.entries) == 0 {
                return nil, 0, errors.New("wrap: нет активных ключей")
        }

        // Перебираем все ключи — AEAD-проверка отсеет неверные
        for _, entry := range s.entries {
                m, err := obfsUnwrapPacket(entry.key, raw, dst)
                if err == nil {
                        // Клонируем ключ в независимую память для использования в сессии
                        return append([]byte(nil), entry.key...), m, nil
                }
        }
        return nil, 0, errors.New("wrap: аутентификация не удалась")
}

// zeroBytes безопасно обнуляет срез байт (для стирания ключей из памяти).
func zeroBytes(b []byte) {
        for i := range b {
                b[i] = 0
        }
}

// ==========================================
// wrapPacketListener — UDP listener с деобфускацией
// ==========================================

// wrapPacketListener оборачивает UDP-сокет: при Accept возвращает
// wrapPacketConn, которая прозрачно расшифровывает WRAP/RTP на лету.
type wrapPacketListener struct {
        inner dtlnet.PacketListener
        keys  *wrapKeyStore
}

// Accept ждёт новый UDP-пакет и оборачивает соединение в wrapPacketConn.
func (l *wrapPacketListener) Accept() (net.PacketConn, net.Addr, error) {
        pc, addr, err := l.inner.Accept()
        if err != nil {
                return pc, addr, err
        }
        return &wrapPacketConn{inner: pc, keys: l.keys}, addr, nil
}

func (l *wrapPacketListener) Close() error   { return l.inner.Close() }
func (l *wrapPacketListener) Addr() net.Addr { return l.inner.Addr() }

// wrapPacketConn — обёртка вокруг net.PacketConn с деобфускацией RTP.
// Первый пакет используется для выбора ключа (автоматическая аутентификация по WRAP).
// Последующие пакеты расшифровываются выбранным ключом; при ошибке — пробует все ключи.
type wrapPacketConn struct {
        inner     net.PacketConn
        keys      *wrapKeyStore
        key       []byte  // выбранный ключ для этой сессии
        selected  int32   // atomic: 0 = ключ ещё не выбран
        authLog   int32   // atomic: логируем первую попытку только один раз
        obfsCfg   *ObfsConfig
        obfsWrite *ObfsState
}

// ReadFrom читает UDP-пакет и расшифровывает его из RTP-обфускации.
func (c *wrapPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
        // Буфер с доп. местом под RTP header (12) + AEAD tag (16) + padding
        buf := make([]byte, len(p)+80)
        n, addr, err := c.inner.ReadFrom(buf)
        if err != nil {
                return 0, addr, err
        }
        raw := buf[:n]

        if atomic.LoadInt32(&c.selected) == 0 {
                // Первый пакет: пробуем все ключи для выбора
                key, m, uErr := c.keys.Unwrap(raw, p)
                if uErr != nil {
                        if atomic.CompareAndSwapInt32(&c.authLog, 0, 1) {
                                log.Printf("[WRAP] Отказ: RTP AEAD auth failed от %s (ключей=%d)", addr.String(), c.keys.Count())
                        }
                        return 0, addr, uErr
                }
                c.key = append([]byte(nil), key...)
                c.obfsCfg = NewObfsConfig()
                c.obfsWrite = NewObfsState()
                atomic.StoreInt32(&c.selected, 1)
                if atomic.CompareAndSwapInt32(&c.authLog, 0, 1) {
                        log.Printf("[WRAP] OK: ключ выбран для %s (ключей=%d)", addr.String(), c.keys.Count())
                }
                return m, addr, nil
        }

        // Последующие пакеты: используем выбранный ключ
        m, uErr := obfsUnwrapPacket(c.key, raw, p)
        if uErr != nil {
                // Если старый ключ не подошёл — возможно, пароль обновлён.
                // Пробуем пере-верифицировать по всем активным ключам.
                key, m2, uErr2 := c.keys.Unwrap(raw, p)
                if uErr2 == nil {
                        c.key = append([]byte(nil), key...)
                        c.obfsCfg = NewObfsConfig()
                        c.obfsWrite = NewObfsState()
                        log.Printf("[WRAP] Ключ обновлён на лету для %s", addr.String())
                        return m2, addr, nil
                }
                return 0, addr, fmt.Errorf("obfs unwrap: %w", uErr)
        }
        return m, addr, nil
}

// WriteTo шифрует данные в RTP-подобный фрейм и отправляет.
func (c *wrapPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
        if atomic.LoadInt32(&c.selected) == 0 || len(c.key) != wrapKeyLen {
                return 0, errors.New("wrap: ключ не выбран")
        }
        if c.obfsCfg == nil || c.obfsWrite == nil {
                c.obfsCfg = NewObfsConfig()
                c.obfsWrite = NewObfsState()
        }
        wrapped, wErr := obfsWrapPacket(c.key, p, c.obfsCfg, c.obfsWrite)
        if wErr != nil {
                return 0, fmt.Errorf("obfs wrap: %w", wErr)
        }
        if _, err := c.inner.WriteTo(wrapped, addr); err != nil {
                return 0, err
        }
        return len(p), nil
}

func (c *wrapPacketConn) Close() error                       { return c.inner.Close() }
func (c *wrapPacketConn) LocalAddr() net.Addr                { return c.inner.LocalAddr() }
func (c *wrapPacketConn) SetDeadline(t time.Time) error      { return c.inner.SetDeadline(t) }
func (c *wrapPacketConn) SetReadDeadline(t time.Time) error  { return c.inner.SetReadDeadline(t) }
func (c *wrapPacketConn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }

// listenWrapped создаёт UDP-слушатель с обфускацией.
func listenWrapped(addr *net.UDPAddr, keys *wrapKeyStore) (dtlnet.PacketListener, error) {
        if keys == nil || keys.Count() == 0 {
                return nil, errors.New("wrap: нет активных ключей")
        }
        inner, err := pionudp.Listen("udp", addr)
        if err != nil {
                return nil, fmt.Errorf("wrap: udp listen: %w", err)
        }
        return &wrapPacketListener{
                inner: dtlnet.PacketListenerFromListener(inner),
                keys:  keys,
        }, nil
}

// ==========================================
// WG-пировские операции
// ==========================================

// b64ToHex конвертирует base64-ключ WireGuard в hex (для IPC API).
func b64ToHex(s string) (string, error) {
        b, err := base64.StdEncoding.DecodeString(s)
        if err != nil {
                return "", err
        }
        if len(b) != 32 {
                return "", fmt.Errorf("длина ключа %d != 32", len(b))
        }
        return hex.EncodeToString(b), nil
}

// generateKeyPair генерирует пару Curve25519 ключей для WireGuard.
func generateKeyPair() (privB64, pubB64 string, err error) {
        var priv [32]byte
        if _, err := rand.Read(priv[:]); err != nil {
                return "", "", err
        }
        // Clamping по спецификации Curve25519
        priv[0] &= 248
        priv[31] = (priv[31] & 127) | 64
        pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
        if err != nil {
                return "", "", err
        }
        return base64.StdEncoding.EncodeToString(priv[:]),
                base64.StdEncoding.EncodeToString(pub), nil
}

// upsertPeerInWG добавляет или обновляет peer в WireGuard через UAPI.
func upsertPeerInWG(wgDev *device.Device, pubKeyB64, ip string) {
        if wgDev == nil || pubKeyB64 == "" || ip == "" {
                return
        }
        pubHex, err := b64ToHex(pubKeyB64)
        if err != nil {
                return
        }
        wgDev.IpcSet(fmt.Sprintf("public_key=%s\nallowed_ip=%s/32\n", pubHex, ip))
}

// removePeerFromWG удаляет peer из WireGuard через UAPI.
func removePeerFromWG(wgDev *device.Device, pubKeyB64 string) {
        if wgDev == nil || pubKeyB64 == "" {
                return
        }
        pubHex, err := b64ToHex(pubKeyB64)
        if err != nil {
                return
        }
        wgDev.IpcSet(fmt.Sprintf("public_key=%s\nremove=true\n", pubHex))
}

// ==========================================
// Пул буферов для производительности
// ==========================================

var bufPool = sync.Pool{
        New: func() interface{} {
                b := make([]byte, 1600)
                return &b
        },
}

func getBuf() *[]byte  { return bufPool.Get().(*[]byte) }
func putBuf(b *[]byte) { bufPool.Put(b) }

// isNetTimeout проверяет, является ли ошибка сетевым таймаутом.
func isNetTimeout(err error) bool {
        ne, ok := err.(net.Error)
        return ok && ne.Timeout()
}

// runCmdSilent выполняет команду и возвращает stdout+stderr (тихо, без логов).
func runCmdSilent(name string, args ...string) string {
        out, _ := exec.Command(name, args...).CombinedOutput()
        return strings.TrimSpace(string(out))
}

// getDefaultInterface возвращает имя внешнего сетевого интерфейса.
func getDefaultInterface() string {
        out := runCmdSilent("bash", "-c", "ip route show default | awk '/default/ {print $5}' | head -1")
        if out != "" {
                return strings.TrimSpace(out)
        }
        out = runCmdSilent("bash", "-c", "ip -o link show | awk -F': ' '{print $2}' | grep -v -E 'lo|wg|tun|bypass' | head -1")
        if out != "" {
                return strings.TrimSpace(out)
        }
        return "eth0"
}

// ==========================================
// Listener — основной WDTT сервер
// ==========================================

// Listener представляет WDTT DTLS-сервер с WireGuard.
// Это самостоятельный компонент, который можно запустить/остановить.
type Listener struct {
        cfg      *config.ServerConfig
        userMgr  *users.Manager
        wrapKeys *wrapKeyStore
        wgDev    *device.Device
        cancel   context.CancelFunc
        wg       sync.WaitGroup

        // Статистика
        totalConns        int64
        activeConns       int32
        totalBytesFromCli int64
        totalBytesToCli   int64
}

// NewListener создаёт WDTT listener. Не запускает его — нужно вызвать Start().
func NewListener(cfg *config.ServerConfig, userMgr *users.Manager) (*Listener, error) {
        l := &Listener{
                cfg:      cfg,
                userMgr:  userMgr,
                wrapKeys: newWrapKeyStore(),
        }

        // Собираем все активные пароли из users.Manager для WRAP-ключей
        allUsers := userMgr.List()
        passwords := make([]string, 0, len(allUsers))
        for _, u := range allUsers {
                if u.Active && u.Password != "" {
                        passwords = append(passwords, u.Password)
                }
        }

        // Устанавливаем WRAP-ключи (главный пароль + пароли пользователей)
        if err := l.wrapKeys.SetPasswords(cfg.WDTT.Password, passwords); err != nil {
                return nil, fmt.Errorf("инициализация WRAP ключей: %w", err)
        }

        log.Printf("[WDTT] Инициализирован: %d WRAP-ключей", l.wrapKeys.Count())
        return l, nil
}

// Start запускает WDTT сервер: создаёт TUN/WireGuard, DTLS listener, принимает соединения.
func (l *Listener) Start() error {
        ctx, cancel := context.WithCancel(context.Background())
        l.cancel = cancel

        // 1. Запускаем userspace WireGuard
        wgDev, err := l.createWGDevice()
        if err != nil {
                cancel()
                return fmt.Errorf("создание WireGuard устройства: %w", err)
        }
        l.wgDev = wgDev

        // 2. Синхронизируем известных пользователей как WG peers
        l.syncPeersToWG()

        // 3. Настраиваем NAT/форвардинг
        if err := l.setupNAT(); err != nil {
                log.Printf("[WDTT] Предупреждение: NAT: %v", err)
        }

        // 4. Создаём wrap-слушатель (UDP с деобфускацией)
        addr, err := net.ResolveUDPAddr("udp", l.cfg.WDTT.DTLSAddr)
        if err != nil {
                cancel()
                return fmt.Errorf("парсинг DTLS адреса: %w", err)
        }

        if l.wrapKeys.Count() == 0 {
                cancel()
                return errors.New("[WDTT] нет активных WRAP-ключей (нет пользователей с паролями)")
        }

        wrapListener, err := listenWrapped(addr, l.wrapKeys)
        if err != nil {
                cancel()
                return fmt.Errorf("wrap listener: %w", err)
        }

        // 5. Оборачиваем в DTLS
        cert, _ := selfsign.GenerateSelfSigned()
        dtlsListener, err := dtls.NewListenerWithOptions(
                wrapListener,
                dtls.WithCertificates(cert),
                dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
                dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256),
                dtls.WithConnectionIDGenerator(dtls.RandomCIDGenerator(8)),
                dtls.WithMTU(1100),
        )
        if err != nil {
                cancel()
                return fmt.Errorf("DTLS listener: %w", err)
        }

        // Закрываем DTLS listener при отмене контекста
        context.AfterFunc(ctx, func() { dtlsListener.Close() })

        wgEndpoint := fmt.Sprintf("127.0.0.1:%d", l.cfg.WireGuard.InternalPort)

        log.Printf("[WDTT] Сервер запущен: DTLS=%s, WG=%s", l.cfg.WDTT.DTLSAddr, wgEndpoint)

        // 6. Основной цикл приёма соединений
        l.wg.Add(1)
        go func() {
                defer l.wg.Done()
                for {
                        dtlsConn, err := dtlsListener.Accept()
                        if err != nil {
                                select {
                                case <-ctx.Done():
                                        return
                                default:
                                        log.Printf("[WDTT] Accept ошибка: %v", err)
                                }
                                continue
                        }
                        l.wg.Add(1)
                        go func(c net.Conn) {
                                defer l.wg.Done()
                                defer c.Close()
                                l.handleConn(ctx, c, wgEndpoint, wgDev)
                        }(dtlsConn)
                }
        }()

        return nil
}

// Stop останавливает WDTT сервер.
func (l *Listener) Stop() {
        if l.cancel != nil {
                l.cancel()
        }

        // Останавливаем WireGuard
        if l.wgDev != nil {
                l.wgDev.Close()
                log.Printf("[WDTT] WireGuard устройство закрыто")
        }

        // Удаляем TUN интерфейс
        if l.cfg != nil {
                runCmdSilent("ip", "link", "del", l.cfg.WireGuard.InterfaceName)
        }

        l.wg.Wait()
        log.Printf("[WDTT] Сервер остановлен")
}

// Stats возвращает статистику соединений.
func (l *Listener) Stats() (totalConns int64, activeConns int32, bytesFrom, bytesTo int64) {
        return atomic.LoadInt64(&l.totalConns),
                atomic.LoadInt32(&l.activeConns),
                atomic.LoadInt64(&l.totalBytesFromCli),
                atomic.LoadInt64(&l.totalBytesToCli)
}

// AddPassword добавляет новый WRAP-ключ (при добавлении нового пользователя).
func (l *Listener) AddPassword(password string) error {
        return l.wrapKeys.AddPassword(password)
}

// RemovePassword удаляет WRAP-ключ.
func (l *Listener) RemovePassword(password string) {
        l.wrapKeys.RemovePassword(password)
}

// RefreshKeys полностью пересоздаёт набор WRAP-ключей из текущих пользователей.
func (l *Listener) RefreshKeys() error {
        allUsers := l.userMgr.List()
        passwords := make([]string, 0, len(allUsers))
        for _, u := range allUsers {
                if u.Active && u.Password != "" {
                        passwords = append(passwords, u.Password)
                }
        }
        return l.wrapKeys.SetPasswords(l.cfg.WDTT.Password, passwords)
}

// syncPeersToWG синхронизирует известных пользователей с WireGuard peer'ами.
func (l *Listener) syncPeersToWG() {
        if l.wgDev == nil {
                return
        }
        peers := l.userMgr.GetWGPeerConfigs()
        count := 0
        for _, p := range peers {
                l.wgDev.IpcSet(fmt.Sprintf("public_key=%s\nallowed_ip=%s\n", p.PublicKey, p.AllowedIPs))
                count++
        }
        if count > 0 {
                log.Printf("[WDTT] Синхронизировано %d WG peers", count)
        }
}

// ==========================================
// WireGuard устройство
// ==========================================

// createWGDevice создаёт userspace WireGuard устройство с TUN.
func (l *Listener) createWGDevice() (*device.Device, error) {
        ifaceName := l.cfg.WireGuard.InterfaceName

        // Удаляем старый интерфейс если есть
        runCmdSilent("ip", "link", "del", ifaceName)
        time.Sleep(100 * time.Millisecond)

        // Создаём TUN
        tunDev, err := tun.CreateTUN(ifaceName, l.cfg.WireGuard.MTU)
        if err != nil {
                return nil, fmt.Errorf("CreateTUN: %w", err)
        }

        actualName, err := tunDev.Name()
        if err != nil {
                tunDev.Close()
                return nil, fmt.Errorf("TUN name: %w", err)
        }

        // Создаём WireGuard устройство
        logger := device.NewLogger(device.LogLevelError, "[WG] ")
        bind := conn.NewDefaultBind()
        dev := device.NewDevice(tunDev, bind, logger)

        // Конфигурируем: приватный ключ, порт
        serverPrivHex, err := b64ToHex(l.cfg.WireGuard.PrivateKey)
        if err != nil {
                dev.Close()
                return nil, fmt.Errorf("конвертация приватного ключа: %w", err)
        }

        if err := dev.IpcSet(fmt.Sprintf(
                "private_key=%s\nlisten_port=%d\n",
                serverPrivHex, l.cfg.WireGuard.InternalPort,
        )); err != nil {
                dev.Close()
                return nil, fmt.Errorf("IpcSet: %w", err)
        }

        // Поднимаем устройство
        if err := dev.Up(); err != nil {
                dev.Close()
                return nil, fmt.Errorf("device.Up: %w", err)
        }

        // Настраиваем IP адрес, MTU, link up
        if err := l.configureInterface(actualName); err != nil {
                dev.Close()
                return nil, err
        }

        // Запускаем UAPI listener для управления WireGuard
        go func() {
                uapiFile, err := ipc.UAPIOpen(actualName)
                if err != nil {
                        return
                }
                uapi, err := ipc.UAPIListen(actualName, uapiFile)
                if err != nil {
                        return
                }
                defer uapi.Close()
                for {
                        c, err := uapi.Accept()
                        if err != nil {
                                return
                        }
                        go dev.IpcHandle(c)
                }
        }()

        log.Printf("[WDTT] WireGuard запущен: %s, порт %d, MTU %d",
                actualName, l.cfg.WireGuard.InternalPort, l.cfg.WireGuard.MTU)

        return dev, nil
}

// configureInterface настраивает IP, MTU и поднимает интерфейс.
func (l *Listener) configureInterface(ifaceName string) error {
        serverCIDR := l.cfg.WireGuard.ServerIP + "/" + strings.Split(l.cfg.WireGuard.Subnet, "/")[1]
        for _, cmd := range [][]string{
                {"ip", "addr", "add", serverCIDR, "dev", ifaceName},
                {"ip", "link", "set", "mtu", fmt.Sprintf("%d", l.cfg.WireGuard.MTU), "dev", ifaceName},
                {"ip", "link", "set", ifaceName, "up"},
        } {
                out, err := runCmdSilent(cmd[0], cmd[1:]...)
                if err != nil && !strings.Contains(out, "File exists") {
                        return fmt.Errorf("%s: %s", strings.Join(cmd, " "), out)
                }
        }
        return nil
}

// setupNAT настраивает NAT (MASQUERADE) для трафика из WG-подсети.
func (l *Listener) setupNAT() error {
        ifaceName := l.cfg.WireGuard.InterfaceName
        extIface := getDefaultInterface()
        serverCIDR := l.cfg.WireGuard.ServerIP + "/" + strings.Split(l.cfg.WireGuard.Subnet, "/")[1]

        // Включаем IP forward
        exec.Command("bash", "-c", "echo 1 > /proc/sys/net/ipv4/ip_forward").Run()

        // Пытаемся nftables, затем iptables
        if _, err := exec.LookPath("nft"); err == nil {
                // Очищаем старые правила
                exec.Command("nft", "delete", "table", "ip", "wdtt_bypass").Run()
                // Создаём таблицу и цепочку
                exec.Command("nft", "add", "table", "ip", "wdtt_bypass").Run()
                exec.Command("nft", "add", "chain", "ip", "wdtt_bypass", "postrouting",
                        "{ type nat hook postrouting priority 100; }").Run()
                exec.Command("nft", "add", "rule", "ip", "wdtt_bypass", "postrouting",
                        "ip", "saddr", serverCIDR, "oifname", extIface, "masquerade").Run()
                // Forward rules
                exec.Command("nft", "add", "table", "inet", "wdtt_bypass").Run()
                exec.Command("nft", "add", "chain", "inet", "wdtt_bypass", "forward",
                        "{ type filter hook forward priority 0; policy accept; }").Run()
                exec.Command("nft", "add", "rule", "inet", "wdtt_bypass", "forward",
                        "iifname", ifaceName, "accept").Run()
                exec.Command("nft", "add", "rule", "inet", "wdtt_bypass", "forward",
                        "oifname", ifaceName, "accept").Run()
                log.Printf("[WDTT] NAT: nftables MASQUERADE (ext: %s)", extIface)
                return nil
        }

        if _, err := exec.LookPath("iptables"); err == nil {
                for i := 0; i < 5; i++ {
                        exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
                                "-s", serverCIDR, "-o", extIface, "-m", "comment",
                                "--comment", "BYPASS_MANAGED", "-j", "MASQUERADE").Run()
                }
                exec.Command("iptables", "-t", "nat", "-I", "POSTROUTING", "1",
                        "-s", serverCIDR, "-o", extIface, "-m", "comment",
                        "--comment", "BYPASS_MANAGED", "-j", "MASQUERADE").Run()
                // Forward rules
                for i := 0; i < 5; i++ {
                        exec.Command("iptables", "-D", "FORWARD", "-i", ifaceName,
                                "-m", "comment", "--comment", "BYPASS_MANAGED", "-j", "ACCEPT").Run()
                        exec.Command("iptables", "-D", "FORWARD", "-o", ifaceName,
                                "-m", "comment", "--comment", "BYPASS_MANAGED", "-j", "ACCEPT").Run()
                }
                exec.Command("iptables", "-A", "FORWARD", "-i", ifaceName,
                        "-m", "comment", "--comment", "BYPASS_MANAGED", "-j", "ACCEPT").Run()
                exec.Command("iptables", "-A", "FORWARD", "-o", ifaceName,
                        "-m", "comment", "--comment", "BYPASS_MANAGED", "-j", "ACCEPT").Run()
                log.Printf("[WDTT] NAT: iptables MASQUERADE (ext: %s)", extIface)
                return nil
        }

        return fmt.Errorf("не найдены nftables/iptables")
}

// getNextIP находит следующий свободный IP в WG-подсети.
func (l *Listener) getNextIP() string {
        prefix := l.cfg.WireGuard.Subnet
        parts := strings.Split(strings.Split(prefix, "/")[0], ".")
        if len(parts) != 4 {
                return ""
        }

        used := make(map[string]bool)
        allUsers := l.userMgr.List()
        for _, u := range allUsers {
                if u.WGIP != "" {
                        used[u.WGIP] = true
                }
        }

        for b3 := 0; b3 <= 255; b3++ {
                for b4 := 1; b4 <= 254; b4++ {
                        ip := fmt.Sprintf("%s.%s.%d.%d", parts[0], parts[1], b3, b4)
                        if ip == l.cfg.WireGuard.ServerIP {
                                continue
                        }
                        if !used[ip] {
                                return ip
                        }
                }
        }
        return ""
}

// ==========================================
// Обработка DTLS-соединений
// ==========================================

// handleConn обрабатывает одно DTLS-соединение с клиентом.
// Протокол:
//  1. DTLS handshake
//  2. Клиент отправляет GETCONF:port|deviceID|password
//  3. Сервер проверяет пароль через users.Manager, регистрирует устройство, выдаёт WG конфиг
//  4. Клиент отправляет READY
//  5. Двусторонний прокси: DTLS ↔ UDP (WireGuard)
func (l *Listener) handleConn(ctx context.Context, clientConn net.Conn, wgEndpoint string, wgDev *device.Device) {
        atomic.AddInt64(&l.totalConns, 1)
        defer atomic.AddInt64(&l.totalConns, -1)

        var connDeviceID string

        dtlsConn, ok := clientConn.(*dtls.Conn)
        if !ok {
                return
        }

        // DTLS handshake с таймаутом
        hctx, hcancel := context.WithTimeout(ctx, 60*time.Second)
        if err := dtlsConn.HandshakeContext(hctx); err != nil {
                hcancel()
                log.Printf("[WDTT] DTLS handshake ошибка от %s: %v", clientConn.RemoteAddr().String(), err)
                return
        }
        hcancel()

        atomic.AddInt32(&l.activeConns, 1)
        defer atomic.AddInt32(&l.activeConns, -1)

        // Читаем первый пакет (таймаут 30 сек)
        buf := make([]byte, 1600)
        clientConn.SetReadDeadline(time.Now().Add(30 * time.Second))
        n, err := clientConn.Read(buf)
        if err != nil {
                return
        }
        clientConn.SetReadDeadline(time.Time{})

        firstPacket := buf[:n]
        firstStr := string(firstPacket)

        // === Обработка GETCONF: выдача WireGuard конфигурации ===
        if strings.HasPrefix(firstStr, "GETCONF:") {
                parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(firstStr, "GETCONF:")), "|")
                clientPort := "9000"
                deviceID := "unknown"
                password := ""
                if len(parts) > 0 {
                        clientPort = parts[0]
                }
                if len(parts) > 1 {
                        deviceID = parts[1]
                }
                if len(parts) > 2 {
                        password = parts[2]
                }

                connDeviceID = deviceID

                // Аутентификация через users.Manager
                user, authErr := l.userMgr.AuthenticateByPassword(password, deviceID)

                if authErr != nil {
                        // Формируем понятный ответ об ошибке
                        var denyMsg string
                        switch authErr.Error() {
                        case "wrong_password":
                                denyMsg = "DENIED:wrong_password"
                                log.Printf("[WDTT] Отказ (неверный пароль) от %s", deviceID)
                        case "device_mismatch":
                                denyMsg = "DENIED:device_mismatch"
                                log.Printf("[WDTT] Отказ (привязка к другому устройству) от %s", deviceID)
                        case "expired":
                                denyMsg = "DENIED:expired"
                                log.Printf("[WDTT] Отказ (пароль истёк) от %s", deviceID)
                        default:
                                denyMsg = "DENIED:auth_error"
                                log.Printf("[WDTT] Отказ (%v) от %s", authErr, deviceID)
                        }
                        clientConn.Write([]byte(denyMsg))
                } else {
                        // Аутентификация успешна — регистрируем/обновляем устройство
                        deviceIP := l.registerDevice(user, deviceID)
                        if deviceIP == "" {
                                clientConn.Write([]byte("NOCONF"))
                                log.Printf("[WDTT] Не удалось назначить IP для %s", deviceID)
                        } else {
                                // Генерируем пару ключей WireGuard для клиента
                                clientPrivKey, clientPubKey, keyErr := generateKeyPair()
                                if keyErr != nil {
                                        clientConn.Write([]byte("NOCONF"))
                                        log.Printf("[WDTT] Генерация ключей ошибка: %v", keyErr)
                                } else {
                                        // Обновляем пользователя в менеджере
                                        _ = l.userMgr.Update(user.ID, func(u *users.User) {
                                                u.WGPublicKey = clientPubKey
                                                u.WGIP = deviceIP
                                        })

                                        // Добавляем peer в WireGuard
                                        upsertPeerInWG(wgDev, clientPubKey, deviceIP)

                                        // Обновляем last seen
                                        l.userMgr.UpdateLastSeen(user.ID)

                                        // Строим конфиг WireGuard для клиента
                                        serverPubB64 := l.cfg.WireGuard.PrivateKey
                                        // Нам нужен публичный ключ сервера — сохраняем при создании
                                        // Для простоты вычисляем из приватного
                                        var serverPrivBytes [32]byte
                                        decoded, _ := base64.StdEncoding.DecodeString(l.cfg.WireGuard.PrivateKey)
                                        copy(serverPrivBytes[:], decoded)
                                        serverPub, _ := curve25519.X25519(serverPrivBytes[:], curve25519.Basepoint)
                                        serverPubB64 = base64.StdEncoding.EncodeToString(serverPub)

                                        config := l.buildClientConfig(serverPubB64, clientPrivKey, deviceIP, clientPort)
                                        clientConn.Write([]byte(config))
                                        log.Printf("[WDTT] Конфиг выдан: user=%s device=%s ip=%s", user.ID, deviceID, deviceIP)
                                }
                        }
                }

                // Читаем следующий пакет после GETCONF (таймаут 5 минут для настройки клиента)
                clientConn.SetReadDeadline(time.Now().Add(5 * time.Minute))
                n, err = clientConn.Read(buf)
                if err != nil {
                        return
                }
                clientConn.SetReadDeadline(time.Time{})
                firstPacket = buf[:n]
                firstStr = string(firstPacket)

        } else if strings.HasPrefix(firstStr, "AUTH:") {
                // === AUTH: только аутентификация (устройство уже зарегистрировано) ===
                parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(firstStr, "AUTH:")), "|")
                if len(parts) > 0 {
                        connDeviceID = parts[0]
                }

                // Обновляем last seen если знаем device
                if connDeviceID != "" {
                        allUsers := l.userMgr.List()
                        for _, u := range allUsers {
                                if u.DeviceID == connDeviceID {
                                        l.userMgr.UpdateLastSeen(u.ID)
                                        break
                                }
                        }
                }

                clientConn.SetReadDeadline(time.Now().Add(5 * time.Minute))
                n, err = clientConn.Read(buf)
                if err != nil {
                        return
                }
                clientConn.SetReadDeadline(time.Time{})
                firstPacket = buf[:n]
                firstStr = string(firstPacket)
        }

        // === READY: клиент готов к проксированию ===
        if firstStr == "READY" {
                clientConn.Write([]byte("READY_OK"))
                clientConn.SetReadDeadline(time.Now().Add(10 * time.Minute))
                n, err = clientConn.Read(buf)
                if err != nil {
                        return
                }
                clientConn.SetReadDeadline(time.Time{})
                firstPacket = buf[:n]
        }

        // === WireGuard прокси ===
        wgConn, err := net.Dial("udp", wgEndpoint)
        if err != nil {
                log.Printf("[WDTT] Не удалось подключиться к WG: %v", err)
                return
        }
        defer wgConn.Close()

        // Увеличиваем буферы для производительности
        if uc, ok := wgConn.(*net.UDPConn); ok {
                uc.SetReadBuffer(2 * 1024 * 1024)
                uc.SetWriteBuffer(2 * 1024 * 1024)
        }

        // Пересылаем первый пакет в WireGuard
        if _, err := wgConn.Write(firstPacket); err != nil {
                return
        }
        atomic.AddInt64(&l.totalBytesFromCli, int64(len(firstPacket)))

        // Контекст для этой сессии
        pctx, pcancel := context.WithCancel(ctx)
        defer pcancel()

        context.AfterFunc(pctx, func() {
                clientConn.SetDeadline(time.Now())
                wgConn.SetDeadline(time.Now())
        })

        var proxyWg sync.WaitGroup
        proxyWg.Add(2)

        // Горутина: Клиент → WireGuard
        go func() {
                defer proxyWg.Done()
                defer pcancel()
                b := getBuf()
                defer putBuf(b)
                for {
                        select {
                        case <-pctx.Done():
                                return
                        default:
                        }
                        clientConn.SetReadDeadline(time.Now().Add(30 * time.Minute))
                        nn, err := clientConn.Read(*b)
                        if err != nil {
                                return
                        }
                        // Пропускаем DTLS keepalive (1 байт 0xFF)
                        if nn == 1 && (*b)[0] == 0xFF {
                                continue
                        }
                        atomic.AddInt64(&l.totalBytesFromCli, int64(nn))
                        if _, err := wgConn.Write((*b)[:nn]); err != nil {
                                return
                        }
                }
        }()

        // Горутина: WireGuard → Клиент
        go func() {
                defer proxyWg.Done()
                defer pcancel()
                b := getBuf()
                defer putBuf(b)
                for {
                        select {
                        case <-pctx.Done():
                                return
                        default:
                        }
                        wgConn.SetReadDeadline(time.Now().Add(30 * time.Minute))
                        nn, err := wgConn.Read(*b)
                        if err != nil {
                                if isNetTimeout(err) {
                                        if pctx.Err() != nil {
                                                return
                                        }
                                        continue
                                }
                                return
                        }
                        atomic.AddInt64(&l.totalBytesToCli, int64(nn))
                        if _, err := clientConn.Write((*b)[:nn]); err != nil {
                                return
                        }
                }
        }()

        proxyWg.Wait()
}

// registerDevice регистрирует устройство для пользователя, назначая IP.
// Если устройство уже есть — возвращает существующий IP.
func (l *Listener) registerDevice(user *users.User, deviceID string) string {
        // Если у пользователя уже есть IP и deviceID совпадает — возвращаем его
        if user.WGIP != "" && user.DeviceID == deviceID {
                return user.WGIP
        }

        // Если у пользователя уже есть IP (привязка к другому устройству) — ошибку не возвращаем,
        // а переназначаем (для совместимости с одним устройством на пользователя)
        if user.WGIP != "" && user.DeviceID != "" && user.DeviceID != deviceID {
                log.Printf("[WDTT] Устройство заменено: user=%s old=%s new=%s", user.ID, user.DeviceID, deviceID)
        }

        // Назначаем новый IP
        newIP := l.getNextIP()
        if newIP == "" {
                return ""
        }

        // Обновляем пользователя
        _ = l.userMgr.Update(user.ID, func(u *users.User) {
                u.DeviceID = deviceID
                u.WGIP = newIP
        })

        return newIP
}

// buildClientConfig формирует текстовый конфиг WireGuard для клиента.
func (l *Listener) buildClientConfig(serverPublic, clientPrivate, clientIP, clientPort string) string {
        return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
DNS = %s
MTU = %d

[Peer]
PublicKey = %s
AllowedIPs = 0.0.0.0/0
Endpoint = 127.0.0.1:%s
PersistentKeepalive = %d`,
                clientPrivate, clientIP,
                "8.8.8.8", // DNS по умолчанию для клиентов
                l.cfg.WireGuard.MTU,
                serverPublic, clientPort,
                l.cfg.WireGuard.Keepalive,
        )
}