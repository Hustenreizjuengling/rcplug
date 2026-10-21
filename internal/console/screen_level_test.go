package console

import "testing"

// The screen level (the plugin standing in for RØDE's screen) writes the
// requests that screen makes, which the expert level keeps denied; readings,
// limits, identities and paths stay denied for it too.
func TestScreenLevel(t *testing.T) {
	allowedForScreen := [][2]string{
		{"SYSTEM", "updateCheckRequested"}, {"SYSTEM", "transferModeType"}, {"SYSTEM", "rcSyncModeRequest"},
		{"SYSTEM", "shareAnom"}, {"STORAGEVOLUME", "storageVolumeErase"}, {"STORAGEVOLUME", "storageVolumeEject"},
		{"OAUTH", "requestSignIn"}, {"FXPRESETS", "anything"}, {"SHOWS", "showName"}, {"NETWORK", "wifiSSID"},
		{"RADIO", "radioPair"}, {"SIPCALLING", "sipDial"}, {"PADRECORDER", "padRecordClear"},
	}
	for _, c := range allowedForScreen {
		if !Writable(LevelScreen, c[0], c[1]) {
			t.Errorf("screen level must write %s.%s", c[0], c[1])
		}
		if Writable(LevelExpert, c[0], c[1]) {
			t.Errorf("expert level must not write %s.%s", c[0], c[1])
		}
	}
	deniedForAll := [][2]string{
		{"SYSTEM", "systemSerialNumber"}, {"SYSTEM", "updateVersion"}, {"SYSTEM", "powerOffRequest"},
		{"STORAGEVOLUME", "storageVolumeCapacity"}, {"OAUTH", "oauthToken"}, {"METER", "meterLevelL"},
		{"RECORDER", "recordState"}, {"FADER", "faderMax"}, {"RADIOTX", "txDeviceSN"}, {"BUILD", "buildHash"},
		{"TEST", "toneGeneration"}, {"WIFISCANRESULT", "ssid"}, {"SHOWS", "showFilePath"}, {"INPUTSOURCE", "inputWirelessSN"},
	}
	for _, c := range deniedForAll {
		if Writable(LevelScreen, c[0], c[1]) {
			t.Errorf("screen level must not write %s.%s", c[0], c[1])
		}
	}
	// Everything the expert level writes, the screen level writes too.
	for _, c := range [][2]string{{"CHANNEL", "channelDepth"}, {"INPUTSOURCE", "inputMicrophoneType"}, {"GUI", "lang"}} {
		if !Writable(LevelScreen, c[0], c[1]) || !Writable(LevelExpert, c[0], c[1]) {
			t.Errorf("%s.%s must be writable at expert and screen level", c[0], c[1])
		}
	}
	if LevelScreen.String() != "screen" {
		t.Errorf("String() = %q", LevelScreen.String())
	}
	if l, err := ParseLevel("screen"); err == nil {
		t.Errorf("people must not choose the screen level, got %v", l)
	}
}
