// Package review is the "open it in $EDITOR and wait" step shared by
// scan-history and scan-apps.
package review

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options lets tests replace the editor and the terminal.
type Options struct {
	Editor string    // command line; empty = $VISUAL, $EDITOR, then nvim/vim/nano/vi
	In     io.Reader // answers to "re-open?" (default os.Stdin)
	Out    io.Writer // messages (default os.Stdout)
	NoEdit bool      // validate and write without opening an editor
}

// FindEditor returns the editor command line to use.
func FindEditor(explicit string) (string, error) {
	for _, c := range []string{explicit, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if strings.TrimSpace(c) != "" {
			return c, nil
		}
	}
	for _, c := range []string{"nvim", "vim", "nano", "vi"} {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no editor found: set $EDITOR (or pass --no-edit)")
}

// WriteAtomic writes data to path via a temp file and rename, mode 0600.
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Edit lets the user review proposal and, once validate accepts what they
// saved, writes it to dest. If validation fails the user is asked whether to
// re-open the editor; if they decline, dest is left untouched and the review
// file is kept so no work is lost.
func Edit(dest, proposal string, validate func([]byte) error, o Options) error {
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.NoEdit {
		if err := validate([]byte(proposal)); err != nil {
			return err
		}
		return WriteAtomic(dest, []byte(proposal))
	}
	editor, err := FindEditor(o.Editor)
	if err != nil {
		return err
	}
	rv := dest + ".review"
	if err := WriteAtomic(rv, []byte(proposal)); err != nil {
		return err
	}
	in := bufio.NewReader(o.In)
	for {
		fmt.Fprintf(o.Out, "Opening %s in %s ... save and quit when done.\n", rv, strings.Fields(editor)[0])
		args := strings.Fields(editor)
		cmd := exec.Command(args[0], append(args[1:], rv)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if o.In != os.Stdin {
			cmd.Stdin = nil // tests: do not hand the editor the scripted answers
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("editor %q failed: %w (review file kept at %s)", args[0], err, rv)
		}
		data, err := os.ReadFile(rv)
		if err != nil {
			return err
		}
		verr := validate(data)
		if verr == nil {
			if err := WriteAtomic(dest, data); err != nil {
				return err
			}
			_ = os.Remove(rv)
			return nil
		}
		fmt.Fprintf(o.Out, "\nThat file is not valid: %v\nRe-open the editor? [Y/n] ", verr)
		ans, _ := in.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "n" || ans == "no" {
			return fmt.Errorf("not saved: %v (your edits are kept in %s)", verr, rv)
		}
	}
}
