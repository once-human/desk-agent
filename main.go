// desk-agent tells a portfolio site, live, what this laptop is doing.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/doctor"
	"github.com/once-human/desk-agent/internal/paths"
)

// version is overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "0.1.0"

const usage = `desk-agent ` + "%s" + `

Usage:
  desk-agent run             run the agent (what the systemd service does)
  desk-agent doctor          check every signal source and print what works
  desk-agent init            create ~/.config/desk-agent with a default config
  desk-agent set-key         store the site key (reads stdin, mode 600)
  desk-agent pause           stop sending; the site shows the laptop as away
  desk-agent resume          start sending again
  desk-agent status          show whether the agent is paused
  desk-agent version
`

func main() {
	doctor.UserAgent = "desk-agent/" + version
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Printf(usage, version)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "run":
		err = cmdRun(ctx)
	case "doctor":
		if doctor.Run(ctx, os.Stdout, version) > 0 {
			os.Exit(1)
		}
	case "init":
		err = cmdInit()
	case "set-key":
		err = cmdSetKey()
	case "pause":
		err = cmdPause(true)
	case "resume":
		err = cmdPause(false)
	case "status":
		err = cmdStatus()
	case "version", "--version", "-v":
		fmt.Println("desk-agent", version)
	case "help", "--help", "-h":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		fmt.Printf(usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "desk-agent:", err)
		os.Exit(1)
	}
}

func cmdInit() error {
	created, err := config.Init()
	if err != nil {
		return err
	}
	if created {
		fmt.Println("wrote", paths.ConfigFile())
	} else {
		fmt.Println(paths.ConfigFile(), "already exists, left untouched")
	}
	if _, _, kerr := config.Key(); kerr != nil {
		fmt.Println("next: run `desk-agent set-key` to store the site key")
	}
	return nil
}

func cmdSetKey() error {
	var key string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Site key (input hidden): ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		key = string(b)
	} else {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		key = line
	}
	if err := config.WriteKey(strings.TrimSpace(key)); err != nil {
		return err
	}
	fmt.Println("key saved to", paths.KeyFile(), "(mode 600)")
	return nil
}

func cmdPause(pause bool) error {
	if err := paths.EnsureDir(paths.StateDir()); err != nil {
		return err
	}
	if pause {
		if err := os.WriteFile(paths.PausedFile(), []byte("paused\n"), 0o600); err != nil {
			return err
		}
		notifyAgent()
		fmt.Println("paused: nothing is sent until you run `desk-agent resume`")
		return nil
	}
	if err := os.Remove(paths.PausedFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	notifyAgent()
	fmt.Println("resumed")
	return nil
}

// notifyAgent tells a running agent to re-read the flag now. If none is
// running, the flag is picked up at its next start.
func notifyAgent() {
	if _, err := control.Notify("reload"); err != nil {
		fmt.Fprintln(os.Stderr, "note:", err, "(the flag applies when it starts)")
	}
}

func cmdStatus() error {
	if _, err := os.Stat(paths.PausedFile()); err == nil {
		fmt.Println("paused")
	} else {
		fmt.Println("running (not paused)")
	}
	return nil
}
