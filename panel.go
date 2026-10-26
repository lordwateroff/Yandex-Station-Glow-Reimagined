package main

import (
	"math"
	"os"
)

// Station 2 LED panel: 88 LEDs behind a diffuser, driven by a Gowin FPGA and exposed
// as /dev/fb0 (89x1, 32 bpp, R G B W per pixel; pixel 88 is not wired).
// The LEDs sit on a 15x13 checkerboard with a hole in the middle where the four
// logo LEDs (84..87) live. Map: hackalice.me/devices/station-2/#led.

const (
	numLEDs   = 88
	fbPixels  = 89
	gridW     = 15
	gridH     = 13
	centreX   = 7.0
	centreY   = 6.0
	fbDevice  = "/dev/fb0"
	frameSize = fbPixels * 4
)

type point struct{ X, Y float64 }

// layout[i] is the grid position of LED i; y grows downwards.
var layout = buildLayout()

func buildLayout() [numLEDs]point {
	var l [numLEDs]point
	i := 0
	row := func(y int, xs ...int) {
		for _, x := range xs {
			l[i] = point{float64(x), float64(y)}
			i++
		}
	}
	odd := []int{1, 3, 5, 7, 9, 11, 13}
	even := []int{0, 2, 4, 6, 8, 10, 12, 14}
	row(0, odd...)
	row(1, even...)
	row(2, odd...)
	row(3, even...)
	row(4, odd...)
	row(5, 0, 2, 12, 14) // 37 38 39 40
	row(6, 1, 13)        // 41 42
	row(7, 0, 2, 12, 14) // 43 44 45 46
	row(8, odd...)
	row(9, even...)
	row(10, odd...)
	row(11, even...)
	row(12, odd...)
	// logo LEDs 84..87
	l[84] = point{8, 7}
	l[85] = point{6, 7}
	l[86] = point{6, 5}
	l[87] = point{8, 5}
	return l
}

// rgb is a colour in linear light, 0..1 per channel (values above 1 are clipped).
type rgb struct{ R, G, B float64 }

func (c rgb) scale(k float64) rgb { return rgb{c.R * k, c.G * k, c.B * k} }
func (c rgb) add(o rgb) rgb       { return rgb{c.R + o.R, c.G + o.G, c.B + o.B} }
func mix(a, b rgb, t float64) rgb {
	return rgb{a.R + (b.R-a.R)*t, a.G + (b.G-a.G)*t, a.B + (b.B-a.B)*t}
}
func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
func (c rgb) clip() rgb         { return rgb{clamp01(c.R), clamp01(c.G), clamp01(c.B)} }
func (c rgb) maxc() float64     { return math.Max(c.R, math.Max(c.G, c.B)) }
func (c rgb) minc() float64     { return math.Min(c.R, math.Min(c.G, c.B)) }

type frame [numLEDs]rgb

// output turns linear frames into framebuffer bytes. The panel PWM is linear in light,
// so linear values go out as they are; the 8-bit steps that would show as banding in
// slow, dim fades are hidden by carrying each channel's rounding error into the next frame.
type output struct {
	f     *os.File
	buf   [frameSize]byte
	err   [numLEDs][4]float64
	white bool
}

func openOutput() (*output, error) {
	f, err := os.OpenFile(fbDevice, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	return &output{f: f, white: true}, nil
}

func (o *output) quant(i, ch int, v float64) byte {
	v = v*255 + o.err[i][ch]
	q := math.Floor(v + 0.5)
	if q < 0 {
		q = 0
	}
	if q > 255 {
		q = 255
	}
	// below one step the error would toggle an LED between 0 and 1 at 30 Hz, which is
	// visible as flicker on a dark panel; drop it instead.
	if q == 0 && v < 0.5 {
		o.err[i][ch] = 0
	} else {
		o.err[i][ch] = v - q
	}
	return byte(q)
}

func (o *output) write(fr *frame) error {
	for i, c := range fr {
		c = c.clip()
		w := 0.0
		if o.white {
			// hand the common part to the white LED: whites and pastels look cleaner
			w = c.minc()
			c = rgb{c.R - w, c.G - w, c.B - w}
		}
		p := o.buf[i*4 : i*4+4]
		p[0] = o.quant(i, 0, c.R)
		p[1] = o.quant(i, 1, c.G)
		p[2] = o.quant(i, 2, c.B)
		p[3] = o.quant(i, 3, w)
	}
	_, err := o.f.WriteAt(o.buf[:], 0)
	return err
}

func (o *output) blank() {
	var zero [frameSize]byte
	o.f.WriteAt(zero[:], 0)
	o.err = [numLEDs][4]float64{}
}
