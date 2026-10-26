package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNameWalksUpToRepoRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	repo := filepath.Join(root, "work", "1618")
	deep := filepath.Join(repo, "src", "deep")
	_ = os.MkdirAll(deep, 0o755)
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	if got := Name(deep); got != "1618" {
		t.Fatalf("got %q", got)
	}
	if got := Name(filepath.Join(root, "work")); got != "" {
		t.Fatalf("outside repo should be empty, got %q", got)
	}
}

func TestWorktreeReportsMainRepo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	wt := filepath.Join(root, "wt", "feature-x")
	_ = os.MkdirAll(wt, 0o755)
	gd := filepath.Join(root, "code", "desk-agent", ".git", "worktrees", "feature-x")
	_ = os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gd+"\n"), 0o644)
	if got := Name(wt); got != "desk-agent" {
		t.Fatalf("got %q", got)
	}
}

func TestSubmoduleReportsItsOwnFolder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	sm := filepath.Join(root, "super", "libs", "sub")
	_ = os.MkdirAll(sm, 0o755)
	_ = os.WriteFile(filepath.Join(sm, ".git"), []byte("gitdir: ../../.git/modules/sub\n"), 0o644)
	if got := Name(sm); got != "sub" {
		t.Fatalf("got %q", got)
	}
}

func TestHomeRepoAndRelativePathsAreIgnored(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "onkar")
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, "Downloads"), 0o755)
	if got := Name(filepath.Join(home, "Downloads")); got != "" {
		t.Fatalf("dotfiles repo at $HOME must not leak the username, got %q", got)
	}
	if Name("relative/dir") != "" || Name("") != "" {
		t.Fatal("non-absolute input must yield empty")
	}
}
