package host

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// jackBridge is the taps' bridge on the device, with the tools the firmware
// ships (the way Sonar proved on the Duo):
//
//  1. snd-aloop as card RcplugLoop (index 7), an ALSA loopback, unless one is
//     there already, and a substream nobody uses on either end;
//  2. alsa_out as JACK client "rcplug-taps" with tapChannels inputs, playing
//     into the loopback;
//  3. arecord on the loopback's other end, the frames the host hands out;
//  4. jack_connect / jack_disconnect for each tapped port.
//
// alsa_out and arecord run in their own process group at the host's low
// priority; stopping the bridge ends both, and their JACK connections go
// with alsa_out.
type jackBridge struct {
	mu       sync.Mutex
	play     *exec.Cmd
	rec      *exec.Cmd
	problems int
	pids     []int
}

const (
	tapClient    = "rcplug-taps"
	tapLoopCard  = "RcplugLoop"
	tapLoopIndex = "7"
)

var jackPortName = regexp.MustCompile(`^[A-Za-z0-9_.-]+:[A-Za-z0-9_. -]+$`)

// NewJackBridge returns the device's bridge for NewTaps.
func NewJackBridge() *jackBridge { return &jackBridge{} }

// Ports lists JACK's output ports, without the bridge's own.
func (b *jackBridge) Ports(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "jack_lsp", "-p").Output()
	if err != nil {
		return nil, fmt.Errorf("JACK is not reachable (jack_lsp: %v)", err)
	}
	var ports []string
	cur := ""
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			cur = strings.TrimSpace(line)
			continue
		}
		props := strings.TrimSpace(line)
		if strings.HasPrefix(props, "properties:") && strings.Contains(props, "output") && cur != "" &&
			jackPortName.MatchString(cur) && !strings.HasPrefix(cur, tapClient+":") {
			ports = append(ports, cur)
		}
	}
	return ports, nil
}

func loopCardID() string {
	raw, _ := os.ReadFile("/proc/asound/cards")
	other := ""
	for _, line := range strings.Split(string(raw), "\n") {
		open, close := strings.IndexByte(line, '['), strings.IndexByte(line, ']')
		if open < 0 || close < open {
			continue
		}
		id := strings.TrimSpace(line[open+1 : close])
		if id == tapLoopCard {
			return id
		}
		if other == "" && strings.Contains(line[close:], "Loopback") {
			other = id
		}
	}
	return other
}

