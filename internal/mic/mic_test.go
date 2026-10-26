package mic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInUse(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "card0", "pcm0c", "sub0")
	_ = os.MkdirAll(p, 0o755)
	_ = os.WriteFile(filepath.Join(p, "status"), []byte("closed\n"), 0o644)
	if InUseUnder(d) {
		t.Fatal("closed counted as in use")
	}
	_ = os.WriteFile(filepath.Join(p, "status"), []byte("state: RUNNING\nowner_pid   : 1\n"), 0o644)
	if !InUseUnder(d) {
		t.Fatal("running not seen")
	}
}
