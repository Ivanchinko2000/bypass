package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"bypass-server/internal/api"
	"bypass-server/internal/config"
	"bypass-server/internal/dns"
	"bypass-server/internal/lists"
	"bypass-server/internal/router"
	"bypass-server/internal/users"
	"bypass-server/internal/vless"
	"bypass-server/internal/zapret"
)

func main() {
	// Парсинг аргументов командной строки
	configPath := flag.String("config", "configs/server.yaml", "путь к файлу конфигурации")
	generateTemplate := flag.Bool("gen-template", false, "сгенерировать шаблон конфига и выйти")
	flag.Parse()

	// Генерация шаблона
	if *generateTemplate {
		if err := config.SaveTemplate("configs/server.yaml.example"); err != nil {
			log.Fatalf("Ошибка: %v", err)
		}
		fmt.Println("Шаблон конфигурации создан: configs/server.yaml.example")
		return
	}

	// Загрузка конфигурации
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Ошибка загрузки конфига: %v", err)
	}

	log.Printf("=== Bypass Server v0.1.0 ===")
	log.Printf("Режим: %s", cfg.Mode)

	// ==========================================
	// 1. Менеджер пользователей
	// ==========================================
	userMgr, err := users.NewManager(cfg.UsersFile)
	if err != nil {
		log.Fatalf("Ошибка загрузки пользователей: %v", err)
	}
	log.Printf("[INIT] Пользователей: %d", userMgr.Count())

	// ==========================================
	// 2. Менеджер списков
	// ==========================================
	listMgr := lists.NewManager(cfg.Lists.DPIBlocklist, cfg.Lists.GeoBlocklist)
	if err := listMgr.Load(); err != nil {
		log.Printf("[INIT] Предупреждение: загрузка списков: %v", err)
	}

	// ==========================================
	// 3. Модуль маршрутизации (только для РФ-сервера)
	// ==========================================
	var routerMgr *router.Manager
	if cfg.Mode == "rf" {
		routerMgr = router.NewManager(
			cfg.WireGuard.InterfaceName,
			"", // автоопределение
			cfg.Listen.SOCKS5Addr,
		)
		if err := routerMgr.Init(); err != nil {
			log.Printf("[INIT] Предупреждение: маршрутизация: %v", err)
		}
		if err := routerMgr.SetupIPRules(); err != nil {
			log.Printf("[INIT] Предупреждение: ip rules: %v", err)
		}
	}

	// ==========================================
	// 4. DNS-резолвер (только для РФ-сервера)
	// ==========================================
	var dnsResolver *dns.Resolver
	if cfg.Mode == "rf" && cfg.DNS.Enabled {
		dnsResolver = dns.NewResolver(
			cfg.DNS.Listen,
			cfg.DNS.Upstream,
			cfg.DNS.EUUpstream,
			cfg.DNS.CacheTTL,
		)
		dnsResolver.SetDPIDomains(listMgr.GetDPIDomains())
		dnsResolver.SetGeoDomains(listMgr.GetGeoDomains())
		if err := dnsResolver.Start(); err != nil {
			log.Printf("[INIT] Предупреждение: DNS: %v", err)
		}
	}

	// ==========================================
	// 5. Zapret (только для РФ-сервера)
	// ==========================================
	var zapretMgr *zapret.Manager
	if cfg.Mode == "rf" && cfg.Zapret.Enabled {
		zapretMgr = zapret.NewManager(
			cfg.Zapret.NfqwsPath,
			cfg.Zapret.NfqwsArgs,
			cfg.Zapret.QNum,
		)

		if err := zapretMgr.Start(); err != nil {
			log.Printf("[INIT] Предупреждение: Zapret: %v", err)
			log.Printf("[INIT] %s", zapret.InstallInstructions())
		} else {
			// Настраиваем nftables правило для NFQUEUE
			if routerMgr != nil {
				_ = routerMgr.SetupZapretRule(cfg.Zapret.QNum)
			}
		}
	}

	// ==========================================
	// 6. Xray / VLESS
	// ==========================================
	var vlessMgr *vless.Manager
	if cfg.Mode == "eu" && cfg.VLESS.Server.Listen != "" {
		// ЕС-сервер: запускаем VLESS+Reality сервер
		xrayCfg := vless.XrayExternalConfig{Role: "server"}
		xrayCfg.Server.Listen = cfg.VLESS.Server.Listen
		xrayCfg.Server.PrivateKey = cfg.VLESS.Server.PrivateKey
		xrayCfg.Server.ShortID = cfg.VLESS.Server.ShortID
		xrayCfg.Server.Dest = cfg.VLESS.Server.Dest
		xrayCfg.Server.ServerNames = cfg.VLESS.Server.ServerNames

		vlessMgr = vless.NewManager(
			"server",
			cfg.VLESS.Client.XrayPath,
			"configs",
			xrayCfg,
			"",
		)
		if err := vlessMgr.Start(); err != nil {
			log.Fatalf("[INIT] Ошибка запуска VLESS сервера: %v", err)
		}

	} else if cfg.Mode == "rf" && cfg.VLESS.Client.Enabled {
		// РФ-сервер: запускаем Xray как VLESS+Reality клиент к ЕС
		xrayCfg := vless.XrayExternalConfig{Role: "client"}
		xrayCfg.Client.Enabled = cfg.VLESS.Client.Enabled
		xrayCfg.Client.RemoteAddr = cfg.VLESS.Client.RemoteAddr
		xrayCfg.Client.UUID = cfg.VLESS.Client.UUID
		xrayCfg.Client.PublicKey = cfg.VLESS.Client.PublicKey
		xrayCfg.Client.ShortID = cfg.VLESS.Client.ShortID
		xrayCfg.Client.Fingerprint = cfg.VLESS.Client.Fingerprint
		xrayCfg.Client.ServerName = cfg.VLESS.Client.ServerName

		vlessMgr = vless.NewManager(
			"client",
			cfg.VLESS.Client.XrayPath,
			"configs",
			xrayCfg,
			cfg.Listen.SOCKS5Addr,
		)
		if err := vlessMgr.Start(); err != nil {
			log.Printf("[INIT] Предупреждение: Xray клиент: %v", err)
		}
	}

	// ==========================================
	// 7. HTTP API
	// ==========================================
	var apiServer *api.Server
	if cfg.API.Enabled {
		apiServer = api.NewServer(cfg, userMgr, listMgr)
		go func() {
			if err := apiServer.Start(); err != nil {
				log.Printf("[INIT] API ошибка: %v", err)
			}
		}()
	}

	log.Printf("=== Сервер запущен (режим: %s) ===", cfg.Mode)
	log.Printf("  Пользователей: %d", userMgr.Count())
	if dpiCount, geoCount := listMgr.Stats(); cfg.Mode == "rf" {
		log.Printf("  DPI-записей: %d, Geo-записей: %d", dpiCount, geoCount)
	}

	// ==========================================
	// Ожидание сигналов
	// ==========================================
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for sig := range sigCh {
		switch sig {
		case syscall.SIGHUP:
			// Перезагрузка списков
			log.Printf("[СИГНАЛ] SIGHUP — перезагрузка списков")
			if err := listMgr.Reload(); err != nil {
				log.Printf("[СИГНАЛ] Ошибка перезагрузки: %v", err)
			}
			if dnsResolver != nil {
				dnsResolver.SetDPIDomains(listMgr.GetDPIDomains())
				dnsResolver.SetGeoDomains(listMgr.GetGeoDomains())
			}

		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("[СИГНАЛ] %v — завершение...", sig)

			// Останавливаем компоненты в обратном порядке
			if apiServer != nil {
				log.Printf("[STOP] API...")
			}
			if vlessMgr != nil {
				vlessMgr.Stop()
			}
			if zapretMgr != nil {
				zapretMgr.Stop()
			}
			if dnsResolver != nil {
				dnsResolver.Stop()
			}
			if routerMgr != nil {
				routerMgr.RemoveAllRules()
			}

			log.Printf("=== Сервер остановлен ===")
			return
		}
	}
}