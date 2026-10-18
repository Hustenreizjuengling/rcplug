package console

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Hustenreizjuengling/rcplug/internal/desk"
	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Secret reports whether a property holds a credential or personal data that
// must never leave the device through the web UI (Wi-Fi key, passwords,
// sign-in codes, account names, call addresses, paired phone numbers, …).
var Secret = rcproto.Secret

// Level selects how much of the tree may be written.
type Level int

const (
	// LevelOff: read-only (the default).
	LevelOff Level = iota
	// LevelBasic: the curated everyday controls in basicRules, each with a
	// type and a value range.
	LevelBasic
	// LevelExpert: any existing property except the deny list, with type
	// checks but without range checks.
	LevelExpert
)

func (l Level) String() string {
	switch l {
	case LevelBasic:
		return "basic"
	case LevelExpert:
		return "expert"
	}
	return "off"
}

// ParseLevel parses "off" | "basic" | "expert".
func ParseLevel(s string) (Level, error) {
	switch s {
	case "off", "":
		return LevelOff, nil
	case "basic":
		return LevelBasic, nil
	case "expert":
		return LevelExpert, nil
	}
	return LevelOff, errf(http.StatusBadRequest, "invalid_level", "unknown control level %q", s)
}

func errf(status int, code, format string, args ...interface{}) error {
	return sdk.NewError(status, code, fmt.Sprintf(format, args...))
}

// denied lists properties that are never written, at any level: firmware
// update/reset machinery, power and lock-out switches, destructive storage
// and show management, file paths, accounts, network and credentials,
// factory test modes, identity and read-only telemetry. A nil set denies the
// whole node type.
var denied = map[string]map[string]bool{
	"SYSTEM": set("updateCheckRequested", "updateInitiateRequested", "downloadInitiateRequested",
		"downloadCancelRequested", "updateRebootRequested", "updateResetDeviceRequested",
		"updateResetAppRequested", "updateResetAfterFWURequested", "updateViaUSB", "updateComplete",
		"updateDownloaded", "updateChecking", "updateNoInternet", "updateDownloadProgress",
		"updateInstalledProgress", "updateVersion", "appUpdateAvailable", "osUpdateAvailable",
		"systemFirmwareVersion", "systemSerialNumber", "boardType", "engineMode", "remountPadStorage",
		"badPadDetected", "usbHostUnspportedFirmware", "transferModeType", "rcSyncModeRequest",
		"rcSyncState", "rcSyncDeviceModel", "systemMainMode", "lastRecordingID", "shareAnom",
		"shareResource", "systemBetaMode", "powerOffRequest", "disableAllPhysicalButtons", "usb1Connected"),
	"STORAGEVOLUME": nil,
	"RECORDINGS":    nil,
	"RECORDING":     nil,
	"PADRECORDER":   set("padRecordClear", "padRecordMemoryFull"),
	// Readings only the desk writes, and the limits the node checks values
	// against (writing those would lift the limits).
	"RECORDER":           set("recordState", "recordTimeMs", "recordBytesPerSecond"),
	"OUTPUT":             set("outputMonAutoMuteActive", "outputBTAutoMuteActive"),
	"PAD":                set("padActive", "padProgress", "padSIPFlashState"),
	"PLAYER":             set("playerProgress", "playerFileSize"),
	"FADER":              set("faderMin", "faderMax"),
	"POT":                set("potMin", "potMax"),
	"INPUTSOURCE":        set("inputWirelessSN"), // identity of a paired wireless microphone
	"BUILD":              nil,
	"TEST":               nil,
	"OAUTH":              nil,
	"NETWORK":            nil,
	"PROXY":              nil,
	"WIFISCANRESULT":     nil,
	"SHOWCONTROL":        nil,
	"SHOW":               nil,
	"SHOWS":              nil,
	"CURRENTSHOW":        nil,
	"FXPRESETS":          nil,
	"SIPPHONEBOOKARRAY":  nil,
	"STREAMERXMIXPRESET": set("streamerXPresetCreated", "streamerXPresetName"),
	"SIPADVANCED":        nil,
	"SIPCALLING":         nil,
	"SIPREGISTRATION":    nil,
	"SIPCALLSLOTS":       nil,
	"RADIO":              nil,
	"RADIORX":            nil,
	"RADIOTX": set("txConnectionId", "txDeviceSN", "txDeviceType", "txConnected", "txRssi",
		"txSignalQuality", "txBatteryLevel", "txChargingState"),
	"FXPRESET":          nil,
	"AUDIO":             nil,
	"APP":               nil,
	"PHYSICALINTERFACE": nil,
	"METER":             nil, // live meter readings, written only by the desk
}

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// isDenied reports whether a property is on the hard deny list. Any property
// that names a file path is denied too: the desk process runs as root.
func isDenied(nodeType, prop string) bool {
	if strings.Contains(strings.ToLower(prop), "path") {
		return true
	}
	m, ok := denied[nodeType]
	if !ok {
		return false
	}
	return m == nil || m[prop]
}

