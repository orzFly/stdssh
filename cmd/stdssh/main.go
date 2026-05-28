// Command stdssh runs an SSH server over inherited stdin/stdout. It is
// intended to be launched as a `kubectl exec` target via an ssh ProxyCommand.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"stdssh/internal/hostkey"
	"stdssh/internal/server"
	"stdssh/internal/stdioconn"

	"golang.org/x/crypto/ssh"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		var fe flagError
		fmt.Fprintln(os.Stderr, "stdssh:", err)
		if errors.As(err, &fe) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run() error {
	var (
		hostkeyPath = flag.String("hostkey", "", "path to PEM private key; generated on first use if missing")
		hostkeySeed = flag.String("hostkey-seed", "", "derive a deterministic ed25519 hostkey from this seed")
		shellPath   = flag.String("shell", "", "override $SHELL for exec/shell sessions")
		logLevel    = flag.String("log-level", "info", "log level: error|warn|info|debug")
		noPTY       = flag.Bool("no-pty", false, "reject pty-req requests")
		noSFTP      = flag.Bool("no-sftp", false, "reject the sftp subsystem")
		noForward   = flag.Bool("no-forward", false, "reject direct-tcpip and tcpip-forward")
		noAgentFwd  = flag.Bool("no-agent-forward", false, "reject SSH agent forwarding")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "stdssh — SSH server over stdio (see specs/20260528-stdio-ssh-server)\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		return err
	}

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	signer, err := selectHostKey(*hostkeyPath, *hostkeySeed)
	if err != nil {
		return err
	}

	logger := buildLogger(*logLevel)
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	slog.SetDefault(logger)

	cfg := server.Config{
		HostKey:       signer,
		Logger:        logger,
		ServerVersion: "SSH-2.0-stdssh_" + version,
		Shell:         *shellPath,
		AllowPTY:      !*noPTY,
		AllowSFTP:     !*noSFTP,
		AllowForward:  !*noForward,
		AllowAgentFwd: !*noAgentFwd,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	conn := stdioconn.New()
	return server.Run(ctx, conn, cfg)
}

func selectHostKey(path, seed string) (ssh.Signer, error) {
	switch {
	case path != "" && seed != "":
		return nil, flagError("--hostkey and --hostkey-seed are mutually exclusive")
	case path == "" && seed == "":
		return nil, flagError("one of --hostkey or --hostkey-seed is required")
	case path != "":
		return hostkey.LoadOrCreate(path)
	default:
		return hostkey.FromSeed(seed)
	}
}

func buildLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

type flagError string

func (e flagError) Error() string { return string(e) }
