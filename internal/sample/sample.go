// Package sample defines the wire format the site's ingest endpoint expects.
package sample

import (
	"encoding/json"
	"fmt"
	"time"
)

type App struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type Site struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// Media describes what is playing. Title and Artist are only ever set for
// native music players; for browsers and YouTube they stay nil.
type Media struct {
	Player string  `json:"player"`
	State  string  `json:"state"`
	Source string  `json:"source"`
	Title  *string `json:"title,omitempty"`
	Artist *string `json:"artist,omitempty"`
	// Videos is only set for source "youtube".
	Videos *int `json:"videos,omitempty"`
}

type Battery struct {
	Pct      int  `json:"pct"`
	Charging bool `json:"charging"`
}

// Sample is one point on the timeline. Every field except T and Active is
// optional.
// FocusItem is how long one app was focused during the sample's 30-second
// bucket. It lets the site see what I mostly did, not just the last window
// I flicked to.
type FocusItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	S        int    `json:"s"`
}

// Sys is how hard the laptop is working, and what the agent itself costs.
type Sys struct {
	CPU      int     `json:"cpu"`             // whole machine, percent
	Mem      int     `json:"mem"`             // RAM in use, percent
	MemGB    float64 `json:"memGB"`           // RAM in use, GB
	TotalGB  float64 `json:"totalGB"`         // RAM installed, GB
	AgentMB  float64 `json:"agentMB"`         // this agent's resident memory
	AgentCPU float64 `json:"agentCPU"`        // this agent's CPU, percent of one core
	GuiMB    float64 `json:"guiMB,omitempty"` // the settings app, when it's open
	UptimeH  float64 `json:"uptimeH"`
}

type Sample struct {
	T       string      `json:"t"`
	Active  bool        `json:"active"`
	IdleS   *int        `json:"idleS,omitempty"`
	App     *App        `json:"app,omitempty"`
	Site    *Site       `json:"site,omitempty"`
	Repo    string      `json:"repo,omitempty"`
	Claude  *bool       `json:"claude,omitempty"`
	Media   *Media      `json:"media,omitempty"`
	Battery *Battery    `json:"battery,omitempty"`
	Focus   []FocusItem `json:"focus,omitempty"`
	Sys     *Sys        `json:"sys,omitempty"`
}

// Format renders t as an ISO UTC timestamp, floored to a multiple of
// interval so retries and bursts land on the same idempotency key.
func Format(t time.Time, interval time.Duration) string {
	t = t.UTC()
	if interval > 0 {
		t = t.Truncate(interval)
	}
	return t.Format("2006-01-02T15:04:05Z")
}

// Parse reads a Sample's T field.
func (s Sample) Time() (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05Z", s.T)
}

// Final is the sample sent when the laptop goes away for good (suspend,
// shutdown, pause): nothing but "not active".
func Final(t time.Time, interval time.Duration) Sample {
	return Sample{T: Format(t, interval), Active: false}
}

// Signature is a stable string of everything that should trigger an
// immediate send when it changes. It leaves out t, idleS and battery so a
// ticking idle counter never causes a send by itself.
func (s Sample) Signature() string {
	c := s
	c.T = ""
	c.IdleS = nil
	c.Battery = nil
	c.Focus = nil
	c.Sys = nil
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("%v", err)
	}
	return string(b)
}

// Batch is the request body.
type Batch struct {
	Samples []Sample `json:"samples"`
}

func Int(v int) *int       { return &v }
func Bool(v bool) *bool    { return &v }
func Str(v string) *string { return &v }
