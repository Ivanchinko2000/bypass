package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"bypass-server/internal/config"
	"bypass-server/internal/lists"
	"bypass-server/internal/users"
)

// Server предоставляет HTTP API для управления сервером.
type Server struct {
	router       *chi.Mux
	users        *users.Manager
	lists        *lists.Manager
	config       *config.ServerConfig
	authToken    string
}

// NewServer создаёт HTTP API сервер.
func NewServer(cfg *config.ServerConfig, userMgr *users.Manager, listMgr *lists.Manager) *Server {
	s := &Server{
		router:    chi.NewRouter(),
		users:     userMgr,
		lists:     listMgr,
		config:    cfg,
		authToken: cfg.API.AuthToken,
	}
	s.setupRoutes()
	return s
}

// setupRoutes настраивает HTTP-маршруты.
func (s *Server) setupRoutes() {
	// CORS (для веб-клиентов если понадобится)
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
	}))

	// Публичные маршруты (требуют базовой авторизации)
	s.router.Post("/api/auth", s.handleAuth)
	s.router.Get("/api/lists", s.handleGetLists)
	s.router.Get("/api/health", s.handleHealth)

	// Административные маршруты (требуют auth token)
	s.router.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Post("/api/lists/update", s.handleUpdateLists)
		r.Get("/api/users", s.handleListUsers)
		r.Post("/api/users", s.handleAddUser)
		r.Put("/api/users/{id}", s.handleUpdateUser)
		r.Delete("/api/users/{id}", s.handleDeleteUser)
		r.Post("/api/reload", s.handleReload)
	})
}

// Start запускает HTTP сервер.
func (s *Server) Start() error {
	addr := s.config.API.Listen
	log.Printf("[API] Сервер запущен на %s", addr)
	return http.ListenAndServe(addr, s.router)
}

// authMiddleware проверяет Authorization заголовок.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		// Формат: "Bearer <token>" или просто <token>
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}

		if token != s.authToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ==========================================
// Обработчики
// ==========================================

// handleAuth — POST /api/auth
// Проверяет пароль пользователя и возвращает конфигурацию для подключения.
// Body: {"password": "...", "device_id": "..."}
// Response: {"status": "ok", "config": {...}} или ошибка
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		DeviceID string `json:"device_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "неверный JSON")
		return
	}

	if req.Password == "" {
		s.writeError(w, http.StatusBadRequest, "пароль не указан")
		return
	}

	// Аутентификация
	user, err := s.users.AuthenticateByPassword(req.Password, req.DeviceID)
	if err != nil {
		switch err.Error() {
		case "wrong_password":
			s.writeError(w, http.StatusForbidden, "DENIED:wrong_password")
		case "expired":
			s.writeError(w, http.StatusForbidden, "DENIED:expired")
		case "device_mismatch":
			s.writeError(w, http.StatusForbidden, "DENIED:device_mismatch")
		default:
			s.writeError(w, http.StatusForbidden, "DENIED:unknown")
		}
		return
	}

	// Обновляем last_seen
	s.users.UpdateLastSeen(user.ID)

	// Формируем конфигурацию для клиента
	clientConfig := map[string]interface{}{
		"mode":       s.config.Mode,
		"server_ip":  s.config.Listen.WDTTDTLSAddr, // для WDTT: DTLS адрес
		"wg_mtu":     s.config.WireGuard.MTU,
		"vless_uuid": user.VLESSUUID,
		// Списки маршрутизации для split tunneling на клиенте
		"dpi_domains": s.lists.GetDPIDomains(),
		"geo_domains": s.lists.GetGeoDomains(),
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"user_id": user.ID,
		"config": clientConfig,
	})

	log.Printf("[API] Авторизация: user=%s device=%s", user.ID, req.DeviceID)
}

// handleGetLists — GET /api/lists
// Возвращает текущие DPI и Geo списки.
func (s *Server) handleGetLists(w http.ResponseWriter, r *http.Request) {
	dpiList := s.lists.GetDPIList()
	geoList := s.lists.GetGeoList()

	// Конвертируем в массивы строк
	dpiDomains := make([]string, len(dpiList))
	geoDomains := make([]string, len(geoList))
	for i, e := range dpiList {
		dpiDomains[i] = e.Original
	}
	for i, e := range geoList {
		geoDomains[i] = e.Original
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"dpi":  dpiDomains,
		"geo":  geoDomains,
		"count": map[string]int{
			"dpi": len(dpiDomains),
			"geo": len(geoDomains),
		},
	})
}

// handleUpdateLists — POST /api/lists/update (admin)
// Перезагружает списки из файлов.
func (s *Server) handleUpdateLists(w http.ResponseWriter, r *http.Request) {
	if err := s.lists.Reload(); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("ошибка перезагрузки: %v", err))
		return
	}

	dpiCount, geoCount := s.lists.Stats()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "reloaded",
		"dpi":    dpiCount,
		"geo":    geoCount,
	})
	log.Printf("[API] Списки перезагружены: DPI=%d, Geo=%d", dpiCount, geoCount)
}

// handleHealth — GET /api/health
// Возвращает статус сервера.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dpiCount, geoCount := s.lists.Stats()
	userCount := s.users.Count()

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"mode":      s.config.Mode,
		"users":     userCount,
		"dpi_count": dpiCount,
		"geo_count": geoCount,
	})
}

// handleListUsers — GET /api/users (admin)
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": s.users.List(),
		"count": s.users.Count(),
	})
}

// handleAddUser — POST /api/users (admin)
func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	var user users.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		s.writeError(w, http.StatusBadRequest, "неверный JSON")
		return
	}

	if user.ID == "" || user.Password == "" {
		s.writeError(w, http.StatusBadRequest, "id и password обязательны")
		return
	}

	if err := s.users.Add(user); err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status": "created",
		"id":     user.ID,
	})
	log.Printf("[API] Пользователь создан: %s", user.ID)
}

// handleUpdateUser — PUT /api/users/{id} (admin)
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var update map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		s.writeError(w, http.StatusBadRequest, "неверный JSON")
		return
	}

	err := s.users.Update(id, func(u *users.User) {
		if v, ok := update["password"].(string); ok {
			u.Password = v
		}
		if v, ok := update["vless_uuid"].(string); ok {
			u.VLESSUUID = v
		}
		if v, ok := update["wg_public_key"].(string); ok {
			u.WGPublicKey = v
		}
		if v, ok := update["wg_ip"].(string); ok {
			u.WGIP = v
		}
		if v, ok := update["active"].(bool); ok {
			u.Active = v
		}
		if v, ok := update["expires"].(string); ok {
			u.Expires = v
		}
		if v, ok := update["description"].(string); ok {
			u.Description = v
		}
	})

	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "updated"})
}

// handleDeleteUser — DELETE /api/users/{id} (admin)
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.users.Delete(id); err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "deleted"})
	log.Printf("[API] Пользователь удалён: %s", id)
}

// handleReload — POST /api/reload (admin)
// Перезагружает списки и конфигурацию (аналог SIGHUP).
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := s.lists.Reload(); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dpiCount, geoCount := s.lists.Stats()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "reloaded",
		"dpi":    dpiCount,
		"geo":    geoCount,
	})
}

// ==========================================
// Helpers
// ==========================================

// writeJSON записывает JSON-ответ.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeError записывает JSON-ошибку.
func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]interface{}{"error": message})
}