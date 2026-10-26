package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/once-human/desk-agent/internal/claude"
	"github.com/once-human/desk-agent/internal/paths"
)

// cmdHook is what Claude Code runs on every hook event. It must be fast, print
// nothing (a UserPromptSubmit hook's stdout is added to Claude's context) and
// always exit 0 (exit code 2 would block the user's prompt).
func cmdHook(args []string) {
	defer os.Exit(0)
	if len(args) != 1 || (args[0] != "start" && args[0] != "stop") {
		return
	}
	done := make(chan claude.HookInput, 1)
	go func() { done <- claude.ParseHookInput(os.Stdin) }()
	var in claude.HookInput
	select {
	case in = <-done:
	case <-time.After(1500 * time.Millisecond):
	}
	if in.CWD == "" {
		in.CWD, _ = os.Getwd()
	}
	_ = claude.Record(paths.ClaudeState(), args[0], in, time.Now())
}

func cmdInstallHooks(args []string) error {
	bin, err := binaryPath(args)
	if err != nil {
		return err
	}
	path := claude.SettingsPath()
	added, err := claude.InstallHooks(path, bin)
	if err != nil {
		return err
	}
	if len(added) == 0 {
		fmt.Println("Claude Code hooks already installed in", path)
		return nil
	}
	fmt.Printf("added %d hooks to %s: %v\n", len(added), path, added)
	fmt.Println("(a one-time backup was saved next to it as settings.json.desk-agent.bak)")
	fmt.Println("Restart running Claude Code sessions for them to take effect.")
	return nil
}

func cmdUninstallHooks() error {
	n, err := claude.UninstallHooks(claude.SettingsPath())
	if err != nil {
		return err
	}
	fmt.Printf("removed %d desk-agent hooks from %s\n", n, claude.SettingsPath())
	return nil
}

// binaryPath returns the absolute path hooks should call: --binary PATH, else
// the running executable, else whatever `desk-agent` resolves to on PATH.
func binaryPath(args []string) (string, error) {
	for i, a := range args {
		if a == "--binary" && i+1 < len(args) {
			return args[i+1], nil
		}
	}
	if p, err := os.Executable(); err == nil {
		return p, nil
	}
	return exec.LookPath("desk-agent")
}
