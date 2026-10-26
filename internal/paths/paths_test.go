package paths

import "testing"

func TestExpand(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for in, want := range map[string]string{
		"~/.config/x": "/home/u/.config/x", "~": "/home/u", "/abs/p": "/abs/p", "rel/p": "rel/p", "": "", "~user/x": "~user/x",
	} {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}
