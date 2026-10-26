// Package gitrepo finds the name of the git repository containing a
// directory. Only the repository's folder name is ever returned, never a path.
package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
)

// Name returns the basename of the git top-level directory containing dir,
// or "" if dir is not inside a repository. Linked worktrees report the main
// repository's name. A repository rooted at $HOME (a dotfiles repo) is
// ignored, since its name would be the username.
func Name(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	home, _ := os.UserHomeDir()
	d := filepath.Clean(dir)
	for {
		if top, ok := topLevel(d); ok {
			if top == home {
				return ""
			}
			return filepath.Base(top)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// topLevel reports the repository root if d directly contains a .git entry.
func topLevel(d string) (string, bool) {
	g := filepath.Join(d, ".git")
	st, err := os.Lstat(g)
	if err != nil {
		return "", false
	}
	if st.IsDir() {
		return d, true
	}
	// A .git file: "gitdir: /repo/.git/worktrees/name" for a linked worktree,
	// ".../.git/modules/x" for a submodule.
	b, err := os.ReadFile(g)
	if err != nil {
		return d, true
	}
	line := strings.TrimSpace(string(b))
	line = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if i := strings.Index(line, "/.git/worktrees/"); i > 0 {
		return line[:i], true
	}
	return d, true
}
