package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Client talks to the console plugin.
type Client struct {
	// Base is the console's base URL, ending in "/" (e.g.
	// http://127.0.0.1:8080/plugins/console/).
	Base string
	// PIN is sent with writes if set.
	PIN  string
	HTTP *http.Client
}

// New returns a client for the console behind the host at hostURL ("" =
// sdk.HostURL()).
func New(hostURL string) *Client {
	if hostURL == "" {
		hostURL = sdk.HostURL()
	}
	return &Client{Base: strings.TrimRight(hostURL, "/") + "/plugins/console/", HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Error is an error answered by the console ({"error","code"}).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("console: %s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

func (c *Client) do(ctx context.Context, method, path string, body, out interface{}) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(sdk.APIHeader, "1")
		if c.PIN != "" {
			req.Header.Set("X-Rcplug-Pin", c.PIN)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Error, Code string }
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return &Error{Status: resp.StatusCode, Code: e.Code, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Status returns the console and connection status.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.do(ctx, http.MethodGet, "api/status", nil, &s)
}

// Summary returns the compact desk summary.
func (c *Client) Summary(ctx context.Context) (*Summary, error) {
	var s Summary
	return &s, c.do(ctx, http.MethodGet, "api/summary", nil, &s)
}

// Tree returns the full (redacted) state tree document as raw JSON:
// {"epoch","seq","stale","meta":{…},"root":{"t","p","y","c"}}.
func (c *Client) Tree(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.do(ctx, http.MethodGet, "api/tree", nil, &raw)
}

// Set writes one property of the node at path. epoch comes from Status or
// Tree and protects against writing through an outdated path.
func (c *Client) Set(ctx context.Context, epoch uint64, path []int, name string, value interface{}) error {
	return c.do(ctx, http.MethodPost, "api/set", map[string]interface{}{
		"epoch": epoch, "path": path, "name": name, "value": value,
	}, nil)
}

// Events streams server-sent events until ctx ends or the stream breaks
// (callers reconnect as they see fit). fn runs on the reading goroutine.
func (c *Client) Events(ctx context.Context, fn func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"api/events", nil)
	if err != nil {
		return err
	}
	hc := *c.HTTP
	hc.Timeout = 0 // streaming
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &Error{Status: resp.StatusCode, Code: "stream", Message: resp.Status}
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	var ev Event
	var data bytes.Buffer
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Name != "" || data.Len() > 0 {
				if ev.Name == "" {
					ev.Name = "message"
				}
				ev.Data = append([]byte(nil), data.Bytes()...)
				fn(ev)
			}
			ev, data = Event{}, bytes.Buffer{}
		case strings.HasPrefix(line, "event:"):
			ev.Name = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line[5:], " "))
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}
