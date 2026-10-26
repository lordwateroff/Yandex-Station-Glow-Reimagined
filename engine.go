package main

import (
	"encoding/json"
	"log"
	"math"
	"os"
	"sync"
	"time"
)

const fps = 60

// settings is what the web panel edits and what survives a restart.
type settings struct {
	Power      bool     `json:"power"`
	Effect     string   `json:"effect"`
	Palette    string   `json:"palette"`
	Color      string   `json:"color"`      // #rrggbb
	Speed      float64  `json:"speed"`      // 0..1, 0.5 = normal
	Brightness float64  `json:"brightness"` // 0..1, perceptual
	Yield      bool     `json:"yield"`      // give the panel to Alice while she uses it
	Custom     []string `json:"custom"`     // the owner's own palette, 2..5 colours, used when Palette is "custom"
}

const customPalette = "custom"

var defaults = settings{Power: true, Effect: "aurora", Palette: "aurora", Color: "#ff7a3d", Speed: 0.5, Brightness: 0.7, Yield: true,
	Custom: []string{"#ff5e3a", "#ffb84d", "#3d8bff"}}

func (s *settings) sanitize() {
	if findEffect(s.Effect) == nil {
		s.Effect = defaults.Effect
	}
	if c, err := parseHex(s.Color); err != nil {
		s.Color = defaults.Color
	} else {
		s.Color = "#" + c.hex()
	}
	var custom []string
	for _, h := range s.Custom {
		if c, err := parseHex(h); err == nil && len(custom) < 5 {
			custom = append(custom, "#"+c.hex())
		}
	}
	if len(custom) < 2 {
		custom = append([]string(nil), defaults.Custom...)
	}
	s.Custom = custom
	if s.Palette != customPalette {
		s.Palette = findPalette(s.Palette).ID
	}
	s.Speed = clamp01(s.Speed)
	s.Brightness = clamp01(s.Brightness)
}

// palette resolves the chosen palette, building the owner's own one from its colours.
func (s *settings) palette() *palette {
	if s.Palette == customPalette {
		return newPalette(customPalette, "Своя", false, s.Custom...)
	}
	return findPalette(s.Palette)
}

func sameColours(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// speedFactor maps the 0..1 slider to a time multiplier, 1/4x .. 4x, 1x in the middle.
func speedFactor(s float64) float64 { return math.Pow(4, 2*s-1) }

// layerState is one effect being drawn; two exist while one cross-fades into the next.
type layerState struct {
	fx *effect
	t  float64
	pk peakTrack
}

type engine struct {
	mu    sync.Mutex
	set   settings
	path  string
	dirty time.Time

	cur, prev *layerState
	xfade     float64 // 0..1 progress of prev -> cur

	pal      *palette
	col      rgb
	palFrom  *palette // palette/colour changes also fade
	colFrom  rgb
	paramMix float64

	power   float64 // smoothed 0..1 master (power switch and brightness)
	yieldK  float64 // 0..1, 0 while the firmware owns the panel
	fw      *firmwareWatch
	audio   *analyzer // nil without the loopback tap
	snd     sound
	out     *output
	preview frame // last frame, for the web preview
	owning  bool
}

func newEngine(path string, out *output, fw *firmwareWatch) *engine {
	e := &engine{path: path, out: out, fw: fw, set: defaults, yieldK: 1}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &e.set)
	}
	e.set.sanitize()
	e.cur = &layerState{fx: findEffect(e.set.Effect)}
	e.pal = e.set.palette()
	e.col, _ = parseHex(e.set.Color)
	e.palFrom, e.colFrom, e.paramMix = e.pal, e.col, 1
	e.xfade = 1
	return e
}

func (e *engine) settings() settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set
}

func (e *engine) update(n settings) settings {
	n.sanitize()
	e.mu.Lock()
	defer e.mu.Unlock()
	if n.Effect != e.set.Effect {
		e.prev, e.cur = e.cur, &layerState{fx: findEffect(n.Effect)}
		e.xfade = 0
	}
	ownChanged := n.Palette == customPalette && !sameColours(n.Custom, e.set.Custom)
	if n.Palette != e.set.Palette || n.Color != e.set.Color || ownChanged {
		// freeze the current blend as the new starting point
		e.palFrom, e.colFrom = e.blendParams()
		e.pal = n.palette()
		e.col, _ = parseHex(n.Color)
		e.paramMix = 0
	}
	e.set = n
	e.dirty = time.Now()
	return e.set
}

