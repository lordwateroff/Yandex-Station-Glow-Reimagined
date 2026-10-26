package main

import (
	"math"
	"testing"
)

func TestLayout(t *testing.T) {
	seen := map[point]int{}
	for i, p := range layout {
		if j, ok := seen[p]; ok {
			t.Fatalf("LED %d and %d share %v", i, j, p)
		}
		seen[p] = i
		if p.X < 0 || p.X >= gridW || p.Y < 0 || p.Y >= gridH {
			t.Fatalf("LED %d off grid: %v", i, p)
		}
		// main panel LEDs sit on the checkerboard: odd rows on odd columns are empty
		if i < 84 && (int(p.X)+int(p.Y))%2 != 1 {
			t.Fatalf("LED %d at %v is off the checkerboard", i, p)
		}
	}
	// spot checks against the wiki map
	for i, want := range map[int]point{0: {1, 0}, 14: {14, 1}, 37: {0, 5}, 42: {13, 6}, 46: {14, 7}, 83: {13, 12}, 86: {6, 5}, 84: {8, 7}} {
		if layout[i] != want {
			t.Errorf("LED %d at %v, want %v", i, layout[i], want)
		}
	}
}

func TestPanelBusy(t *testing.T) {
	cases := []struct {
		line     string
		busy, ok bool
	}{
		{"[1735:1758] [2026-10-26 15:21:50.844] [device_state_handler] [info] [device_state_handler.cpp:743] Play animations: [ /system/vendor/quasar/ledpatterns/panel//alice_listening.led ] [ /system/vendor/quasar/ledpatterns/oknix//alice_listening.led ] source=onSDKState", true, true},
		{"Play animations: [ idle.led /system/vendor/quasar/ledpatterns/panel//alice_thinking.led ] [ x ] source=onSDKState", true, true},
		{"Play animations: [ spectrogram ] [ /system/vendor/quasar/ledpatterns/oknix//alice_speaking.led ] source=onSDKState", true, true},
		{"Play animations: [ /system/vendor/quasar/ledpatterns/panel//alice_listening_out.led idle.led ] [ y idle.led ] source=onSDKState", false, true},
		{"Play animations: [ idle.led ] [ idle.led ] source=onSDKState", false, true},
		{"[info] Spectrum paused. playerInstanceId: 1831978188280", false, false},
	}
	for _, c := range cases {
		b, ok := panelBusy(c.line)
		if b != c.busy || ok != c.ok {
			t.Errorf("%q: got busy=%v ok=%v", c.line[len(c.line)-60:], b, ok)
		}
	}
}

func TestPaletteSeamless(t *testing.T) {
	for _, p := range palettes {
		a, b := p.at(0.9999), p.at(0)
		if math.Abs(a.R-b.R)+math.Abs(a.G-b.G)+math.Abs(a.B-b.B) > 0.01 {
			t.Errorf("%s: seam at t=0 (%v vs %v)", p.ID, a, b)
		}
	}
}

func TestHexRoundTrip(t *testing.T) {
	for _, s := range []string{"#000000", "#ffffff", "#ff7a3d", "#00a2ff"} {
		c, err := parseHex(s)
		if err != nil || "#"+c.hex() != s {
			t.Errorf("%s -> %v -> %s (%v)", s, c, c.hex(), err)
		}
	}
}

// Every effect must stay finite and inside 0..1 over a long run.
func TestEffectsBounded(t *testing.T) {
	loud := sound{Level: 1, Beat: 1, Active: true}
	for i := range loud.Bands {
		loud.Bands[i] = 1
	}
	p := params{palettes[0], mustHex("#ff7a3d"), &loud, &peakTrack{}}
	for _, fx := range effects {
		var fr frame
		for step := 0; step < 2000; step++ {
			fx.draw(float64(step)*0.37, p, &fr)
			for i, c := range fr {
				for _, v := range []float64{c.R, c.G, c.B} {
					if math.IsNaN(v) || v < -1e-9 || v > 1+1e-9 {
						t.Fatalf("%s: LED %d = %v at step %d", fx.ID, i, c, step)
					}
				}
			}
		}
	}
}

func BenchmarkFrame(b *testing.B) {
	e := &engine{set: defaults, fw: &firmwareWatch{}, yieldK: 1, xfade: 0, paramMix: 0.5}
	e.cur = &layerState{fx: findEffect("aurora")}
	e.prev = &layerState{fx: findEffect("lava")}
	e.pal, e.palFrom = palettes[0], palettes[1]
	for i := 0; i < b.N; i++ {
		e.xfade, e.paramMix = 0.5, 0.5
		e.step(1.0 / fps)
	}
}

func sineFrames(hz, amp float64, n int) []byte {
	b := make([]byte, n*4)
	for i := 0; i < n; i++ {
		v := int16(amp * 32767 * math.Sin(2*math.Pi*hz*float64(i)/sampleRate))
		b[i*4], b[i*4+1] = byte(v), byte(uint16(v)>>8)
		b[i*4+2], b[i*4+3] = byte(v), byte(uint16(v)>>8)
	}
	return b
}

func TestAnalyzerBassAndSilence(t *testing.T) {
	a := newAnalyzer()
	a.feed(sineFrames(60, 0.3, sampleRate)) // one second of 60 Hz
	s := a.snapshot()
	if !s.Active {
		t.Fatalf("60 Hz tone not seen as active: %+v", s)
	}
	if s.Bands[0]+s.Bands[1] < 0.8 || s.Bands[numBands-1] > 0.3 {
		t.Errorf("60 Hz should light the low bands only: %v", s.Bands)
	}
	a.feed(make([]byte, 3*sampleRate*4)) // three seconds of silence
	s = a.snapshot()
	if s.Active || s.Level > 0.05 {
		t.Errorf("silence still active/level %.2f: %+v", s.Level, s)
	}
}

func TestBandEdgesIncrease(t *testing.T) {
	a := newAnalyzer()
	for b := 1; b <= numBands; b++ {
		if a.edges[b] <= a.edges[b-1] || a.edges[b] > fftSize/2 {
			t.Fatalf("edges not increasing / out of range: %v", a.edges)
		}
	}
}

func TestAnalyzerIgnoresDCOffset(t *testing.T) {
	a := newAnalyzer()
	b := make([]byte, 2*sampleRate*4)
	for i := 0; i < len(b); i += 2 {
		b[i], b[i+1] = 0x20, 0x00 // constant +32 on both channels
	}
	a.feed(b)
	if s := a.snapshot(); s.Active || s.Bands[0] > 0.05 {
		t.Errorf("a constant offset reads as sound: %+v", s)
	}
}

func TestCustomPalette(t *testing.T) {
	s := defaults
	s.Palette, s.Custom = customPalette, []string{"#FF0000", "bad", "#00ff00", "#0000ff", "#ffffff", "#000000", "#123456"}
	s.sanitize()
	if s.Palette != customPalette || len(s.Custom) != 5 || s.Custom[0] != "#ff0000" {
		t.Fatalf("custom palette not kept/normalised: %+v", s)
	}
	if p := s.palette(); p.ID != customPalette || len(p.labs) != 5 {
		t.Fatalf("custom palette not built: %+v", p)
	}
	s.Custom = []string{"#ff0000"} // one colour is not a palette
	s.sanitize()
	if len(s.Custom) < 2 {
		t.Fatalf("custom palette left with %d colours", len(s.Custom))
	}
}
