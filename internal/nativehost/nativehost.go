// Package nativehost is the program a browser launches for native messaging
// (`desk-agent native-host`). It reads length-prefixed JSON frames from the
// extension on stdin, keeps only the two fields it expects, and forwards them
// as one line of JSON to the agent's unix socket.
//
// It is a privacy choke point on purpose: frames with any unexpected field are
// dropped whole, hosts must look like plain host names, and nothing is logged
// except counts.
package nativehost

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/bridge"
)

// maxFrame bounds a frame from the browser. Real ones are under 100 bytes.
const maxFrame = 4096

// In is what the extension sends.
type In struct {
	Host           string `json:"host"`
	YouTubeAudible bool   `json:"youtubeAudible"`
}

// FamilyFromArgs tells which browser launched us. Chromium-family browsers
// pass the extension origin ("chrome-extension://<id>/") as the first
// argument; Firefox passes the manifest path and the extension id.
func FamilyFromArgs(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "chrome-extension://") {
			return "chromium"
		}
	}
	return "firefox"
}

// ReadFrame reads one native-messaging frame (4-byte native-endian length).
func ReadFrame(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.NativeEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > maxFrame {
		return nil, fmt.Errorf("frame of %d bytes rejected", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Convert validates a frame and produces the line for the agent.
func Convert(family string, frame []byte) ([]byte, error) {
	var in In
	dec := json.NewDecoder(bytes.NewReader(frame))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, err
	}
	w := bridge.Wire{Browser: family, Host: in.Host, YouTube: in.YouTubeAudible}
	line, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	// Run it through the agent's own validation so a bad host is blanked
	// here too and the socket never sees it.
	checked, err := bridge.Decode(line)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(checked)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Run forwards frames from in until it closes. The agent may not be running
// yet or may restart: the connection is (re)opened lazily and messages sent
// while it is down are dropped (the next one is at most ~15 s away).
func Run(in io.Reader, sock string, family string) error {
	br := bufio.NewReader(in)
	var conn net.Conn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	for {
		frame, err := ReadFrame(br)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			if strings.Contains(err.Error(), "rejected") {
				return err // a frame of absurd size: hang up
			}
			return err
		}
		line, err := Convert(family, frame)
		if err != nil {
			continue // drop frames carrying anything unexpected
		}
		for attempt := 0; attempt < 2; attempt++ {
			if conn == nil {
				c, derr := net.DialTimeout("unix", sock, 500*time.Millisecond)
				if derr != nil {
					break
				}
				conn = c
			}
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			if _, werr := conn.Write(line); werr == nil {
				break
			}
			conn.Close()
			conn = nil
		}
	}
}