// rule describes one property writable at the basic level.
type rule struct {
	kind     byte    // 'b' bool, 'i' integer, 'd' double, 's' special (see check), 't' trigger
	min, max float64 // inclusive range for 'i' and 'd'
	// step bounds |new − current| for integers whose real range is unknown,
	// so a remote client can only nudge them, never jump to an extreme.
	step  float64
	check func(n *rcproto.Node, l *rcproto.Layout, path []int, v *rcproto.Value) error
}

func boolean() rule               { return rule{kind: 'b'} }
func unit() rule                  { return rule{kind: 'd', min: 0, max: 1} }
func dbl(lo, hi float64) rule     { return rule{kind: 'd', min: lo, max: hi} }
func integer(lo, hi float64) rule { return rule{kind: 'i', min: lo, max: hi} }
func nudge(lo, hi, step float64) rule {
	return rule{kind: 'i', min: lo, max: hi, step: step}
}
func trigger() rule { return rule{kind: 't'} }
func special(kind byte, check func(*rcproto.Node, *rcproto.Layout, []int, *rcproto.Value) error) rule {
	return rule{kind: kind, check: check}
}

func bools(m map[string]rule, names ...string) {
	for _, n := range names {
		m[n] = boolean()
	}
}

func units(m map[string]rule, names ...string) {
	for _, n := range names {
		m[n] = unit()
	}
}