// blendParams returns where the palette/colour fade currently is. Palettes cannot be
// blended as objects, so a palette change is drawn twice and mixed in render.
func (e *engine) blendParams() (*palette, rgb) {
	if e.paramMix >= 1 {
		return e.pal, e.col
	}
	return e.palFrom, mix(e.colFrom, e.col, smooth(e.paramMix))
}

func (e *engine) save() {
	e.mu.Lock()
	if e.dirty.IsZero() || time.Since(e.dirty) < 2*time.Second {
		e.mu.Unlock()
		return
	}
	b, _ := json.MarshalIndent(e.set, "", "  ")
	e.dirty = time.Time{}
	e.mu.Unlock()
	tmp := e.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err == nil {
		os.Rename(tmp, e.path)
	}
}

// approach moves v toward target with a time constant tau (seconds): smooth, never overshoots.
func approach(v, target, dt, tau float64) float64 {
	return target + (v-target)*math.Exp(-dt/tau)
}

func (e *engine) step(dt float64) (frame, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sp := speedFactor(e.set.Speed)
	e.cur.t += dt * sp
	if e.prev != nil {
		e.prev.t += dt * sp
		e.xfade += dt / 1.2
		if e.xfade >= 1 {
			e.xfade, e.prev = 1, nil
		}
	}
	if e.paramMix < 1 {
		e.paramMix = math.Min(1, e.paramMix+dt/1.0)
	}

	target := 0.0
	if e.set.Power {
		target = level(e.set.Brightness)
	}
	e.power = approach(e.power, target, dt, 0.25)

	busy := e.set.Yield && e.fw.busy.Load()
	if busy {
		e.yieldK = 0
	} else if e.yieldK < 1 {
		// let the firmware's own "out" animation finish, then fade back in
		if time.Since(time.Unix(0, e.fw.since.Load())) > 700*time.Millisecond {
			e.yieldK = math.Min(1, e.yieldK+dt/0.9)
		}
	}

	if e.audio != nil {
		e.snd = e.audio.snapshot()
	}
	var fr frame
	e.drawBlend(&fr)
	k := e.power * smooth(e.yieldK)
	for i := range fr {
		fr[i] = fr[i].scale(k)
	}
	e.preview = fr
	// switched off and fully faded: stop writing so the firmware's own idle screen can show
	off := !e.set.Power && e.power < 1e-4
	return fr, !busy && !off
}

func (e *engine) sndp() *sound {
	if e.audio == nil {
		return nil
	}
	return &e.snd
}

func (e *engine) drawBlend(out *frame) {
	draw := func(l *layerState, dst *frame) {
		if e.paramMix >= 1 {
			l.fx.draw(l.t, params{e.pal, e.col, e.sndp(), &l.pk}, dst)
			return
		}
		var a frame
		f := smooth(e.paramMix)
		l.fx.draw(l.t, params{e.palFrom, e.colFrom, e.sndp(), &l.pk}, &a)
		l.fx.draw(l.t, params{e.pal, e.col, e.sndp(), &l.pk}, dst)
		for i := range dst {
			dst[i] = mix(a[i], dst[i], f)
		}
	}
	draw(e.cur, out)
	if e.prev != nil {
		var p frame
		draw(e.prev, &p)
		f := smooth(e.xfade)
		for i := range out {
			out[i] = mix(p[i], out[i], f)
		}
	}
}

// look returns what a preview needs to draw any effect the way the panel would now.
func (e *engine) look() (*palette, rgb, sound, float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pal, e.col, e.snd, speedFactor(e.set.Speed)
}

func (e *engine) frame() frame {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.preview
}

func (e *engine) run(stop <-chan struct{}) {
	tick := time.NewTicker(time.Second / fps)
	defer tick.Stop()
	last := time.Now()
	saveTick := 0
	for {
		select {
		case <-stop:
			e.out.blank()
			e.save()
			return
		case now := <-tick.C:
			dt := now.Sub(last).Seconds()
			last = now
			if dt > 0.1 {
				dt = 0.1
			}
			fr, mine := e.step(dt)
			if mine {
				if err := e.out.write(&fr); err != nil {
					log.Printf("write fb: %v", err)
				}
				e.owning = true
			} else if e.owning {
				// stop writing at once; if the panel was handed back because we are off,
				// leave it dark rather than on our last dim frame
				if !e.fw.busy.Load() {
					e.out.blank()
				}
				e.owning = false
			}
			if saveTick++; saveTick >= fps {
				saveTick = 0
				e.save()
			}
		}
	}
}
