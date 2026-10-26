package catalog

import "testing"

func TestWebAppAndNewSeeds(t *testing.T) {
	a, _ := LoadApps("")
	if e := a.Lookup("chrome-web.whatsapp.com__-Default"); e.Name != "WhatsApp" || e.Category != "chat" {
		t.Fatalf("whatsapp web app: %+v", e)
	}
	if e := a.Lookup("com.anthropic.claude"); e.Category != "ai" {
		t.Fatalf("claude: %+v", e)
	}
	if e := a.Lookup("in.onkaryaglewad.deskagent"); e.Category != "tool" {
		t.Fatalf("deskagent: %+v", e)
	}
}