// basicRules is the curated everyday control set. A property is writable at
// the basic level only if it has a rule here.
var basicRules = func() map[string]map[string]rule {
	r := map[string]map[string]rule{}
	ch := map[string]rule{"channelInputSource": special('i', checkSource)}
	bools(ch, "channelOutputMute", "channelCueEnable", "channelBypassProcessing", "channelPanOn",
		"channelAdvancedProcessing", "channelTalkbackEnable", "channelWirelessMute",
		"aphexOn", "eqOn", "eqLowOn", "eqMidOn", "eqHighOn", "deesserOn", "compressorOn", "noiseGateOn", "hpfOn")
	units(ch, "channelPan", "channelDepth", "channelSparkle", "channelPunch",
		"aphexAEMix", "aphexAETune", "aphexBBDrive", "aphexBBTune",
		"eqLowGain", "eqLowShelf", "eqLowQ", "eqMidGain", "eqMidBell", "eqMidQ", "eqHighGain", "eqHighBell", "eqHighQ",
		"deesserThreshold", "deesserRatio", "deesserFrequency", "deesserAttack", "deesserRelease", "deesserGain",
		"compressorThreshold", "compressorRatio", "compressorAttack", "compressorRelease", "compressorGain",
		"noiseGateThreshold", "noiseGateRange", "noiseGateAttack", "noiseGateHold", "noiseGateRelease", "noiseGateHysteresis",
		"hpfFrequency", "hpfSlope")
	r["CHANNEL"] = ch
	r["FADER"] = map[string]rule{"faderLevel": special('i', checkFader)}
	r["MIX"] = map[string]rule{
		"mixMute": boolean(), "mixDisabled": boolean(),
		"mixLevelWithAnchor": special('s', checkMixLevel),
		"mixLinkRequest":     trigger(), "mixUnlinkRequest": trigger(),
	}
	r["INPUTSOURCE"] = map[string]rule{
		// Gains: real ranges unknown — allow small steps only.
		"inputMicrophoneGain": nudge(0, 100, 10), "inputInstrumentGain": nudge(0, 100, 10),
		"inputDigitalGain": nudge(-100, 100, 10),
		"inputPower":       integer(0, 1), // phantom power off/on
		"inputPhaseFlip":   boolean(),
	}
	out := map[string]rule{"headphone1Level": nudge(0, 127, 16)}
	bools(out, "outputMonMute", "outputMonAutoMute", "outputBTMute", "outputBTAutoMute")
	units(out, "outputMonLevel", "outputBTLevel")
	r["OUTPUT"] = out
	m := map[string]rule{"masterDelaySeconds": dbl(0, 2)}
	bools(m, "masterCompellorOn", "masterDelayOn")
	units(m, "masterCompellorThreshold", "masterCompellorAttack", "masterCompellorRelease", "masterCompellorGain")
	r["MASTERCHANNEL"] = m
	r["DUCKER"] = map[string]rule{"duckerDepth": unit()}
	r["EMERGENCYMUTE"] = map[string]rule{"emergencyMuteActive": boolean()}
	r["RECORDER"] = map[string]rule{"requestRecordState": integer(0, 2), "requestDropMarker": integer(0, 1<<53)}
	r["GUI"] = map[string]rule{
		"selectedBank":     special('i', checkBank),
		"screenBrightness": integer(0, 100), "activeButtonsBrightness": integer(0, 100),
		"inactiveButtonsBrightness": integer(0, 100), "autoBrightness": integer(0, 1),
		"screenDimAfterSeconds": boolean(),
	}
	fx := map[string]rule{"reverbModel": dbl(0, 10), "pitchShiftSemitones": dbl(-12, 12)}
	bools(fx, "reverbOn", "echoOn", "pitchShiftOn", "distortionOn", "robotOn", "voiceDisguiseOn")
	units(fx, "reverbMix", "reverbLowCut", "reverbHighCut", "echoMix", "echoDelay", "echoDecay",
		"echoLowCut", "echoHighCut", "distortionIntensity", "robotLevel", "robotMix")
	r["EFFECTS_PARAMETERS"] = fx
	r["PADBUTTON"] = map[string]rule{"padButtonPressed": boolean()}
	r["PAD"] = map[string]rule{"padGain": unit()}
	return r
}()

// triggerProps rest as bools in the full sync but are driven with the binary
// two-bool trigger pulse on the wire (validated on hardware by the
// rodecaster-protocol project).
var triggerProps = set("mixLinkRequest", "mixUnlinkRequest")

// Writable reports whether level permits writing the property at all (before
// value checks). Used to tell the UI which controls are live.
func Writable(level Level, nodeType, prop string) bool {
	if level == LevelOff || Secret(nodeType, prop) || isDenied(nodeType, prop) {
		return false
	}
	if level == LevelExpert {
		return true
	}
	_, ok := basicRules[nodeType][prop]
	return ok
}