func ensureLoop(ctx context.Context) (string, error) {
	if id := loopCardID(); id != "" {
		return id, nil
	}
	log.Printf("audio taps: loading snd-aloop as card %s (index %s)", tapLoopCard, tapLoopIndex)
	out, err := exec.CommandContext(ctx, "modprobe", "snd-aloop", "index="+tapLoopIndex, "id="+tapLoopCard, "pcm_substreams=2").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("loading snd-aloop failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	for i := 0; i < 20; i++ {
		if id := loopCardID(); id != "" {
			return id, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", errors.New("snd-aloop is loaded, but no loopback card appeared")
}

func freeSubstream(card string) (int, error) {
	for n := 0; n < 8; n++ {
		play, err1 := os.ReadFile(fmt.Sprintf("/proc/asound/%s/pcm0p/sub%d/status", card, n))
		capt, err2 := os.ReadFile(fmt.Sprintf("/proc/asound/%s/pcm1c/sub%d/status", card, n))
		if err1 != nil || err2 != nil {
			break
		}
		if strings.TrimSpace(string(play)) == "closed" && strings.TrimSpace(string(capt)) == "closed" {
			return n, nil
		}
	}
	return 0, fmt.Errorf("every substream of the loopback card %s is in use", card)
}

// Start runs alsa_out and arecord.
func (b *jackBridge) Start(ctx context.Context) (io.ReadCloser, error) {
	for _, tool := range []string{"jack_lsp", "jack_connect", "jack_disconnect", "alsa_out", "arecord", "modprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("the audio taps need %s, which is not installed here", tool)
		}
	}
	card, err := ensureLoop(ctx)
	if err != nil {
		return nil, err
	}
	sub, err := freeSubstream(card)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.problems = 0
	play := exec.CommandContext(ctx, "alsa_out", "-j", tapClient, "-d", fmt.Sprintf("hw:%s,0,%d", card, sub),
		"-c", strconv.Itoa(tapChannels), "-r", strconv.Itoa(tapRate))
	setProcessGroup(play)
	playErr, _ := play.StderrPipe()
	play.Stdout = nil
	if err := play.Start(); err != nil {
		return nil, fmt.Errorf("start alsa_out: %w", err)
	}
	go b.watch("alsa_out", playErr)
	if err := waitPorts(ctx, tapClient+":playback_1"); err != nil {
		killGroup(play)
		_ = play.Wait()
		return nil, err
	}
	rec := exec.CommandContext(ctx, "arecord", "-q", "-D", fmt.Sprintf("plughw:%s,1,%d", card, sub),
		"-f", "S16_LE", "-c", strconv.Itoa(tapChannels), "-r", strconv.Itoa(tapRate), "-t", "raw", "--buffer-time=200000")
	setProcessGroup(rec)
	out, err := rec.StdoutPipe()
	if err != nil {
		killGroup(play)
		_ = play.Wait()
		return nil, err
	}
	recErr, _ := rec.StderrPipe()
	if err := rec.Start(); err != nil {
		killGroup(play)
		_ = play.Wait()
		return nil, fmt.Errorf("start arecord: %w", err)
	}
	go b.watch("arecord", recErr)
	b.play, b.rec = play, rec
	b.pids = []int{play.Process.Pid, rec.Process.Pid}
	log.Printf("audio taps: %s with %d channels → hw:%s,0,%d → arecord", tapClient, tapChannels, card, sub)
	return out, nil
}

// watch logs what a bridge program says and counts its dropouts.
func (b *jackBridge) watch(name string, r io.Reader) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		low := strings.ToLower(line)
		if strings.Contains(low, "xrun") || strings.Contains(low, "overrun") || strings.Contains(low, "underrun") {
			b.mu.Lock()
			b.problems++
			n := b.problems
			b.mu.Unlock()
			if n <= 5 || n%100 == 0 {
				log.Printf("audio taps: %s: %s (%d so far)", name, line, n)
			}
			continue
		}
		log.Printf("audio taps: %s: %s", name, line)
	}
}

func waitPorts(ctx context.Context, want string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := exec.CommandContext(ctx, "jack_lsp").Output()
		if err == nil && contains(strings.Split(strings.TrimSpace(string(out)), "\n"), want) {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("alsa_out did not register %s in JACK", want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func jackTool(tool, port string, channel int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dst := fmt.Sprintf("%s:playback_%d", tapClient, channel)
	if out, err := exec.CommandContext(ctx, tool, port, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s %s: %v %s", tool, port, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *jackBridge) Connect(port string, channel int) error {
	return jackTool("jack_connect", port, channel)
}

func (b *jackBridge) Disconnect(port string, channel int) error {
	return jackTool("jack_disconnect", port, channel)
}

// Stop ends both programs.
func (b *jackBridge) Stop() {
	b.mu.Lock()
	play, rec := b.play, b.rec
	b.play, b.rec, b.pids = nil, nil, nil
	b.mu.Unlock()
	for _, c := range []*exec.Cmd{rec, play} {
		if c != nil && c.Process != nil {
			killGroup(c)
			_ = c.Wait()
		}
	}
	log.Printf("audio taps: bridge stopped")
}

func (b *jackBridge) Problems() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.problems
}

// PIDs are the bridge's processes (for the process list).
func (b *jackBridge) PIDs() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.pids...)
}
