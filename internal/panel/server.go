package panel

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"time"

	"github.com/frpanel/frpanel/internal/version"
	"github.com/frpanel/frpanel/web"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Start brings up background loops and the HTTP server, blocking until ctx is
// cancelled, then shuts down gracefully.
func (a *App) Start(ctx context.Context) error {
	go a.pingDBLoop(ctx)
	go a.pipe.Run(ctx)
	go a.tcpPingLoop(ctx)
	a.nodes.StartAll(ctx)

	handler := a.router()
	srv := &http.Server{
		Addr:              a.cfg.Listen,
		Handler:           h2c.NewHandler(handler, &http2.Server{}), // HTTP/2 cleartext (LAN) + TLS gets h2 automatically
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		a.log.Info("shutting down")
		a.nodes.StopAll()
		sh, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()

	if a.cfg.TLS.Enabled {
		a.log.Info("panel listening (https)", "addr", a.cfg.Listen, "version", version.Version)
		err := srv.ListenAndServeTLS(a.cfg.TLS.CertFile, a.cfg.TLS.KeyFile)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	a.log.Info("panel listening (http)", "addr", a.cfg.Listen, "version", version.Version)
	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *App) router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(a.recoverer)
	r.Use(securityHeaders)
	r.Use(middleware.Compress(5))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("ok")) })

	r.Route("/api", func(r chi.Router) {
		r.Post("/login", a.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(a.auth.RequireAuth)
			r.Use(a.auth.CSRFGuard)

			r.Post("/logout", a.handleLogout)
			r.Get("/me", a.handleMe)
			r.Get("/overview", a.handleOverview)

			r.Get("/nodes", a.handleListNodes)
			r.Post("/nodes", a.handleCreateNode)
			r.Get("/nodes/{id}", a.handleGetNode)
			r.Put("/nodes/{id}", a.handleUpdateNode)
			r.Delete("/nodes/{id}", a.handleDeleteNode)
			r.Post("/nodes/{id}/rotate-token", a.handleRotateToken)
			r.Post("/nodes/{id}/update-agent", a.handleUpdateAgent)
			r.Get("/nodes/{id}/history", a.handleNodeHistory)

			r.Get("/mappings", a.handleListMappings)
			r.Post("/mappings", a.handleCreateMapping)
			r.Put("/mappings/{id}", a.handleUpdateMapping)
			r.Delete("/mappings/{id}", a.handleDeleteMapping)
			r.Post("/mappings/{id}/toggle", a.handleToggleMapping)
			r.Post("/mappings/port-check", a.handlePortCheck)
			r.Post("/mappings/local-check", a.handleLocalCheck)

			r.Get("/logs", a.handleListLogs)
			r.Post("/logs/clean", a.handleCleanLogs)

			r.Get("/settings", a.handleGetSettings)
			r.Put("/settings", a.handleUpdateSettings)
			r.Post("/settings/password", a.handleChangePassword)
			r.Get("/settings/check-update", a.handleCheckUpdate)
			r.Post("/settings/self-update", a.handleSelfUpdate)
			r.Post("/settings/backup", a.handleBackup)

			r.Get("/internal/metrics", a.handleInternalMetrics)

			r.Get("/ws", a.hub.ServeWS)
		})
	})

	if a.cfg.Debug {
		r.Mount("/debug", debugRouter())
	}

	a.mountSPA(r)
	return r
}

// recoverer turns panics into a JSON 500 (API) so a handler bug never crashes
// the process; the browser also has an ErrorBoundary for its own faults.
func (a *App) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.log.Error("panic in handler", "err", rec, "path", r.URL.Path)
				if strings.HasPrefix(r.URL.Path, "/api/") {
					failCode(w, CodeInternal, "服务器内部错误")
				} else {
					http.Error(w, "internal error", 500)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; connect-src 'self' ws: wss:; "+
				"object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func debugRouter() http.Handler {
	r := chi.NewRouter()
	r.HandleFunc("/pprof/*", pprof.Index)
	r.HandleFunc("/pprof/cmdline", pprof.Cmdline)
	r.HandleFunc("/pprof/profile", pprof.Profile)
	r.HandleFunc("/pprof/symbol", pprof.Symbol)
	r.HandleFunc("/pprof/trace", pprof.Trace)
	return r
}

// mountSPA serves the embedded React build: hashed assets with immutable cache,
// everything else falling back to index.html for client-side routing.
func (a *App) mountSPA(r chi.Router) {
	dist, err := web.DistFS()
	if err != nil {
		a.log.Error("embed dist", "err", err)
		return
	}
	indexBytes, _ := fs.ReadFile(dist, "index.html")
	fileServer := http.FileServer(http.FS(dist))

	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		p := strings.TrimPrefix(req.URL.Path, "/")
		if p == "" {
			serveIndex(w, indexBytes)
			return
		}
		if f, err := dist.Open(p); err == nil {
			f.Close()
			// Long-lived immutable cache for fingerprinted assets.
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, req)
			return
		}
		// Unknown path that isn't an asset -> SPA entry (client routing).
		serveIndex(w, indexBytes)
	})
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if len(index) == 0 {
		w.WriteHeader(200)
		w.Write([]byte("<!doctype html><title>FRPanel</title><p>前端资源未构建。请运行 make web。</p>"))
		return
	}
	w.WriteHeader(200)
	w.Write(index)
}

// handleInternalMetrics exposes health counters for the 72h leak-check
// acceptance criterion (goroutines, ws clients, dropped batches, uptime).
func (a *App) handleInternalMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	ok(w, map[string]any{
		"goroutines":      runtime.NumGoroutine(),
		"ws_clients":      a.hub.Clients(),
		"dropped_batches": a.droppedBatches.Load(),
		"db_healthy":      a.DBUp(),
		"heap_alloc":      m.HeapAlloc,
		"heap_sys":        m.HeapSys,
		"uptime_sec":      int64(time.Since(a.startedAt).Seconds()),
		"version":         version.Version,
	})
}