// Capabilities lists, per node type present in root, the properties writable
// at level — an exact list the UI can use without knowing the rules.
func Capabilities(level Level, root *rcproto.Node) map[string]interface{} {
	out := map[string]interface{}{"level": level.String()}
	if level == LevelOff || root == nil {
		out["writable"] = map[string][]string{}
		return out
	}
	seen := map[string]map[string]bool{}
	var walk func(n *rcproto.Node)
	walk = func(n *rcproto.Node) {
		for _, p := range n.Props {
			if Writable(level, n.Type, p.Name) {
				if seen[n.Type] == nil {
					seen[n.Type] = map[string]bool{}
				}
				seen[n.Type][p.Name] = true
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	w := map[string][]string{}
	for t, props := range seen {
		for p := range props {
			w[t] = append(w[t], p)
		}
		sort.Strings(w[t])
	}
	out["writable"] = w
	return out
}

// CheckWrite validates a write against the policy and converts the JSON input
// to a value of the property's existing wire type. It never allows creating a
// property: writing unknown names makes the desk add them permanently.
func CheckWrite(level Level, s desk.Snapshot, path []int, name string, in interface{}) (rcproto.Value, error) {
	if s.Root == nil {
		return rcproto.Value{}, desk.ErrNotSynced
	}
	n := s.Root.At(path)
	if n == nil {
		return rcproto.Value{}, errf(http.StatusBadRequest, "no_node", "no node at path %v", path)
	}
	cur, ok := n.Get(name)
	if !ok {
		return rcproto.Value{}, errf(http.StatusBadRequest, "unknown_property",
			"%s has no property %q (new properties are never created)", n.Type, name)
	}
	if !Writable(level, n.Type, name) {
		if level == LevelOff {
			return rcproto.Value{}, errf(http.StatusForbidden, "control_off", "control is off (read-only)")
		}
		return rcproto.Value{}, errf(http.StatusForbidden, "denied", "%s.%s is not writable at level %q", n.Type, name, level)
	}
	v, err := coerce(cur, name, in)
	if err != nil {
		return rcproto.Value{}, errf(http.StatusBadRequest, "invalid_value", "%s.%s: %v", n.Type, name, err)
	}
	if err := checkValue(level, n, s.Layout, path, name, cur, &v); err != nil {
		return rcproto.Value{}, errf(http.StatusBadRequest, "out_of_range", "%s.%s: %v", n.Type, name, err)
	}
	return v, nil
}

// coerce converts a decoded JSON value to cur's wire type.
func coerce(cur rcproto.Value, name string, in interface{}) (rcproto.Value, error) {
	if s, ok := in.(string); ok && (s == "press" || s == "release") && (triggerProps[name] || cur.Kind == rcproto.KindBinary) {
		if cur.Kind == rcproto.KindBinary && !triggerProps[name] && len(cur.B) != 0 && !rcproto.IsTriggerPayload(cur.B) {
			return rcproto.Value{}, fmt.Errorf("not a trigger property")
		}
		if s == "press" {
			return rcproto.Binary(rcproto.TriggerPress), nil
		}
		return rcproto.Binary(rcproto.TriggerRelease), nil
	}
	if triggerProps[name] {
		return rcproto.Value{}, fmt.Errorf(`trigger property: send "press"`)
	}
	switch cur.Kind {
	case rcproto.KindBoolTrue, rcproto.KindBoolFalse:
		b, ok := in.(bool)
		if !ok {
			return rcproto.Value{}, fmt.Errorf("expected true or false")
		}
		return rcproto.Bool(b), nil
	case rcproto.KindInt, rcproto.KindInt64:
		f, ok := number(in)
		if !ok || f != math.Trunc(f) {
			return rcproto.Value{}, fmt.Errorf("expected an integer")
		}
		if cur.Kind == rcproto.KindInt {
			if f < math.MinInt32 || f > math.MaxInt32 {
				return rcproto.Value{}, fmt.Errorf("outside the 32-bit range")
			}
			return rcproto.Int(int64(f)), nil
		}
		if math.Abs(f) > 1<<53 {
			return rcproto.Value{}, fmt.Errorf("too large")
		}
		return rcproto.Int64(int64(f)), nil
	case rcproto.KindDouble:
		f, ok := number(in)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return rcproto.Value{}, fmt.Errorf("expected a number")
		}
		return rcproto.Double(f), nil
	case rcproto.KindString:
		s, ok := in.(string)
		if !ok {
			return rcproto.Value{}, fmt.Errorf("expected a string")
		}
		if len(s) > 1024 {
			return rcproto.Value{}, fmt.Errorf("string too long")
		}
		if strings.ContainsRune(s, 0) {
			// The wire format ends strings at NUL; the desk and the mirror
			// would disagree about the value.
			return rcproto.Value{}, fmt.Errorf("string contains a NUL character")
		}
		return rcproto.String(s), nil
	case rcproto.KindBinary:
		return rcproto.Value{}, fmt.Errorf(`binary values can only be triggered ("press"/"release")`)
	}
	return rcproto.Value{}, fmt.Errorf("type %c cannot be written", cur.TypeCode())
}

func number(in interface{}) (float64, bool) {
	switch x := in.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// checkValue applies value constraints: the basic rule for the property, and
// in every level the structural sanity checks (fader bounds, mix format).
func checkValue(level Level, n *rcproto.Node, l *rcproto.Layout, path []int, name string, cur rcproto.Value, v *rcproto.Value) error {
	switch {
	case n.Type == "FADER" && name == "faderLevel" && level == LevelExpert:
		return intBounds(n, v, "faderMin", "faderMax")
	case n.Type == "POT" && name == "potLevel":
		return intBounds(n, v, "potMin", "potMax")
	case n.Type == "MIX" && name == "mixLevelWithAnchor" && level == LevelExpert:
		return checkMixLevel(n, l, path, v)
	}
	if level != LevelBasic {
		return nil
	}
	r, ok := basicRules[n.Type][name]
	if !ok {
		return fmt.Errorf("no rule") // unreachable: Writable checked it
	}
	if r.check != nil {
		return r.check(n, l, path, v)
	}
	switch r.kind {
	case 'b':
		if !v.IsBool() {
			return fmt.Errorf("expected true or false")
		}
	case 'i', 'd':
		x := v.Num()
		if !v.IsNumber() || x < r.min || x > r.max {
			return fmt.Errorf("must be between %s and %s", fmtNum(r.min), fmtNum(r.max))
		}
		if r.kind == 'i' && v.Kind == rcproto.KindDouble {
			return fmt.Errorf("expected an integer")
		}
		if r.step > 0 && cur.IsNumber() && math.Abs(x-cur.Num()) > r.step {
			return fmt.Errorf("may change by at most %s per write (range of this value is not documented)", fmtNum(r.step))
		}
	case 't':
		if v.Kind != rcproto.KindBinary {
			return fmt.Errorf(`trigger property: send "press"`)
		}
	}
	return nil
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func intBounds(n *rcproto.Node, v *rcproto.Value, minName, maxName string) error {
	lo, hi := int64(0), int64(127)
	if mn, ok := n.Get(minName); ok && mn.IsNumber() {
		if mx, ok := n.Get(maxName); ok && mx.IsNumber() && mx.I > mn.I {
			lo, hi = mn.I, mx.I
		}
	}
	if v.I < lo || v.I > hi {
		return fmt.Errorf("must be between %d and %d", lo, hi)
	}
	return nil
}

// checkFader: physical faders are not motorised, so they cannot follow a
// remote value; only virtual faders are writable at the basic level.
func checkFader(n *rcproto.Node, l *rcproto.Layout, path []int, v *rcproto.Value) error {
	if l == nil {
		return fmt.Errorf("layout unknown")
	}
	idx := l.Runs["FADER"].IndexOf(path)
	if idx < 0 {
		return fmt.Errorf("not a known fader")
	}
	if idx < l.PhysicalFaders {
		return fmt.Errorf("physical faders are not motorised and cannot be moved remotely")
	}
	return intBounds(n, v, "faderMin", "faderMax")
}

var mixLevelRe = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*\|\s*([0-9]*\.?[0-9]+)\s*$`)

func checkMixLevel(n *rcproto.Node, l *rcproto.Layout, path []int, v *rcproto.Value) error {
	m := mixLevelRe.FindStringSubmatch(v.S)
	if m == nil {
		return fmt.Errorf(`format must be "anchor|level"`)
	}
	a, _ := strconv.ParseFloat(m[1], 64)
	b, _ := strconv.ParseFloat(m[2], 64)
	if a < 0 || a > 1 || b < 0 || b > 1 {
		return fmt.Errorf("both parts must be between 0 and 1")
	}
	*v = rcproto.String(strconv.FormatFloat(a, 'f', 6, 64) + "|" + strconv.FormatFloat(b, 'f', 6, 64))
	return nil
}

func checkSource(n *rcproto.Node, l *rcproto.Layout, path []int, v *rcproto.Value) error {
	max := int64(len(rcproto.Sources) - 1)
	if l != nil && l.SourceCount > 0 {
		max = int64(l.SourceCount - 1)
	}
	if !v.IsNumber() || v.I < -1 || v.I > max {
		return fmt.Errorf("source must be between -1 (none) and %d", max)
	}
	return nil
}

func checkBank(n *rcproto.Node, l *rcproto.Layout, path []int, v *rcproto.Value) error {
	banks := int64(8)
	if l != nil && l.PadBanks > 0 {
		banks = int64(l.PadBanks)
	}
	if !v.IsNumber() || v.I < 0 || v.I >= banks {
		return fmt.Errorf("bank must be between 0 and %d", banks-1)
	}
	return nil
}
