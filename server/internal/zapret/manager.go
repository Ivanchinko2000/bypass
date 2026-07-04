package zapret

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Manager управляет процессом nfqws из пакета Zapret.
// nfqws обрабатывает пакеты из NFQUEUE и модифицирует их для обхода DPI.
type Manager struct {
	mu       sync.Mutex
	binPath  string    // путь к nfqws бинарнику
	args     string    // аргументы запуска
	qNum     int       // номер NFQUEUE
	cmd      *exec.Cmd // запущенный процесс
	running  bool
	stopCh   chan struct{}
}

// NewManager создаёт менеджер Zapret.
func NewManager(binPath, args string, qNum int) *Manager {
	return &Manager{
		binPath: binPath,
		args:    args,
		qNum:    qNum,
		stopCh:  make(chan struct{}),
	}
}

// Start запускает nfqws процесс.
// nfqws читает пакеты из NFQUEUE (номер qNum) и модифицирует их.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		log.Printf("[ZAPRET] nfqws уже запущен")
		return nil
	}

	// Проверяем наличие бинарника
	if _, err := os.Stat(m.binPath); os.IsNotExist(err) {
		return fmt.Errorf("nfqws не найден: %s (установите zapret: sudo apt install zapret)", m.binPath)
	}

	log.Printf("[ZAPRET] Запуск nfqws: %s %s --qnum=%d", m.binPath, m.args, m.qNum)

	// Формируем аргументы
	args := m.parseArgs()

	m.cmd = exec.Command(m.binPath, args...)
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("запуск nfqws: %w", err)
	}

	m.running = true

	// Watchdog: следим за процессом
	go m.watchdog()

	log.Printf("[ZAPRET] nfqws запущен (PID %d)", m.cmd.Process.Pid)
	return nil
}

// Stop останавливает nfqws процесс.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		return nil
	}

	log.Printf("[ZAPRET] Остановка nfqws (PID %d)...", m.cmd.Process.Pid)

	// Сначала SIGTERM
	if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("[ZAPRET] SIGTERM ошибка: %v", err)
	}

	// Ждём завершения с таймаутом
	done := make(chan error, 1)
	go func() {
		done <- m.cmd.Wait()
	}()

	select {
	case <-done:
		log.Printf("[ZAPRET] nfqws остановлен")
	case <-time.After(5 * time.Second):
		log.Printf("[ZAPRET] nfqws не завершился за 5с, SIGKILL...")
		_ = m.cmd.Process.Kill()
	}

	m.running = false
	return nil
}

// Restart перезапускает nfqws (например после обновления списков).
func (m *Manager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return m.Start()
}

// IsRunning возвращает статус nfqws.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// watchdog следит за процессом nfqws и перезапускает при падении.
func (m *Manager) watchdog() {
	for {
		err := m.cmd.Wait()

		m.mu.Lock()
		wasRunning := m.running
		m.running = false
		m.mu.Unlock()

		if !wasRunning {
			// Остановлен вручную
			return
		}

		log.Printf("[ZAPRET] nfqws упал с ошибкой: %v, перезапуск через 3с...", err)

		select {
		case <-m.stopCh:
			return
		case <-time.After(3 * time.Second):
		}

		// Перезапуск
		m.mu.Lock()
		args := m.parseArgs()
		m.cmd = exec.Command(m.binPath, args...)
		m.cmd.Stdout = os.Stdout
		m.cmd.Stderr = os.Stderr

		if err := m.cmd.Start(); err != nil {
			log.Printf("[ZAPRET] Ошибка перезапуска: %v", err)
			m.mu.Unlock()
			return
		}
		m.running = true
		m.mu.Unlock()

		log.Printf("[ZAPRET] nfqws перезапущен (PID %d)", m.cmd.Process.Pid)
	}
}

// parseArgs формирует массив аргументов из строки + добавляет --qnum.
func (m *Manager) parseArgs() []string {
	baseArgs := "--qnum=" + fmt.Sprintf("%d", m.qNum)
	if m.args != "" {
		baseArgs = m.args + " " + baseArgs
	}
	// Простое разбиение по пробелам (достаточно для наших целей)
	var result []string
	inQuote := false
	current := ""
	for _, c := range baseArgs {
		switch {
		case c == '"' || c == '\'':
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			if current != "" {
				result = append(result, current)
				current = ""
			}
		default:
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// CheckInstalled проверяет, установлен ли nfqws в системе.
func CheckInstalled(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// InstallInstructions возвращает инструкции по установке zapret.
func InstallInstructions() string {
	return `Установка Zapret (Ubuntu/Debian):
  sudo apt install -y software-properties-common dirmngr apt-transport-https
  sudo gpg --homedir /tmp --no-default-keyring --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys EDF899A4641B3891
  sudo gpg --homedir /tmp --no-default-keyring --export --armor EDF899A4641B3891 | sudo tee /usr/share/keyrings/zapret.gpg >/dev/null
  echo "deb [signed-by=/usr/share/keyrings/zapret.gpg] http://repo.zapret.info/apt-noble noble main" | sudo tee /etc/apt/sources.list.d/zapret.list
  sudo apt update
  sudo apt install -y zapret

Путь к nfqws обычно: /usr/bin/nfqws`
}