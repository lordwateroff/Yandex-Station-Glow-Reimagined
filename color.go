package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSrgb(v float64) float64 {
	v = clamp01(v)
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// parseHex reads "#rrggbb" into linear light.
func parseHex(s string) (rgb, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return rgb{}, fmt.Errorf("colour %q: want #rrggbb", s)
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}, fmt.Errorf("colour %q: %v", s, err)
	}
	return rgb{
		srgbToLinear(float64(n>>16&0xff) / 255),
		srgbToLinear(float64(n>>8&0xff) / 255),
		srgbToLinear(float64(n&0xff) / 255),
	}, nil
}

func mustHex(s string) rgb {
	c, err := parseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

func (c rgb) hex() string {
	c = c.clip()
	return fmt.Sprintf("%02x%02x%02x",
		int(linearToSrgb(c.R)*255+0.5), int(linearToSrgb(c.G)*255+0.5), int(linearToSrgb(c.B)*255+0.5))
}

// OKLab: perceptually even blends, so a fade from blue to orange does not pass through grey mud.
type lab struct{ L, A, B float64 }

func toLab(c rgb) lab {
	l := math.Cbrt(0.4122214708*c.R + 0.5363325363*c.G + 0.0514459929*c.B)
	m := math.Cbrt(0.2119034982*c.R + 0.6806995451*c.G + 0.1073969566*c.B)
	s := math.Cbrt(0.0883024619*c.R + 0.2817188376*c.G + 0.6299787005*c.B)
	return lab{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

func fromLab(c lab) rgb {
	l := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	m := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	s := c.L - 0.0894841775*c.A - 1.2914855480*c.B
	l, m, s = l*l*l, m*m*m, s*s*s
	return rgb{
		+4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s,
	}.clip()
}

// hueColor gives a fully saturated colour of even lightness at hue h (turns, 0..1),
// via OKLCh, so a rainbow has no bright yellow band and no dark blue hole.
func hueColor(h float64) rgb {
	a := 2 * math.Pi * h
	const L, C = 0.72, 0.17
	return fromLab(lab{L, C * math.Cos(a), C * math.Sin(a)})
}

type palette struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Hexes []string `json:"colors"`
	labs  []lab
	cyc   bool // true: sample by hue instead of stops (rainbow)
}

// at samples the palette cyclically: t and t+1 give the same colour, so moving
// patterns never hit a seam.
func (p *palette) at(t float64) rgb {
	t -= math.Floor(t)
	if p.cyc {
		return hueColor(t)
	}
	n := len(p.labs)
	x := t * float64(n)
	i := int(x) % n
	f := smooth(x - math.Floor(x))
	a, b := p.labs[i], p.labs[(i+1)%n]
	return fromLab(lab{a.L + (b.L-a.L)*f, a.A + (b.A-a.A)*f, a.B + (b.B-a.B)*f})
}

func newPalette(id, name string, cyc bool, hexes ...string) *palette {
	p := &palette{ID: id, Name: name, Hexes: hexes, cyc: cyc}
	for _, h := range hexes {
		p.labs = append(p.labs, toLab(mustHex(h)))
	}
	return p
}

var palettes = []*palette{
	newPalette("aurora", "Северное сияние", false, "#00e5a8", "#00a2ff", "#7a3cff", "#00d4c8"),
	newPalette("sunset", "Закат", false, "#ff5e3a", "#ff2f7d", "#a43cff", "#ff9a3c"),
	newPalette("ocean", "Океан", false, "#0047ff", "#00b3ff", "#00e0c6", "#1a2bff"),
	newPalette("ember", "Угли", false, "#ff3d00", "#ff8a00", "#ffb84d", "#ff1e1e"),
	newPalette("sakura", "Сакура", false, "#ff7eb6", "#ffc2dc", "#b48cff", "#ff4f9a"),
	newPalette("forest", "Лес", false, "#2bd96b", "#b6ff3c", "#00b88a", "#e8ff8a"),
	newPalette("rainbow", "Радуга", true, "#ff4040", "#ffd040", "#40ff80", "#40a0ff", "#c040ff"),
}

func findPalette(id string) *palette {
	for _, p := range palettes {
		if p.ID == id {
			return p
		}
	}
	return palettes[0]
}

func smooth(t float64) float64 { return t * t * (3 - 2*t) }
