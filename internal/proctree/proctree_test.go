package proctree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fake builds a /proc entry. start is the starttime field.
func fake(t *testing.T, root string, pid, ppid int, start uint64, comm, cwd, cmdline string, children ...int) {
	t.Helper()
	d := filepath.Join(root, fmt.Sprint(pid))
	_ = os.MkdirAll(filepath.Join(d, "task", fmt.Sprint(pid)), 0o755)
	stat := fmt.Sprintf("%d (%s) S %d 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0", pid, comm, ppid, start)
	_ = os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
	if cwd != "" {
		_ = os.Symlink(cwd, filepath.Join(d, "cwd"))
	}
	_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644)
	var kids string
	for _, c := range children {
		kids += fmt.Sprint(c, " ")
	}
	_ = os.WriteFile(filepath.Join(d, "task", fmt.Sprint(pid), "children"), []byte(kids), 0o644)
}

func repo(t *testing.T, base, name string) string {
	d := filepath.Join(base, name)
	_ = os.MkdirAll(filepath.Join(d, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	return d
}

func TestRepoFromTerminalChildShell(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	r := repo(t, base, "1618")
	proc := filepath.Join(base, "proc")
	fake(t, proc, 100, 1, 10, "kitty", filepath.Join(base, "home"), "kitty\x00", 101)
	fake(t, proc, 101, 100, 20, "zsh", filepath.Join(r, "sub"), "zsh\x00", 102)
	fake(t, proc, 102, 101, 30, "nvim", filepath.Join(r, "sub"), "nvim\x00")
	if got := (Proc{Root: proc}).RepoFor(100, false); got != "1618" {
		t.Fatalf("got %q", got)
	}
}

func TestNewestRepoWinsAmongShells(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	a, b := repo(t, base, "old-repo"), repo(t, base, "new-repo")
	proc := filepath.Join(base, "proc")
	fake(t, proc, 100, 1, 10, "kitty", "", "kitty\x00", 101, 102)
	fake(t, proc, 101, 100, 20, "zsh", a, "zsh\x00")
	fake(t, proc, 102, 100, 50, "zsh", b, "zsh\x00")
	if got := (Proc{Root: proc}).RepoFor(100, false); got != "new-repo" {
		t.Fatalf("got %q", got)
	}
}

func TestEditorUsesCommandLineDirectory(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	r := repo(t, base, "desk-agent")
	proc := filepath.Join(base, "proc")
	fake(t, proc, 200, 1, 10, "code", filepath.Join(base, "home"), "code\x00--flag\x00"+r+"\x00")
	p := Proc{Root: proc}
	if got := p.RepoFor(200, true); got != "desk-agent" {
		t.Fatalf("editor: %q", got)
	}
	if got := p.RepoFor(200, false); got != "" {
		t.Fatalf("non-editor must ignore cmdline args, got %q", got)
	}
}

func TestStatParsingSurvivesOddCommNames(t *testing.T) {
	base := t.TempDir()
	proc := filepath.Join(base, "proc")
	fake(t, proc, 7, 3, 99, "we ird) (name", "", "")
	ppid, start := (Proc{Root: proc}).statFields(7)
	if ppid != 3 || start != 99 {
		t.Fatalf("ppid=%d start=%d", ppid, start)
	}
}

func TestChildrenFallbackScansParents(t *testing.T) {
	base := t.TempDir()
	proc := filepath.Join(base, "proc")
	fake(t, proc, 10, 1, 1, "a", "", "")
	fake(t, proc, 11, 10, 2, "b", "", "")
	_ = os.RemoveAll(filepath.Join(proc, "10", "task")) // kernel without CONFIG_PROC_CHILDREN
	got := (Proc{Root: proc}).Children(10)
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("%v", got)
	}
}
