package hypr

import "testing"

func TestParseFocusJSONIgnoresTitleAndWipes(t *testing.T) {
	raw := []byte(`{"address":"0x1","class":"Firefox","title":"my secret tab","pid":321}`)
	f := ParseFocusJSON(raw)
	if f.Class != "firefox" || f.PID != 321 {
		t.Fatalf("%+v", f)
	}
	for _, b := range raw {
		if b != 0 {
			t.Fatal("raw buffer not wiped")
		}
	}
}

func TestParseFocusFallsBackToInitialClassAndEmpty(t *testing.T) {
	if f := ParseFocusJSON([]byte(`{"class":"","initialClass":"Kitty","pid":1}`)); f.Class != "kitty" {
		t.Fatalf("%+v", f)
	}
	if f := ParseFocusJSON([]byte(`{}`)); f.Class != "" {
		t.Fatalf("%+v", f)
	}
	if f := ParseFocusJSON([]byte(`not json`)); f.Class != "" {
		t.Fatalf("%+v", f)
	}
}

func TestIsFocusEventOnlyV2(t *testing.T) {
	if !IsFocusEvent("activewindowv2>>0xabc") {
		t.Fatal("v2 should match")
	}
	for _, l := range []string{"activewindow>>kitty,title", "windowtitlev2>>0x1,x", "workspace>>1"} {
		if IsFocusEvent(l) {
			t.Fatalf("%q must not match", l)
		}
	}
}
