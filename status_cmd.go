package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/paths"
)

// cmdStatus asks the running agent for its state. Without an agent it falls
// back to the pause flag so the command still answers.
func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output (the agent's full state)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reply, err := control.Notify("state")
	var st control.State
	running := err == nil && json.Unmarshal([]byte(reply), &st) == nil
	_, perr := os.Stat(paths.PausedFile())
	flagged := perr == nil
	if *asJSON {
		if !running {
			return printJSON(map[string]any{"running": false, "paused": flagged})
		}
		var raw map[string]any
		_ = json.Unmarshal([]byte(reply), &raw)
		raw["running"] = true
		return printJSON(raw)
	}
	if !running {
		if flagged {
			fmt.Println("agent not running; pause flag is set (it starts paused)")
		} else {
			fmt.Println("agent not running")
		}
		return nil
	}
	state := "sending"
	switch {
	case st.Paused:
		state = "paused (nothing is sent)"
	case st.Asleep:
		state = "asleep (laptop suspended)"
	case st.Private:
		state = "private mode (presence only)"
	}
	fmt.Printf("agent %s, pid %d, up %s: %s\n", st.Version, st.PID, time.Since(st.Started).Round(time.Second), state)
	if !st.Send.LastOK.IsZero() {
		fmt.Printf("last delivered %s ago to %s\n", time.Since(st.Send.LastOK).Round(time.Second), st.EndpointHost)
	} else if st.Send.Result != "" {
		fmt.Printf("last send attempt: %s\n", st.Send.Result)
	}
	if st.Send.Queued > 0 {
		fmt.Printf("%d sample(s) queued offline\n", st.Send.Queued)
	}
	return nil
}
