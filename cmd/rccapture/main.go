// Command rccapture records the desk's state tree as a sanitized full-sync
// frame, the test fixture format of this repository (see
// docs/adding-a-model.md). Run it on the device:
//
//	rccapture --out /tmp/pro2-fw1.2.3.frame
//
// It joins rc_audio_mixer's control port read-only, with the same GUI guard
// as the console, waits for the first full sync, sanitizes it (every string
// except the firmware version becomes "<redacted>", binary blobs are zeroed)
// and exits. It never writes to the desk.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

var version = "dev"

func main() {
	addr := flag.String("mixer", "127.0.0.1:2345", "rc_audio_mixer control port")
	out := flag.String("out", "", "output file (required)")
	raw := flag.Bool("raw", false, "write the unsanitized tree (contains Wi-Fi keys, names, …; never publish it)")
	timeout := flag.Duration("timeout", 30*time.Second, "give up after this long")
	guard := flag.Bool("guard", true, "protect the device GUI's connection")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	_ = sdk.LowPriority()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tree, err := capture(ctx, *addr, *guard)
	if err != nil {
		log.Fatal(err)
	}
	if !*raw {
		tree = rcproto.Sanitize(tree)
	}
	frame := rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: tree}).Encode())
	if err := sdk.WriteFileAtomic(*out, frame, 0o644); err != nil {
		log.Fatal(err)
	}
	nodes, props := tree.Count()
	fw, _ := findProp(tree, "SYSTEM", "systemFirmwareVersion")
	p := rcproto.ProfileOf(rcproto.DetectModel(tree))
	fmt.Printf("wrote %s: %d bytes, %d nodes, %d properties\nmodel: %s, firmware %s\n",
		*out, len(frame), nodes, props, p.Name, fw.S)
	if *raw {
		fmt.Println("WARNING: unsanitized capture – keep it private")
	}
}

// capture connects read-only and returns a copy of the first complete tree.
func capture(ctx context.Context, addr string, guard bool) (*rcproto.Node, error) {
	m := desk.NewMirror()
	c := desk.NewClient(desk.Config{Addr: addr, Guard: guard, BootGrace: time.Nanosecond, ReadOnly: true,
		Logf: func(string, ...interface{}) {}}, m)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { c.Run(runCtx); close(done) }()
	defer func() { stop(); <-done }()

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var tree *rcproto.Node
		m.View(func(s desk.Snapshot) {
			if s.Root != nil && !s.Stale {
				tree = s.Root.Clone()
			}
		})
		if tree != nil {
			return tree, nil
		}
		select {
		case <-ctx.Done():
			st := c.Status()
			return nil, fmt.Errorf("no full sync from %s (%s %s): %w", addr, st.State, st.Error, ctx.Err())
		case <-tick.C:
		}
	}
}

func findProp(root *rcproto.Node, nodeType, name string) (rcproto.Value, bool) {
	for _, c := range root.Children {
		if c.Type == nodeType {
			return c.Get(name)
		}
	}
	return rcproto.Value{}, false
}
