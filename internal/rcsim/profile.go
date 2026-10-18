// Package rcsim simulates rc_audio_mixer's TCP control port for offline
// development and tests. It serves the real (sanitized) Duo full-sync tree,
// filled with plausible values, streams meter activity, and reacts to client
// writes the way the desk does (no echo to the writer, broadcast to others).
package rcsim

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// DuoFrame is a sanitized full-sync TCP frame captured from a RØDECaster Duo
// on firmware 1.7.4 (MIT, from the rodecaster-protocol crate; see fixture/NOTICE).
//
//go:embed fixture/duo-fw1.7.4-full-sync.frame
var DuoFrame []byte

// DuoTree parses the embedded fixture into a fresh tree.
func DuoTree() (*rcproto.Node, error) { return TreeFromFrame(DuoFrame) }

// TreeFromFrame parses a full-sync frame (as written by rccapture) into a
// fresh tree.
func TreeFromFrame(frame []byte) (*rcproto.Node, error) {
	body, _, err := rcproto.ParseFrame(frame)
	if err != nil {
		return nil, err
	}
	c, err := rcproto.DecodeChange(body)
	if err != nil {
		return nil, err
	}
	if c.Type != rcproto.FullSync {
		return nil, fmt.Errorf("frame is %s, not a full sync", c.Type)
	}
	return c.Tree, nil
}

// set overwrites an existing property, keeping its wire type. Unknown names
// are ignored so the profile can never invent properties.
func set(n *rcproto.Node, name string, v interface{}) {
	if n == nil {
		return
	}
	old, ok := n.Get(name)
	if !ok {
		return
	}
	var nv rcproto.Value
	switch x := v.(type) {
	case bool:
		if !old.IsBool() {
			return
		}
		nv = rcproto.Bool(x)
	case int:
		if !old.IsNumber() {
			return
		}
		nv = old
		switch old.Kind {
		case rcproto.KindDouble:
			nv.F = float64(x)
		default:
			nv.I = int64(x)
		}
	case float64:
		if !old.IsNumber() {
			return
		}
		nv = old
		switch old.Kind {
		case rcproto.KindInt, rcproto.KindInt64:
			nv.I = int64(x)
		default:
			nv.F = x
		}
	case string:
		if old.Kind != rcproto.KindString {
			return
		}
		nv = rcproto.String(x)
	default:
		return
	}
	n.Set(name, nv)
}

