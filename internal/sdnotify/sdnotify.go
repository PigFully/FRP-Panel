// Package sdnotify implements the minimal subset of the systemd notify protocol
// needed for Type=notify units with a watchdog (READY=1 and WATCHDOG=1).
package sdnotify

import (
	"context"
	"net"
	"os"
	"strconv"
	"time"
)

// notify sends a datagram to $NOTIFY_SOCKET. No-op when unset (non-systemd run).
func notify(state string) error {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return nil
	}
	addr := &net.UnixAddr{Name: sock, Net: "unixgram"}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}

// Ready signals the service manager that startup is complete.
func Ready() error { return notify("READY=1") }

// Status sets a free-form status line shown by `systemctl status`.
func Status(s string) error { return notify("STATUS=" + s) }

// WatchdogInterval returns half of WATCHDOG_USEC (the recommended ping period),
// or 0 if the watchdog is not enabled for this process.
func WatchdogInterval() time.Duration {
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	// Ping at half the timeout.
	return time.Duration(n) * time.Microsecond / 2
}

// RunWatchdog pings the watchdog until ctx is cancelled. Safe to call even when
// the watchdog is disabled (it simply returns).
func RunWatchdog(ctx context.Context) {
	iv := WatchdogInterval()
	if iv <= 0 {
		return
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = notify("WATCHDOG=1")
		}
	}
}
