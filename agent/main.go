// Command agent is the FRPanel node agent. Subcommands:
//
//	agent init      generate/upgrade config + cert + frps.toml, print receipt
//	agent receipt   reprint the registration receipt
//	agent run       run the service (default)
//	agent version   print version info
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/frpanel/frpanel/internal/agent"
	"github.com/frpanel/frpanel/internal/sdnotify"
	"github.com/frpanel/frpanel/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		runService()
		return
	}
	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Println("frpanel-agent", version.Info())
	case "init":
		cmdInit(os.Args[2:])
	case "receipt":
		cmdReceipt(os.Args[2:])
	case "run":
		os.Args = append(os.Args[:1], os.Args[2:]...)
		runService()
	default:
		runService()
	}
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dataDir := fs.String("datadir", agent.DefaultDataDir, "agent data directory")
	bind := fs.String("bind", ":8443", "management bind address")
	ip := fs.String("ip", "", "override detected public IP")
	_ = fs.Parse(args)
	cfg, err := agent.Init(agent.InitOptions{DataDir: *dataDir, BindAddr: *bind, ManualIP: *ip})
	if err != nil {
		fmt.Fprintln(os.Stderr, "init failed:", err)
		os.Exit(1)
	}
	if cfg.PublicIP == "" {
		fmt.Fprintln(os.Stderr, "警告: 未能自动探测公网 IP，请用 -ip 指定后重新执行 init")
	}
	if err := cfg.PrintReceiptBox(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "receipt error:", err)
		os.Exit(1)
	}
}

func cmdReceipt(args []string) {
	fs := flag.NewFlagSet("receipt", flag.ExitOnError)
	dataDir := fs.String("datadir", agent.DefaultDataDir, "agent data directory")
	_ = fs.Parse(args)
	cfg, err := agent.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config failed (run `agent init` first):", err)
		os.Exit(1)
	}
	if err := cfg.PrintReceiptBox(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "receipt error:", err)
		os.Exit(1)
	}
}

func runService() {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := fs.String("datadir", agent.DefaultDataDir, "agent data directory")
	_ = fs.Parse(os.Args[1:])

	log := logger()
	cfg, err := agent.Load(*dataDir)
	if err != nil {
		log.Error("load config failed; run `agent init` first", "err", err)
		os.Exit(1)
	}
	srv, err := agent.NewServer(cfg, log)
	if err != nil {
		log.Error("server init failed", "err", err)
		os.Exit(1)
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A successful self-update swaps the binary then calls stop(): Run returns,
	// the process exits 0 and systemd (Restart=always) respawns the new build.
	srv.OnRestart(stop)
	go sdnotify.RunWatchdog(ctx)
	_ = sdnotify.Ready()
	_ = sdnotify.Status("running")

	if err := srv.Run(ctx); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}