// MakeRealistic replaces the sanitizer's neutral values with a plausible
// "evening show" so every view of the console has something to show.
func MakeRealistic(root *rcproto.Node) {
	clearRedacted(root)
	l := rcproto.DiscoverLayout(root)
	each := func(family string, fn func(i int, n *rcproto.Node)) {
		r := l.Runs[family]
		for i := 0; i < r.Count; i++ {
			fn(i, root.At(r.Path(i)))
		}
	}
	single := func(family string) *rcproto.Node { return l.Node(root, family, 0) }

	set(single("PHYSICALINTERFACE"), "onOffSw", 1)
	set(single("PHYSICALINTERFACE"), "hwScreenBrightness", 80)
	pots := []int{64, 80, 72, 96}
	each("POT", func(i int, n *rcproto.Node) {
		set(n, "potMin", 0)
		set(n, "potMax", 127)
		set(n, "potLevel", pots[i%len(pots)])
	})
	faders := []int{90, 84, 70, 0, 90, 62, 76, 0, 0}
	each("FADER", func(i int, n *rcproto.Node) {
		set(n, "faderMin", 0)
		set(n, "faderMax", 127)
		set(n, "faderLevel", faders[i%len(faders)])
	})
	sources := []int{0, 1, 7, 10, 11, 9, 12, 13, -1, -1}
	stereo := map[int]bool{7: true, 9: true, 10: true, 11: true, 12: true, 13: true}
	each("METER", func(i int, n *rcproto.Node) {
		if i < len(faders) {
			set(n, "faderLevel", faders[i])
			set(n, "meterStereo", stereo[sources[i]])
		} else {
			set(n, "meterStereo", true)
		}
	})
	set(single("DUCKER"), "duckerDepth", -9.0) // dB, as on a Duo with firmware 1.7.6
	gui := single("GUI")
	set(gui, "lang", "de")
	set(gui, "broadcastMeters", true)
	set(gui, "activeButtonsBrightness", 255) // 0..255, as on a Duo with firmware 1.7.6
	set(gui, "inactiveButtonsBrightness", 8)
	set(gui, "screenBrightness", 250)
	set(gui, "screenDimAfterSeconds", true)
	colours := []string{"FF19C37D", "FF2F81F7", "FFF0A030", "FFD03CA0"}
	each("HEADPHONE", func(i int, n *rcproto.Node) { set(n, "headphoneColour", colours[i%len(colours)]) })
	out := single("OUTPUT")
	set(out, "outputMonLevel", 0.55)
	set(out, "outputMonAutoMute", true)
	set(out, "outputBTLevel", 0.7)
	set(out, "headphone1Level", 80)
	set(out, "recordingMultitrackMode", 1)
	m := single("MASTERCHANNEL")
	set(m, "masterCompellorOn", true)
	set(m, "masterCompellorThreshold", 0.6)
	set(m, "masterCompellorAttack", 0.3)
	set(m, "masterCompellorRelease", 0.4)
	set(m, "masterCompellorGain", 0.5)

	sys := single("SYSTEM")
	set(sys, "systemFirmwareVersion", "1.7.4")
	set(sys, "systemSerialNumber", "SIM-0000001")
	set(sys, "systemName", "RØDECaster Duo (Simulator)")
	set(sys, "usb1Connected", true)
	set(sys, "systemDateTime24h", true)
	set(sys, "systemDateTimezone", 1)
	set(sys, "systemHapticSetting", 1)
	vols := []struct {
		name       string
		cap, free  int
		in, mount  bool
		recDest    bool
		stateLabel string
	}{
		{"microSD", 61035, 40210, true, true, true, "mounted"},
		{"USB", 0, 0, false, false, false, "empty"},
		{"Intern", 7629, 6900, true, true, false, "mounted"},
	}
	each("STORAGEVOLUME", func(i int, n *rcproto.Node) {
		if i >= len(vols) {
			return
		}
		v := vols[i]
		set(n, "storageVolumeName", v.name)
		set(n, "storageVolumeCapacity", v.cap)
		set(n, "storageVolumeFree", v.free)
		set(n, "storageVolumeInserted", v.in)
		set(n, "storageVolumeMounted", v.mount)
		set(n, "storageVolumeFormatted", v.in)
		set(n, "storageVolumeRecDestination", v.recDest)
		set(n, "storageVolumeState", v.stateLabel)
	})
	net := single("NETWORK")
	set(net, "ipAddress", "192.168.1.50")
	set(net, "subnetMask", "255.255.255.0")
	set(net, "gateway", "192.168.1.1")
	set(net, "primaryDns", "192.168.1.1")
	set(net, "wiredConnected", true)
	set(net, "wifiSSID", "Heimnetz")
	set(net, "wifiPSK", "geheim-1234") // must never reach the web UI
	set(net, "btConnectedAddress", "")
	ssids := []string{"Heimnetz", "FRITZ!Box 7590", "Nachbar-WLAN"}
	each("NETWORK/WIFISCANRESULT", func(i int, n *rcproto.Node) {
		if i < len(ssids) {
			set(n, "wifiScanResultSSID", ssids[i])
		}
	})
	audio := single("AUDIO")
	set(audio, "audioSampleRate", 48000.0)
	set(audio, "audioInputChannels", 26)
	set(audio, "audioOutputChannels", 26)
	set(audio, "audioBufferSize", 64)
	set(audio, "audioInputLatency", 96)
	set(audio, "audioOuputLatency", 96)

	each("CHANNEL", func(i int, n *rcproto.Node) {
		src := -1
		if i < len(sources) {
			src = sources[i]
		}
		set(n, "channelInputSource", src)
		set(n, "channelListenSource", src)
		set(n, "channelOutputMute", i == 3)
		set(n, "channelPan", 0.5)
		set(n, "channelPanL", 0.5)
		set(n, "channelPanR", 0.5)
		set(n, "channelDepth", 0.5)
		set(n, "channelSparkle", 0.5)
		set(n, "channelPunch", 0.5)
		mic := src >= 0 && src <= 6
		set(n, "channelAdvancedProcessing", mic)
		set(n, "aphexOn", mic)
		set(n, "aphexAEMix", 0.4)
		set(n, "aphexAETune", 0.5)
		set(n, "aphexBBDrive", 0.3)
		set(n, "aphexBBTune", 0.5)
		set(n, "compressorOn", mic)
		set(n, "compressorThreshold", 0.55)
		set(n, "compressorRatio", 0.35)
		set(n, "compressorAttack", 0.2)
		set(n, "compressorRelease", 0.4)
		set(n, "compressorGain", 0.5)
		set(n, "noiseGateOn", mic)
		set(n, "noiseGateThreshold", 0.3)
		set(n, "noiseGateRange", 0.6)
		set(n, "noiseGateAttack", 0.1)
		set(n, "noiseGateHold", 0.2)
		set(n, "noiseGateRelease", 0.3)
		set(n, "noiseGateHysteresis", 0.2)
		set(n, "deesserOn", mic)
		set(n, "deesserThreshold", 0.5)
		set(n, "deesserFrequency", 0.6)
		set(n, "deesserRatio", 0.4)
		set(n, "deesserAttack", 0.1)
		set(n, "deesserRelease", 0.3)
		set(n, "deesserGain", 0.5)
		set(n, "hpfOn", mic)
		set(n, "hpfFrequency", 0.25)
		set(n, "hpfSlope", 0.5)
		set(n, "eqOn", mic)
		set(n, "eqLowOn", true)
		set(n, "eqMidOn", true)
		set(n, "eqHighOn", true)
		set(n, "eqLowGain", 0.55)
		set(n, "eqMidGain", 0.5)
		set(n, "eqHighGain", 0.6)
		set(n, "eqLowShelf", 0.3)
		set(n, "eqMidBell", 0.5)
		set(n, "eqHighBell", 0.7)
		set(n, "eqLowQ", 2.0/99) // as on a Duo with firmware 1.7.6
		set(n, "eqMidQ", 4.0/99)
		set(n, "eqHighQ", 2.0/99)
	})
	each("EFFECTS_PARAMETERS", func(i int, n *rcproto.Node) {
		set(n, "effectsIdx", i)
		set(n, "reverbOn", i == 0)
		set(n, "reverbMix", 0.3)
		set(n, "reverbModel", 1)
		set(n, "reverbLowCut", 0.2)
		set(n, "reverbHighCut", 0.8)
		set(n, "echoMix", 0.25)
		set(n, "echoDelay", 0.4)
		set(n, "echoDecay", 0.4)
		set(n, "echoLowCut", 0.1)
		set(n, "echoHighCut", 0.9)
		set(n, "pitchShiftSemitones", 0.5)
		set(n, "distortionIntensity", 0.3)
		set(n, "robotLevel", 0.5)
		set(n, "robotMix", 0.5)
	})

	// Routing: every source feeds every bus at its fader level, except
	// the usual mix-minus holes (a source never returns to its own bus).
	level := map[int]float64{}
	for i, s := range sources {
		if s >= 0 && i < len(faders) {
			level[s] = float64(faders[i]) / 127
		}
	}
	selfBus := map[int]int{7: 7, 8: 8, 9: 9, 10: 6, 16: 10, 17: 11, 18: 12}
	for s := 0; s < l.SourceCount; s++ {
		for o := 0; o < rcproto.MixPerSource; o++ {
			n := root.At(l.MixPath(s, o))
			lv := level[s]
			set(n, "mixLevelWithAnchor", fmt.Sprintf("%.6f|%.6f", lv, lv))
			set(n, "mixLink", true)
			set(n, "mixMute", false)
			if b, ok := selfBus[s]; ok && b == o {
				set(n, "mixDisabled", true)
			}
		}
	}
	each("STREAMERXSTREAMMIX", func(i int, n *rcproto.Node) { set(n, "streammixmixLevel", 0.8) })
	srcColours := []string{"FF19C37D", "FF2F81F7", "FFF0A030", "FFD03CA0"}
	each("INPUTSOURCE", func(i int, n *rcproto.Node) {
		set(n, "inputId", i)
		set(n, "inputColour", srcColours[i%len(srcColours)])
		if i <= 3 {
			set(n, "inputMicrophoneGain", 42)
			set(n, "inputInstrumentGain", 10)
			set(n, "inputDigitalGain", 0)
			set(n, "inputType", 0) // a microphone, as on a Duo with firmware 1.7.6
			set(n, "inputMicrophoneType", 4)
			set(n, "inputPower", map[bool]int{true: 1, false: 0}[i == 1])
		}
	})
	shows := []string{"Standard", "Hide & Seek", "Podcast", "Stream", "Musik", "Interview", "Leer"}
	each("SHOWS/SHOW", func(i int, n *rcproto.Node) {
		set(n, "showName", shows[i%len(shows)])
		set(n, "showUUID", fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1))
		set(n, "showIcon", i)
		set(n, "showUID", i+1)
		set(n, "showSnapshotIndex", i)
	})
	cs := single("CURRENTSHOW")
	set(cs, "currentShowName", "Hide & Seek")
	set(cs, "currentShowUUID", "00000000-0000-4000-8000-000000000002")
	set(cs, "currentShowIcon", 1)
	recs := single("RECORDINGS")
	set(recs, "recordingTotalCount", l.Runs["RECORDINGS/RECORDING"].Count)
	set(recs, "recordingTotalDuration", 18234.5)
	each("RECORDINGS/RECORDING", func(i int, n *rcproto.Node) {
		set(n, "recordingUID", 1000+i)
		set(n, "recordingContent", fmt.Sprintf("REC%04d", i+1))
	})
	b := single("BUILD")
	set(b, "buildMixerVersion", "1.7.4 (Simulator)")
	set(b, "buildGuiVersion", "1.7.4 (Simulator)")
	set(b, "buildCallMeVersion", "2.1.0")
	set(b, "buildMixerModulesGitSha", "0000000")
	set(b, "buildGuiModulesGitSha", "0000000")
	pads := []string{"Applaus", "Lachen", "Trommelwirbel", "Airhorn", "Sonar-Ping", "Gefunden!",
		"Countdown", "Tusch", "Buzzer", "Glocke", "Jingle", "Outro"}
	each("SOUNDPADS/PAD", func(i int, n *rcproto.Node) {
		set(n, "padIdx", i)
		set(n, "padName", pads[i%len(pads)])
		set(n, "padColourIndex", i%12)
		set(n, "padType", 1)
		set(n, "padGain", -12.0) // dB
		set(n, "padPlayMode", i%3)
	})
	each("RADIO/RADIOTX", func(i int, n *rcproto.Node) {
		set(n, "txConnectionId", i)
		set(n, "txConnected", i == 0)
		set(n, "txDeviceSN", fmt.Sprintf("SIMTX%03d", i+1))
		set(n, "txBatteryLevel", 76-i*30)
		set(n, "txSignalQuality", 4)
		set(n, "txRssi", -48)
	})
}

// clearRedacted replaces the sanitizer's "<redacted>" placeholders with empty
// strings so they don't look like real data.
func clearRedacted(n *rcproto.Node) {
	for i := range n.Props {
		if v := n.Props[i].Value; v.Kind == rcproto.KindString && strings.Contains(v.S, "<redacted>") {
			n.Props[i].Value = rcproto.String("")
		}
	}
	for _, c := range n.Children {
		clearRedacted(c)
	}
}
