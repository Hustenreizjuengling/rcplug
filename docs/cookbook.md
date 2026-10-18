# Plugin cookbook

Short recipes for things plugins often do. The Go programs here are complete: each
was compiled and run against the simulated desk. For the concepts, read
[plugin-guide.md](plugin-guide.md) first; every detail of the interface is in
[plugin-api.md](plugin-api.md), and the console's HTTP API in
[console-api.md](console-api.md).

All Go recipes import the SDK as

```go
import (
	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)
```

and find the console through the host (`capi.New("")` uses `RCPLUG_HOST_URL`). To run
one outside the host, point it at a running one:
`RCPLUG_HOST_URL=http://127.0.0.1:8080 go run .`

**The desk**
[Find a node](#find-a-node) ·
[Follow every change](#follow-every-change-of-the-desk) ·
[Change something](#change-something-mute-a-channel) ·
[Drop a marker](#drop-a-marker) ·
[Start and stop recording](#start-and-stop-recording) ·
[React to a SMART pad](#react-to-a-smart-pad) ·
[Call a URL when recording starts](#call-a-url-when-recording-starts-or-stops)

**The page**
[Live data](#push-live-data-to-the-page) ·
[Open from the console's strips](#open-your-plugin-from-the-consoles-channel-strips) ·
[Language](#match-the-dashboards-language) ·
[Inside the dashboard](#look-right-inside-the-dashboard) ·
[State changes](#protect-state-changes)

**State, processes, system**
[Keep state](#keep-state-across-restarts-and-updates) ·
[Operator settings and secrets](#settings-only-the-operator-may-change) ·
[Run a helper program](#run-a-helper-program) ·
[Use the desk's audio](#use-the-desks-audio) ·
[Another plugin](#talk-to-another-plugin) ·
[The host's API](#use-the-hosts-api) ·
[Diagnostics](#add-your-data-to-the-diagnostics-bundle) ·
[No page](#a-plugin-without-a-page)

**Other languages and tests**
[Shell](#shell-read-the-desk-with-curl) ·
[Python](#python) ·
[Tests without the host](#test-against-the-simulated-desk-without-the-host)

## The desk

### Find a node

The desk's state is a tree. Nodes of one kind (`CHANNEL`, `RECORDER`, `PADBUTTON`,
…) sit next to each other, and `meta.layout.runs` in `GET api/tree` says where each
family starts. Never hard-code a path: firmware versions may reorder the tree. A
path is valid for one **epoch**; writes carry it, and the console refuses them with
`stale_epoch` if the tree changed meanwhile.

```go
// layout is the part of GET api/tree that tells where each family of nodes is.
type layout struct {
	Epoch uint64 `json:"epoch"`
	Meta  struct {
		Layout struct {
			Runs map[string]struct {
				Parent []int `json:"parent"`
				First  int   `json:"first"`
				Count  int   `json:"count"`
			} `json:"runs"`
		} `json:"layout"`
	} `json:"meta"`
}
```

```go
// pathOf returns the path of the i-th node of a family (CHANNEL, RECORDER, …)
// and the epoch it is valid for.
func pathOf(ctx context.Context, c *capi.Client, family string, i int) ([]int, uint64, error) {
	raw, err := c.Tree(ctx)
	if err != nil {
		return nil, 0, err
	}
	var l layout
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, 0, err
	}
	run, ok := l.Meta.Layout.Runs[family]
	if !ok || i >= run.Count {
		return nil, 0, errors.New("no " + family + " node")
	}
	return append(slices.Clone(run.Parent), run.First+i), l.Epoch, nil
}
```

(`slices.Clone` needs Go 1.21.) The node families of the Duo, with their properties,
are easiest to explore in the console's **Explorer** tab.

### Follow every change of the desk

For things that change often or must not be missed, follow the console's event stream
instead of polling. It starts with `hello`, then sends `changes` batches (at most
every 60 ms, latest value per property) and `resync` when changes were dropped, after
which you read the tree again. This program logs every mute and unmute of the first
channel; it uses `pathOf` from [Find a node](#find-a-node):

```go
// follow logs every mute and unmute of the first channel, from the console's
// event stream: no polling, and no change is missed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

func main() {
	ctx, stop := sdk.SignalContext()
	defer stop()
	c := capi.New("")
	for {
		err := follow(ctx, c)
		if ctx.Err() != nil {
			return // stopped
		}
		log.Printf("event stream ended (%v), reconnecting", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func follow(ctx context.Context, c *capi.Client) error {
	var path []int
	var epoch uint64
	return c.Events(ctx, func(e capi.Event) {
		switch e.Name {
		case "hello", "resync": // (re)connected, or changes were dropped: read the layout again
			var err error
			if path, epoch, err = pathOf(ctx, c, "CHANNEL", 0); err != nil {
				log.Print(err)
			}
		case "changes":
			var batch struct {
				Epoch uint64              `json:"epoch"`
				Ch    [][]json.RawMessage `json:"ch"` // [path, property, value, type]
			}
			if json.Unmarshal(e.Data, &batch) != nil || batch.Epoch != epoch {
				return
			}
			for _, ch := range batch.Ch {
				var p []int
				var name string
				var muted bool
				if len(ch) == 4 && json.Unmarshal(ch[0], &p) == nil && json.Unmarshal(ch[1], &name) == nil &&
					name == "channelOutputMute" && slices.Equal(p, path) && json.Unmarshal(ch[2], &muted) == nil {
					log.Printf("channel 1 muted: %v", muted)
				}
			}
		}
	})
}
```

### Change something: mute a channel

Writes go through the console, which checks them against the control level the user
chose (*Read only* after installation) and the PIN. Say in your plugin's
description what it changes; the user decides by setting the level. Pass the PIN in
through the operator's settings, never through a web page.

```go
// mute mutes the first channel strip and drops a marker into the recording.
// Writing needs the control level "Control" in the console (it is read-only
// after installation), and the PIN if the user set one.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"slices"
	"time"

	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := capi.New("")
	c.PIN = os.Getenv("CONSOLE_PIN") // from config/<name>.json, never from a web page

	err := set(ctx, c, "CHANNEL", 0, "channelOutputMute", true)
	if err == nil {
		err = set(ctx, c, "RECORDER", 0, "requestDropMarker", time.Now().UnixMilli())
	}
	var e *capi.Error
	switch {
	case errors.As(err, &e) && e.Code == "control_off":
		log.Fatal("control is off: switch the console from Read only to Control")
	case errors.As(err, &e) && e.Code == "pin_required":
		log.Fatal("the console has a PIN: put it into config/<name>.json as CONSOLE_PIN")
	case err != nil:
		log.Fatal(err)
	}
	log.Print("channel 1 muted, marker dropped")
}

// set writes a property of the i-th node of a family. If the tree changed
// between reading the path and writing (stale_epoch), it reads it again once.
func set(ctx context.Context, c *capi.Client, family string, i int, name string, value interface{}) error {
	for attempt := 0; ; attempt++ {
		path, epoch, err := pathOf(ctx, c, family, i)
		if err != nil {
			return err
		}
		err = c.Set(ctx, epoch, path, name, value)
		var e *capi.Error
		if attempt == 0 && errors.As(err, &e) && e.Code == "stale_epoch" {
			continue
		}
		return err
	}
}
```

The error codes are listed in [console-api.md](console-api.md#error-codes).

### Drop a marker

A marker is the current time in milliseconds, written to `requestDropMarker` of the
`RECORDER` node, as in the program above:

```go
err := set(ctx, c, "RECORDER", 0, "requestDropMarker", time.Now().UnixMilli())
```

### Start and stop recording

The recorder has its own request, `POST api/record` with `value` 0 (stop), 1 (pause)
or 2 (record). From a shell on the device:

```sh
B="$RCPLUG_HOST_URL/plugins/console"
E=$(curl -fsS "$B/api/status" | sed -n 's/.*"epoch":\([0-9]*\).*/\1/p')
curl -fsS -X POST "$B/api/record" -H 'X-Rcplug: 1' -d "{\"epoch\":$E,\"value\":2}"
```

In Go, send the same request with `net/http`: a POST with the header `X-Rcplug: 1`
(and `X-Rcplug-Pin` if a PIN is set).

### React to a SMART pad

`Summary` lists the pads with their names and whether they are playing. This plugin
runs a command when a given pad starts:

```go
// pad runs a command whenever the SMART pad with a given name starts playing,
// e.g. to switch a light or start a script. Configure it in config/<name>.json:
//
//	{"env": {"PAD_NAME": "Applause", "PAD_COMMAND": "echo applause!"}}
package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

func main() {
	name, command := os.Getenv("PAD_NAME"), os.Getenv("PAD_COMMAND")
	if name == "" || command == "" {
		log.Fatal("set PAD_NAME and PAD_COMMAND in config/<name>.json")
	}
	ctx, stop := sdk.SignalContext()
	defer stop()
	watchPad(ctx, capi.New(""), name, func() { run(ctx, command) })
}

// watchPad calls pressed when the pad named name becomes active.
func watchPad(ctx context.Context, c *capi.Client, name string, pressed func()) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	was := false
	for {
		if s, err := c.Summary(ctx); err == nil {
			now := false
			for _, p := range s.Pads {
				now = now || (p.Name == name && p.Active)
			}
			if now && !was {
				pressed()
			}
			was = now
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// run starts the command and waits for it. Its output lands in the plugin's
// log; it inherits the lowest CPU priority and is stopped with the plugin.
func run(ctx context.Context, command string) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("command failed: %v", err)
	}
}
```

Five reads a second cost little: the summary comes from the console's mirror, not
from the desk.

### Call a URL when recording starts or stops

An on-air lamp on a smart plug, a status in a home automation system, a message to a
chat: most of these take an HTTP request. The URLs often carry tokens, so they belong
in the operator's settings and never in a log:

```go
// webhook calls a URL when the desk starts recording and another one when it
// stops, e.g. a smart plug that switches an on-air lamp. The URLs come from
// the operator's config/<name>.json, so they never show up on a web page:
//
//	{"env": {"ONAIR_ON_URL": "http://192.168.1.60/relay/0?turn=on",
//	         "ONAIR_OFF_URL": "http://192.168.1.60/relay/0?turn=off"}}
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	capi "github.com/Hustenreizjuengling/rcplug/sdk/console"
)

func main() {
	on, off := os.Getenv("ONAIR_ON_URL"), os.Getenv("ONAIR_OFF_URL")
	if on == "" && off == "" {
		log.Fatal("set ONAIR_ON_URL and/or ONAIR_OFF_URL in config/<name>.json")
	}
	ctx, stop := sdk.SignalContext()
	defer stop()
	c := capi.New("")
	hc := &http.Client{Timeout: 5 * time.Second}

	t := time.NewTicker(time.Second)
	defer t.Stop()
	known, recording := false, false
	for {
		// A stale summary is the last known state, not news: wait for the desk.
		if s, err := c.Summary(ctx); err == nil && !s.Stale {
			now := s.Recorder.State == 2
			if !known || now != recording { // the first read sets the lamp, too
				target := off
				if now {
					target = on
				}
				if target == "" || call(ctx, hc, target) { // else try again next time
					known, recording = true, now
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// call requests the URL and reports whether it answered with success. It
// logs only the host: URLs often carry tokens.
func call(ctx context.Context, hc *http.Client, target string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		log.Print("invalid webhook URL")
		return true // retrying will not help
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // without the URL
		}
		log.Printf("webhook %s: %v", req.URL.Host, err)
		return false
	}
	resp.Body.Close()
	log.Printf("webhook %s: %s", req.URL.Host, resp.Status)
	return resp.StatusCode < 300
}
```

## The page

### Push live data to the page

Serve Server-Sent Events and read them with `EventSource`; the host's proxy flushes
them at once. The complete pattern, with one goroutine reading the desk and any
number of browsers, is in [the guide, step 6](plugin-guide.md#6-push-changes-to-the-browser).
Send something at least every 30 s on idle streams, so that proxies and browsers keep
them open, and stop writing when the request's context ends.

### Open your plugin from the console's channel strips

Declare an action in `plugin.json`; the console shows it as a button on every strip
and in the channel dialog:

```json
"actions": [{
  "id": "channel",
  "context": "channel",
  "title": "Analyse",
  "icon": "◉",
  "path": "?strip={strip}&source={source}&label={label}"
}]
```

A click opens your page with the placeholders filled in:

```js
const q = new URLSearchParams(location.search);
const strip = q.get('strip');   // physical1, virtual2, …
const source = q.get('source'); // combo1, usb2, … (empty if none)
const label = q.get('label');   // the source's display name
```

### Match the dashboard's language

```js
let lang = 'en';
try { lang = localStorage.getItem('rcplug-lang') || (navigator.language.startsWith('de') ? 'de' : 'en'); } catch (e) { /* ignore */ }
```

The dashboard sets `rcplug-lang` to `de` or `en`. Leave the language switch to the
dashboard while embedded. Title and description in the dashboard come from
`translations` in `plugin.json`.

### Look right inside the dashboard

The dashboard shows your page in an iframe below its own navigation. Hide your own
header there, and use the dashboard's colours (dark background `#0d1015`, panels
`#151a21`, lines `#2a333f`, text `#e7edf3`, accent `#19c37d`) so it does not stand
out:

```js
if (window.self !== window.top) document.body.classList.add('embedded');
```

```css
body.embedded header { display: none; }
```

For a page that should fill a second screen, add a full-screen button
(`document.documentElement.requestFullscreen()`); the dashboard allows it.

### Protect state changes

Every POST, PUT, PATCH and DELETE must carry `X-Rcplug: 1`, and your server must refuse
those without it (403). `sdk.Serve` does that. With your own server, wrap the handler:

```go
srv := &http.Server{Addr: addr, Handler: sdk.CSRF(mux)}
```

and in the page:

```js
fetch('api/thing', { method: 'POST', headers: { 'X-Rcplug': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(data) });
```

Never change anything on a GET: links and image tags from other sites can trigger
GETs.

## State, processes, system

### Keep state across restarts and updates

The plugin directory is replaced by every update; the data directory
(`RCPLUG_DATA_DIR`) is kept. `sdk.StatePath` gives a path there (and moves a file an
older version left in the plugin directory), `sdk.SaveJSON` writes atomically:

```go
path := sdk.StatePath("state.json")
var st State
if err := sdk.LoadJSON(path, &st); err != nil { // a missing file is no error
	log.Printf("state: %v", err)
}
st.Count++
if err := sdk.SaveJSON(path, st); err != nil { // temporary file, sync, rename
	log.Printf("saving state: %v", err)
}
```

The device can lose power at any moment: never write a state file in place.

### Settings only the operator may change

Things like a PIN, a webhook URL or a token must not be settable from the web page,
where anyone on the network could read or change them. The operator puts them into
`config/<name>.json` in the install directory (over SSH); the host passes them on and
keeps the file across updates:

```json
{"env": {"CONSOLE_PIN": "4711", "ONAIR_ON_URL": "http://192.168.1.60/relay/0?turn=on"},
 "args": ["--interval", "2s"]}
```

`env` adds environment variables, `args` adds command-line arguments after those of
`exec`. Read them with `os.Getenv` and the `flag` package. The host never logs `env`
values, and masks the values of arguments whose names start with `pin`, `pass`,
`token`, `secret` or `key`. Do not print them yourself, and leave them out of
[diagnostics](#add-your-data-to-the-diagnostics-bundle).

### Run a helper program

`exec.CommandContext` with the plugin's context, as `run` in
[React to a SMART pad](#react-to-a-smart-pad) does. Helpers inherit the lowest CPU
priority and belong to the plugin's process group: the host stops them together with
the plugin and kills what is left. Their output goes to the plugin's log if you pass
`os.Stdout` and `os.Stderr`. Never build a shell command from text a web page sent;
pass arguments separately (`exec.Command("arecord", "-D", device)`).

### Use the desk's audio

On the RØDECaster Duo the whole desk is a JACK graph: `jackd` holds the only capture
device, so `arecord` cannot open the card, and every input and output is a JACK port.
Sonar bridges two ports with tools the firmware ships
([`sonar/jack.go`](https://github.com/Hustenreizjuengling/rcplug-plugins/blob/main/sonar/jack.go)):

```sh
modprobe snd-aloop index=7 id=RcplugLoop pcm_substreams=2   # an ALSA loopback card
jack_lsp -p                                                 # the ports there are
alsa_out -j my-plugin -d hw:RcplugLoop,0,0 -c 2 -r 48000 &  # a JACK client playing into it
jack_connect system:capture_1 my-plugin:playback_1          # pick the ports you need
jack_connect system:capture_2 my-plugin:playback_2
arecord -D plughw:RcplugLoop,1,0 -f S16_LE -c 2 -r 48000 -t raw   # the other end
```

This adds a client to RØDE's audio graph. Make it opt-in, say so on your page, set
`needsAudio` in `plugin.json`, and stop the bridge when you no longer need it
(ending `alsa_out` removes its connections). It has been tested with the firmware's
JACK tools and JACK2, not yet on a running Duo.

### Talk to another plugin

Other plugins are reachable through the host, like from a browser:

```go
resp, err := http.Get(sdk.HostURL() + "/plugins/reclog/api/log")
```

POSTs need the header:

```go
req, err := http.NewRequestWithContext(ctx, http.MethodPost, sdk.HostURL()+"/plugins/reclog/api/clear", nil)
if err != nil {
	return err
}
req.Header.Set(sdk.APIHeader, "1")
resp, err := http.DefaultClient.Do(req)
```

The other plugin may be stopped or not installed: expect 404 and 502.

### Use the host's API

The dashboard's API is open to plugins too, for example to restart another plugin:

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodPost, sdk.HostURL()+"/api/plugins/sonar/restart", nil)
req.Header.Set(sdk.APIHeader, "1")
```

The endpoints are listed in [architecture.md](architecture.md#host-api).

### Add your data to the diagnostics bundle

When a user downloads diagnostics, the host asks every running plugin with a page
for `GET /api/diagnostics` and adds the answer to the zip. Report what helps to find a
problem, never secrets:

```go
mux.HandleFunc("GET /api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"version":   version,
		"settings":  a.settings(), // without tokens, PINs, URLs with credentials
		"browsers":  a.subscribers(),
		"lastError": a.lastError(),
	})
})
```

JSON becomes `diagnostics.json`, other text `diagnostics.txt`, a zip is merged. Answer
within 5 seconds and below 20 MB.

### A plugin without a page

Leave out `httpProxy`. The plugin gets no port (`RCPLUG_PORT` is `0`), has no entry
in the navigation, and shows up on the **Plugins** page with its log. Background
workers like [the webhook](#call-a-url-when-recording-starts-or-stops) are of this
kind.

## Other languages and tests

### Shell: read the desk with curl

The firmware has `curl` and Python 3, but no `jq`:

```sh
state=$(curl -fsS "$RCPLUG_HOST_URL/plugins/console/api/summary" |
	python3 -c 'import json, sys; print(json.load(sys.stdin)["recorder"]["state"])')
[ "$state" = 2 ] && echo "recording"
```

A complete shell plugin is [`examples/hello-sh`](../examples/hello-sh). Remember the
`trap` for SIGINT: a background `sleep` in a non-interactive shell ignores it.

### Python

A complete plugin with a page, an API, the `X-Rcplug` check and a clean stop is in
[the guide, section 12](plugin-guide.md#12-other-languages). Print with `flush=True`,
or the log lags behind.

### Test against the simulated desk without the host

The console runs on its own with a built-in simulated Duo:

```bash
go run github.com/Hustenreizjuengling/rcplug/cmd/console --sim --addr 127.0.0.1:8082
```

Point the client at it instead of at a host:

```go
c := capi.New("")
c.Base = "http://127.0.0.1:8082/" // the console itself, not /plugins/console/ behind a host
```

For unit tests, fake the console with `httptest` as
[`examples/onair/main_test.go`](../examples/onair/main_test.go) does.
