package review

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "editor.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func validNonEmpty(b []byte) error {
	if strings.Contains(string(b), "BROKEN") {
		return errString("contains BROKEN")
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func TestEditAcceptsEditedFile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "cfg", "sites.toml")
	ed := script(t, `printf '\n# reviewed\n' >> "$1"`)
	var out bytes.Buffer
	if err := Edit(dest, "proposal\n", validNonEmpty, Options{Editor: ed, In: strings.NewReader(""), Out: &out}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "proposal\n\n# reviewed\n" {
		t.Errorf("dest = %q", got)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if _, err := os.Stat(dest + ".review"); err == nil {
		t.Error("review file should be removed on success")
	}
}

func TestEditLoopsUntilValid(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "sites.toml")
	counter := filepath.Join(t.TempDir(), "n")
	// first run breaks the file, second run fixes it
	ed := script(t, `n=$(cat `+counter+` 2>/dev/null || echo 0); n=$((n+1)); echo $n > `+counter+`
if [ "$n" = 1 ]; then echo BROKEN >> "$1"; else sed -i 's/BROKEN//' "$1"; fi`)
	var out bytes.Buffer
	if err := Edit(dest, "ok\n", validNonEmpty, Options{Editor: ed, In: strings.NewReader("y\n"), Out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "not valid") {
		t.Error("the user must be told why the file was rejected")
	}
	got, _ := os.ReadFile(dest)
	if strings.Contains(string(got), "BROKEN") {
		t.Errorf("dest = %q", got)
	}
}

func TestEditDeclineKeepsDestUntouchedAndWork(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sites.toml")
	os.WriteFile(dest, []byte("original\n"), 0o600)
	ed := script(t, `echo BROKEN >> "$1"`)
	err := Edit(dest, "proposal\n", validNonEmpty, Options{Editor: ed, In: strings.NewReader("n\n"), Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), ".review") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "original\n" {
		t.Errorf("dest was modified: %q", got)
	}
	if _, err := os.Stat(dest + ".review"); err != nil {
		t.Error("the user's edits must be kept")
	}
}

func TestNoEditValidates(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "a.toml")
	if err := Edit(dest, "BROKEN\n", validNonEmpty, Options{NoEdit: true}); err == nil {
		t.Error("invalid content must be rejected even with --no-edit")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("nothing should be written")
	}
	if err := Edit(dest, "fine\n", validNonEmpty, Options{NoEdit: true}); err != nil {
		t.Fatal(err)
	}
}

func TestEditorFailureIsReported(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "a.toml")
	err := Edit(dest, "x\n", validNonEmpty, Options{Editor: script(t, "exit 3"), In: strings.NewReader(""), Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestFindEditorPrefersExplicitThenEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor --wait")
	if e, _ := FindEditor(""); e != "myeditor --wait" {
		t.Errorf("got %q", e)
	}
	t.Setenv("VISUAL", "visual-one")
	if e, _ := FindEditor(""); e != "visual-one" {
		t.Errorf("VISUAL should win: %q", e)
	}
	if e, _ := FindEditor("explicit"); e != "explicit" {
		t.Errorf("explicit should win: %q", e)
	}
}
