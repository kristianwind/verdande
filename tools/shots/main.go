// Command shots captures the screenshots used on the site and in the README.
//
// It drives a headless Chrome over the DevTools protocol rather than using
// `--screenshot`, because the app needs a session and that flag cannot set a
// cookie. Kept in the repository so the images can be regenerated when the
// interface changes, rather than being a thing somebody did once by hand.
//
//	go run ./tools/shots -url http://localhost:8096 -cookie <session> -out site/screenshots
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
)

var chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

func main() {
	base := flag.String("url", "http://localhost:8096", "the running instance")
	cookie := flag.String("cookie", "", "session cookie value")
	out := flag.String("out", "site/screenshots", "where to write the PNGs")
	width := flag.Int("width", 1400, "viewport width")
	height := flag.Int("height", 900, "viewport height")
	flag.Parse()

	if err := run(*base, *cookie, *out, *width, *height); err != nil {
		fmt.Fprintln(os.Stderr, "shots:", err)
		os.Exit(1)
	}
}

type shot struct {
	name string
	path string
	// theme is applied before the capture, so both palettes can be shown without
	// a second run.
	theme string
	wait  time.Duration
}

func run(base, cookie, out string, width, height int) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	profile, err := os.MkdirTemp("", "verdande-shots-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)

	chrome := exec.Command(chromePath,
		"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-sync", "--hide-scrollbars",
		"--remote-debugging-port=9223", "--user-data-dir="+profile,
		fmt.Sprintf("--window-size=%d,%d", width, height), "about:blank")
	if err := chrome.Start(); err != nil {
		return fmt.Errorf("start chrome: %w", err)
	}
	// Killed rather than asked to quit: headless Chrome does not exit on its own
	// after a screenshot, which is what made the plain --screenshot flag hang.
	defer chrome.Process.Kill()

	wsURL, err := waitForDevTools()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect to devtools: %w", err)
	}
	defer conn.CloseNow()
	// A screenshot arrives base64-encoded in one frame, so the default 32 KiB
	// read limit rejects every capture bigger than a blank page.
	conn.SetReadLimit(64 << 20)

	c := &client{conn: conn, ctx: ctx}

	if _, err := c.send("Network.enable", nil); err != nil {
		return err
	}
	if _, err := c.send("Page.enable", nil); err != nil {
		return err
	}
	// The session cookie, so the captures show the app rather than the sign-in page.
	if _, err := c.send("Network.setCookie", map[string]any{
		"name": "verdande_session", "value": cookie,
		"domain": "localhost", "path": "/", "httpOnly": true,
	}); err != nil {
		return err
	}

	shots := []shot{
		{name: "today", path: "/", theme: "dark", wait: 2500 * time.Millisecond},
		{name: "today-light", path: "/", theme: "light", wait: 1500 * time.Millisecond},
		{name: "upcoming", path: "/upcoming", theme: "dark", wait: 2000 * time.Millisecond},
		// The integrations page rather than the account one: it shows the feed
		// address and the mail-to-task address, which are the parts people ask
		// what they look like. Account settings are a password form.
		{name: "settings", path: "/indstillinger/integrationer", theme: "dark", wait: 2000 * time.Millisecond},
	}
	if project := os.Getenv("SHOT_PROJECT"); project != "" {
		shots = append(shots,
			shot{name: "board", path: "/projekt/" + project, theme: "dark", wait: 2500 * time.Millisecond})
	}

	for _, s := range shots {
		if err := c.capture(base+s.path, s.theme, s.wait, filepath.Join(out, s.name+".png")); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		fmt.Println("wrote", filepath.Join(out, s.name+".png"))
	}
	return nil
}

func waitForDevTools() (string, error) {
	for i := 0; i < 60; i++ {
		resp, err := http.Get("http://127.0.0.1:9223/json/list")
		if err == nil {
			var targets []struct {
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, t := range targets {
				if t.Type == "page" && t.WS != "" {
					return t.WS, nil
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", fmt.Errorf("devtools never came up")
}

type client struct {
	conn *websocket.Conn
	ctx  context.Context
	id   int
}

// send issues one command and waits for the reply with the matching id. Events
// arrive on the same socket and are skipped — they have no id.
func (c *client) send(method string, params map[string]any) (json.RawMessage, error) {
	c.id++
	id := c.id

	body, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := c.conn.Write(c.ctx, websocket.MessageText, body); err != nil {
		return nil, err
	}

	for {
		_, raw, err := c.conn.Read(c.ctx)
		if err != nil {
			return nil, err
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func (c *client) capture(url, theme string, wait time.Duration, path string) error {
	if _, err := c.send("Page.navigate", map[string]any{"url": url}); err != nil {
		return err
	}
	// A fixed wait rather than waiting on a load event: the interesting part is
	// after hydration and after the first data fetch, neither of which the load
	// event says anything about.
	time.Sleep(wait)

	if theme != "" {
		if _, err := c.send("Runtime.evaluate", map[string]any{
			"expression": fmt.Sprintf("document.documentElement.dataset.theme=%q", theme),
		}); err != nil {
			return err
		}
		time.Sleep(400 * time.Millisecond)
	}

	result, err := c.send("Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		return err
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &shot); err != nil {
		return err
	}
	decoded, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, decoded, 0o644)
}
