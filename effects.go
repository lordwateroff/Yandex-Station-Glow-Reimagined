package main

import "math"

// An effect draws one frame at effect time t. t advances with the speed setting, so
// changing speed never makes the picture jump.
type params struct {
	pal   *palette
	color rgb
	snd   *sound     // nil when there is no audio tap
	pk    *peakTrack // state of the Peaks effect for this drawing; nil gets a throwaway
}

type effect struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	UsesPal bool   `json:"palette"`
	UsesCol bool   `json:"color"`
	Music   bool   `json:"music"`
	draw    func(t float64, p params, out *frame)
}

var silence sound

func (p params) sound() *sound {
	if p.snd == nil {
		return &silence
	}
	return p.snd
}

// level turns a perceptual brightness 0..1 into a linear light factor.
func level(k float64) float64 { return math.Pow(clamp01(k), 2.2) }

// uv: LED position relative to the panel centre, about -1..1 on both axes.
func uv(i int) (float64, float64) {
	return (layout[i].X - centreX) / 7.5, (layout[i].Y - centreY) / 7.5
}

var effects = []*effect{
	{ID: "aurora", Name: "Сияние", UsesPal: true, draw: func(t float64, p params, out *frame) {
		for i := range out {
			u, v := uv(i)
			n := noise3(u*1.1, v*1.4-t*0.11, t*0.07)
			band := 0.5 + 0.5*math.Sin((v+0.9*n)*math.Pi*1.3+t*0.45)
			k := 0.18 + 0.82*smooth(band)
			out[i] = p.pal.at(0.5 + 0.6*n + t*0.015).scale(level(k))
		}
	}},
	{ID: "flow", Name: "Перелив", UsesPal: true, draw: func(t float64, p params, out *frame) {
		for i := range out {
			u, v := uv(i)
			out[i] = p.pal.at(t*0.04 + u*0.12 + v*0.08)
		}
	}},
	{ID: "breathe", Name: "Дыхание", UsesCol: true, draw: func(t float64, p params, out *frame) {
		ph := 0.5 - 0.5*math.Cos(2*math.Pi*t*0.16)
		k := level(0.1 + 0.9*ph)
		for i := range out {
			out[i] = p.color.scale(k)
		}
	}},
	{ID: "waves", Name: "Волны", UsesPal: true, draw: func(t float64, p params, out *frame) {
		for i := range out {
			u, v := uv(i)
			r := math.Hypot(u, v)
			w := 0.5 + 0.5*math.Cos(2*math.Pi*(r*0.85-t*0.22))
			out[i] = p.pal.at(r*0.3 - t*0.05).scale(level(0.22 + 0.78*w*w))
		}
	}},
	{ID: "swirl", Name: "Вихрь", UsesPal: true, draw: func(t float64, p params, out *frame) {
		for i := range out {
			u, v := uv(i)
			a := math.Atan2(v, u) / (2 * math.Pi)
			r := math.Hypot(u, v)
			k := 0.55 + 0.45*math.Sin(2*math.Pi*(a*2+r*0.6-t*0.18))
			out[i] = p.pal.at(a + r*0.2 + t*0.06).scale(level(k))
		}
	}},
	{ID: "lava", Name: "Лава", UsesPal: true, draw: func(t float64, p params, out *frame) {
		var bx, by [4]float64
		for j := range bx {
			f := float64(j)
			bx[j] = 0.85 * math.Sin(t*(0.13+0.04*f)+f*1.7)
			by[j] = 0.85 * math.Cos(t*(0.11+0.05*f)+f*2.3)
		}
		for i := range out {
			u, v := uv(i)
			field := 0.0
			for j := range bx {
				dx, dy := u-bx[j], v-by[j]
				field += 0.09 / (dx*dx + dy*dy + 0.02)
			}
			k := 0.12 + 0.88*smooth(clamp01((field-0.35)/1.1))
			out[i] = p.pal.at(field*0.12 + t*0.01).scale(level(k))
		}
	}},
	{ID: "candle", Name: "Свеча", draw: func(t float64, p params, out *frame) {
		deep, flame, core := candleDeep, candleFlame, candleCore
		gust := 0.85 + 0.15*noise3(t*0.9, 3.1, 7.7)
		for i := range out {
			u, v := uv(i)
			h := clamp01((v + 0.9) / 1.8) // 0 at the top, 1 at the bottom
			n := noise3(u*1.8, v*1.6+t*0.9, t*0.5)
			k := clamp01(h*1.05+0.25*n-0.1) * gust
			c := mix(deep, flame, smooth(clamp01(k*1.4)))
			c = mix(c, core, smooth(clamp01(k*1.6-0.8)))
			out[i] = c.scale(level(0.08 + 0.92*k))
		}
	}},
	{ID: "spectrum", Name: "Спектр", UsesPal: true, Music: true, draw: func(t float64, p params, out *frame) {
		s := p.sound()
		for i := range out {
			x, y := layout[i].X, layout[i].Y
			d := math.Abs(x-centreX) / centreX // 0 in the middle, 1 at the edges
			// bass in the middle, treble at the sides, read between bands for a smooth curve
			h := s.band(d) * (gridH + 0.5)       // bar height in rows, from the bottom
			fill := clamp01(h - (gridH - 1 - y)) // soft top edge
			c := p.pal.at(d*0.55 + t*0.02 + (gridH-1-y)/gridH*0.25)
			k := 0.05 + 0.95*fill
			out[i] = c.scale(level(k))
		}
	}},
	{ID: "pulse", Name: "Пульс", UsesPal: true, Music: true, draw: func(t float64, p params, out *frame) {
		s := p.sound()
		for i := range out {
			u, v := uv(i)
			r := math.Hypot(u, v)
			n := noise3(u*1.2, v*1.2, t*0.15)
			base := 0.12 + 0.55*s.Level
			// the beat lands in the middle and runs outward as it fades
			ring := s.Beat * math.Exp(-math.Pow((r-(1-s.Beat)*1.3)/0.28, 2))
			k := clamp01(base + 0.25*n*s.Level + 0.9*ring)
			out[i] = p.pal.at(0.5 + 0.4*n + t*0.02 + s.Beat*0.15).scale(level(k))
		}
	}},
	{ID: "mirror", Name: "Зеркало", UsesPal: true, Music: true, draw: func(t float64, p params, out *frame) {
		s := p.sound()
		for i := range out {
			x, y := layout[i].X, layout[i].Y
			d := math.Abs(x-centreX) / centreX
			// bars grow from the middle row up and down at once
			h := s.band(d) * (centreY + 0.6)
			fill := clamp01(h - math.Abs(y-centreY) + 0.5)
			c := p.pal.at(d*0.55 + t*0.02 + math.Abs(y-centreY)/centreY*0.2)
			out[i] = c.scale(level(0.05 + 0.95*fill))
		}
	}},
	{ID: "rings", Name: "Кольца", UsesPal: true, Music: true, draw: func(t float64, p params, out *frame) {
		s := p.sound()
		for i := range out {
			u, v := uv(i)
			r := clamp01(math.Hypot(u, v) / 1.15) // 0 at the logo, 1 in the corners
			k := 0.05 + 0.95*s.band(r)
			out[i] = p.pal.at(r*0.6 + t*0.03).scale(level(k))
		}
	}},
	{ID: "voice", Name: "Голос", UsesPal: true, Music: true, draw: func(t float64, p params, out *frame) {
		s := p.sound()
		for i := range out {
			x, y := layout[i].X, layout[i].Y
			d := math.Abs(x-centreX) / centreX
			// a soft band across the middle that swells with the sound, tallest in the centre,
			// like the firmware's own animation while Alice talks
			amp := s.band(d) * (1 - 0.55*d*d) * (0.4 + 0.6*s.Level)
			half := 0.6 + amp*5.5
			k := math.Exp(-math.Pow((y-centreY)/half, 2)) * (0.15 + 0.85*amp)
			out[i] = p.pal.at(d*0.4 + t*0.02 + s.Beat*0.1).scale(level(clamp01(k)))
		}
	}},
	{ID: "peaks", Name: "Пики", UsesPal: true, Music: true, draw: drawPeaks},
	{ID: "static", Name: "Цвет", UsesCol: true, draw: func(t float64, p params, out *frame) {
		for i := range out {
			out[i] = p.color
		}
	}},
}

