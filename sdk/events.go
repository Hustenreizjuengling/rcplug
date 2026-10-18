package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Event is one message on the host's event bus (see docs/plugin-api.md#events).
type Event struct {
	ID     uint64          `json:"id"`
	Time   time.Time       `json:"time"`
	Type   string          `json:"type"`   // e.g. "desk.recording.started"
	Source string          `json:"source"` // "host", "console", or the publishing plugin
	Data   json.RawMessage `json:"data,omitempty"`
}

// ErrStandalone: the plugin runs without the host (no RCPLUG_TOKEN).
var ErrStandalone = errors.New("not running under rcplugd")

var eventClient = &http.Client{Timeout: 10 * time.Second}

// Publish puts an event on the host's bus, with this plugin as its source.
// typ has two to six dot-separated parts of a-z, 0-9, - and _ (start it with
// the plugin's name); data is nil or anything that encodes as a JSON object.
// Without the host it returns ErrStandalone.
func Publish(ctx context.Context, typ string, data interface{}) error {
	token := os.Getenv("RCPLUG_TOKEN")
	if token == "" {
		return ErrStandalone
	}
	body := map[string]interface{}{"type": typ}
	if data != nil {
		body["data"] = data
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(HostURL(), "/")+"/api/events", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set(APIHeader, "1")
	req.Header.Set("X-Rcplug-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := eventClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("publish %s: %s %s", typ, resp.Status, e.Error)
	}
	return nil
}

// Events streams the host's events whose type starts with one of the
// prefixes (all without any) until ctx ends. It reconnects by itself and
// catches up on what it missed, as far as the host still keeps it; the
// channel closes when ctx ends.
func Events(ctx context.Context, prefixes ...string) <-chan Event {
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		var last uint64
		seen := false
		for ctx.Err() == nil {
			err := streamEvents(ctx, prefixes, &last, &seen, out)
			if ctx.Err() != nil {
				return
			}
			_ = err
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return out
}

func streamEvents(ctx context.Context, prefixes []string, last *uint64, seen *bool, out chan<- Event) error {
	u := strings.TrimRight(HostURL(), "/") + "/api/events/stream"
	if len(prefixes) > 0 {
		u += "?type=" + url.QueryEscape(strings.Join(prefixes, ","))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if *seen {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(*last, 10))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) != nil {
			continue
		}
		*last, *seen = e.ID, true
		select {
		case out <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sc.Err()
}
