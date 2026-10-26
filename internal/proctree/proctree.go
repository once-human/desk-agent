// Package proctree walks /proc to find which git repository a focused
// terminal or editor is working in. It reads only process cwd and, for
// editors, command-line arguments that are existing directories; the result
// is a repository name, never a path.
package proctree

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/once-human/desk-agent/internal/gitrepo"
)

// Proc reads a /proc-shaped tree rooted at Root (default "/proc").
type Proc struct{ Root string }

func (p Proc) root() string {
	if p.Root == "" {
		return "/proc"
	}
	return p.Root
}

func (p Proc) pidDir(pid int) string { return filepath.Join(p.root(), strconv.Itoa(pid)) }

// Children lists direct child pids, using the kernel's per-task children
// files and falling back to scanning every process's parent.
func (p Proc) Children(pid int) []int {
	var out []int
	found := false
	tasks, _ := filepath.Glob(filepath.Join(p.pidDir(pid), "task", "*", "children"))
	for _, f := range tasks {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		found = true
		for _, s := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(s); err == nil {
				out = append(out, n)
			}
		}
	}
	if found {
		return out
	}
	entries, _ := os.ReadDir(p.root())
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if ppid, _ := p.statFields(n); ppid == pid {
			out = append(out, n)
		}
	}
	return out
}

// statFields returns ppid and start time (clock ticks since boot) from
// /proc/<pid>/stat. The comm field may contain spaces and parentheses, so
// parsing starts after the last ')'.
func (p Proc) statFields(pid int) (ppid int, start uint64) {
	b, err := os.ReadFile(filepath.Join(p.pidDir(pid), "stat"))
	if err != nil {
		return 0, 0
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0, 0
	}
	f := strings.Fields(s[i+1:])
	// f[0]=state f[1]=ppid ... f[19]=starttime
	if len(f) < 20 {
		return 0, 0
	}
	ppid, _ = strconv.Atoi(f[1])
	start, _ = strconv.ParseUint(f[19], 10, 64)
	return ppid, start
}

func (p Proc) cwd(pid int) string {
	d, err := os.Readlink(filepath.Join(p.pidDir(pid), "cwd"))
	if err != nil {
		return ""
	}
	return d
}

func (p Proc) cmdlineDirs(pid int) []string {
	b, err := os.ReadFile(filepath.Join(p.pidDir(pid), "cmdline"))
	if err != nil {
		return nil
	}
	var dirs []string
	for i, a := range strings.Split(strings.TrimRight(string(b), "\x00"), "\x00") {
		if i == 0 || a == "" || strings.HasPrefix(a, "-") || !filepath.IsAbs(a) {
			continue
		}
		if st, err := os.Stat(a); err == nil && st.IsDir() {
			dirs = append(dirs, a)
		}
	}
	return dirs
}

// Descendants returns pid and everything below it (breadth first, capped).
func (p Proc) Descendants(pid, limit int) []int {
	seen := map[int]bool{pid: true}
	queue := []int{pid}
	for i := 0; i < len(queue) && len(queue) < limit; i++ {
		for _, c := range p.Children(queue[i]) {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, c)
			}
		}
	}
	return queue
}

// RepoFor returns the repository name for the window process pid and its
// descendants. The most recently started process that is inside a repository
// wins (the shell or editor you just opened). With editor=true, directory
// arguments on the command line count too, since editors are usually started
// from a launcher whose cwd is not the project.
func (p Proc) RepoFor(pid int, editor bool) string {
	type cand struct {
		start uint64
		repo  string
	}
	var cands []cand
	for _, d := range p.Descendants(pid, 64) {
		_, start := p.statFields(d)
		if r := gitrepo.Name(p.cwd(d)); r != "" {
			cands = append(cands, cand{start, r})
		}
		if editor {
			for _, dir := range p.cmdlineDirs(d) {
				if r := gitrepo.Name(dir); r != "" {
					// command-line projects outrank the process's own cwd
					cands = append(cands, cand{start + 1, r})
				}
			}
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].start > cands[j].start })
	return cands[0].repo
}