// band reads the spectrum at f (0 = bass, 1 = treble), between bands for a smooth curve.
func (s *sound) band(f float64) float64 {
	fb := clamp01(f) * (numBands - 1)
	b0 := int(fb)
	b1 := min(b0+1, numBands-1)
	return s.Bands[b0] + (s.Bands[b1]-s.Bands[b0])*(fb-float64(b0))
}

// Peaks keeps a falling cap over each bar, so it needs state between frames. Each
// drawing (the panel, every web preview) owns its own, so they never disturb each other.
type peakTrack struct {
	last float64
	top  [8]float64 // per distance from the centre column, in rows
}

func drawPeaks(t float64, p params, out *frame) {
	s := p.sound()
	ps := p.pk
	if ps == nil {
		ps = &peakTrack{}
	}
	dt := t - ps.last
	if dt < 0 || dt > 0.5 {
		dt = 0
	}
	ps.last = t
	var h [8]float64
	for c := range h {
		h[c] = s.band(float64(c)/centreX) * (gridH + 0.5)
		// the cap jumps up with the bar and falls at 3.5 rows per second
		if h[c] >= ps.top[c] {
			ps.top[c] = h[c]
		} else {
			ps.top[c] = math.Max(h[c], ps.top[c]-3.5*dt)
		}
	}
	for i := range out {
		x, y := layout[i].X, layout[i].Y
		c := int(math.Abs(x - centreX))
		row := gridH - 1 - y // 0 at the bottom
		bar := clamp01(h[c]-row) * 0.75
		capK := math.Exp(-math.Pow(row+0.5-ps.top[c], 2) / 0.35)
		col := p.pal.at(float64(c)/centreX*0.55 + t*0.02)
		k := clamp01(0.05 + bar + capK)
		out[i] = mix(col, rgb{1, 1, 1}, 0.35*capK).scale(level(k))
	}
}

var candleDeep, candleFlame, candleCore = mustHex("#ff2a00"), mustHex("#ff8c1a"), mustHex("#ffd27a")

func findEffect(id string) *effect {
	for _, e := range effects {
		if e.ID == id {
			return e
		}
	}
	return nil
}
