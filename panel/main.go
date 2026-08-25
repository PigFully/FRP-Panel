// Command panel is the FRPanel management panel. Subcommands:
//
//	panel run           run the service (default)
//	panel migrate       apply DB migrations and exit
//	panel ensure-admin  create the admin user if none exists, print the password
//	panel version       print version info
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/frpanel/frpanel/internal/panel"
	"github.com/frpanel/frpanel/internal/sdnotify"
	"github.com/frpanel/frpanel/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		runService([]string{})
		return
	}
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println("frpanel-panel", version.Info())
	case "migrate":
		cmdMigrate(os.Args[2:])
	case "ensure-admin":
		cmdEnsureAdmin(os.Args[2:])
	case "reset-admin":
		cmdResetAdmin(os.Args[2:])
	case "run":
		runService(os.Args[2:])
	default:
		runService(os.Args[1:])
	}
}

func logger(debug bool) *slog.Logger {
	lvl := slog.LevelInfo
	if debug {
		lvl = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func loadCfg(args []string) *panel.Config {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/frp-panel/config.yaml", "config file path")
	_ = fs.Parse(args)
	cfg, err := panel.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config failed:", err)
		os.Exit(1)
	}
	return cfg
}

func cmdMigrate(args []string) {
	cfg := loadCfg(args)
	db, err := panel.Connect(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect:", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := panel.Migrate(db); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	fmt.Println("迁移完成")
}

func cmdEnsureAdmin(args []string) {
	cfg := loadCfg(args)
	db, err := panel.Connect(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect:", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := panel.Migrate(db); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	store := panel.NewStore(db)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	u, err := store.GetUserByUsername(ctx, "admin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "query admin:", err)
		os.Exit(1)
	}
	if u != nil {
		fmt.Println("管理员账户已存在，跳过创建。")
		return
	}
	pw := panel.RandomPassword()
	hash, err := panel.HashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash:", err)
		os.Exit(1)
	}
	if _, err := store.CreateUser(ctx, "admin", hash); err != nil {
		fmt.Fprintln(os.Stderr, "create admin:", err)
		os.Exit(1)
	}
	fmt.Println("==== 管理员账户已创建（以下密码仅显示这一次）====")
	fmt.Println("  账户: admin")
	fmt.Println("  密码:", pw)
	fmt.Println("=================================================")
}

func cmdResetAdmin(args []string) {
	cfg := loadCfg(args)
	db, err := panel.Connect(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect:", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := panel.Migrate(db); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	store := panel.NewStore(db)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pw := panel.RandomPassword()
	hash, err := panel.HashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash:", err)
		os.Exit(1)
	}
	u, _ := store.GetUserByUsername(ctx, "admin")
	if u == nil {
		if _, err := store.CreateUser(ctx, "admin", hash); err != nil {
			fmt.Fprintln(os.Stderr, "create admin:", err)
			os.Exit(1)
		}
	} else if _, err := store.UpdatePassword(ctx, u.ID, hash); err != nil { // bumps pwd_version -> old sessions die
		fmt.Fprintln(os.Stderr, "reset:", err)
		os.Exit(1)
	}
	fmt.Println("==== 管理员密码已重置（旧会话立即失效，密码仅显示这一次）====")
	fmt.Println("  账户: admin")
	fmt.Println("  密码:", pw)
	fmt.Println("============================================================")
}

func runService(args []string) {
	cfg := loadCfg(args)
	log := logger(cfg.Debug)

	db, err := panel.Connect(cfg)
	if err != nil {
		log.Error("db open failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	store := panel.NewStore(db)
	app := panel.New(cfg, store, log)

	// Best-effort startup migration + panel_name load. If the DB is down the
	// panel still starts (degraded) and recovers via its ping loop.
	ctxStart, cancelStart := context.WithTimeout(context.Background(), 20*time.Second)
	if err := db.PingContext(ctxStart); err == nil {
		if err := panel.Migrate(db); err != nil {
			log.Error("migrate failed (continuing degraded)", "err", err)
		} else {
			app.LoadRuntimeSettings(ctxStart)
		}
	} else {
		log.Error("db unreachable at startup (continuing degraded)", "err", err)
	}
	cancelStart()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A successful self-update swaps the binary then calls stop(): Start returns
	// after a graceful shutdown, the process exits 0 and systemd
	// (Restart=always) respawns the new build.
	app.OnRestart(stop)
	go sdnotify.RunWatchdog(ctx)
	_ = sdnotify.Ready()
	_ = sdnotify.Status("running")

	if err := app.Start(ctx); err != nil {
		log.Error("panel exited", "err", err)
		os.Exit(1)
	}
}
