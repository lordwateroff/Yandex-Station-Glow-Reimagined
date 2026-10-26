package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"math"
	"math/cmplx"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The speaker signal is fed back into the microphone ADC (hardware loopback) so the
// firmware can cancel its own echo. That feedback only flows while someone reads
// hw:0,1, which is why /etc/init.d/S40adc starts a dummy
//
//	arecord -D hw:0,1 -r 48000 -f S16_LE -c 2 -t raw /dev/null
//
// glow takes over that job: it runs the same arecord with stdout piped to itself,
// so the firmware still gets its feedback and we get the music. On exit the dummy
// is put back; if glow dies, the watchdog in S99zglow puts it back.

const (
	dummyPid   = "/var/run/hw_feedback.pid"
	loopStatus = "/proc/asound/card0/pcm1c/sub0/status"
	sampleRate = 48000
	fftSize    = 1024
	hopSize    = sampleRate / fps // one analysis per rendered frame
	numBands   = 16
	bandLowHz  = 45.0
	bandHighHz = 12000.0
)

var arecordArgs = []string{"-q", "-D", "hw:0,1", "-r", "48000", "-f", "S16_LE", "-c", "2", "-t", "raw"}

// sound is what effects see: everything 0..1, already smoothed and auto-gained.
type sound struct {
	Bands  [numBands]float64 `json:"bands"`
	Level  float64           `json:"level"`
	Beat   float64           `json:"beat"`   // jumps to 1 on a bass hit, decays
	Active bool              `json:"active"` // music is playing
	DB     float64           `json:"db"`     // raw loudness, for tuning
}

type analyzer struct {
	mu  sync.Mutex
	out sound

	ring   [fftSize]float64
	dcIn   float64 // DC blocker state: the ADC feedback sits on a constant offset
	dcOut  float64
	pos    int
	since  int
	window [fftSize]float64
	edges  [numBands + 1]int

	tilt    [numBands]float64 // dB added per band so typical music reads flat
	peak    float64           // slow peak of the loudest band, dB: one scale for all bands
	bassAvg float64
	clock   float64 // seconds of audio analysed; timing follows the stream, not the wall
	lastHit float64
	heard   float64 // clock when the signal was last above the noise floor
}

func newAnalyzer() *analyzer {
	a := &analyzer{peak: -60, lastHit: -1, heard: -10}
	for i := range a.window {
		a.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}
	for b := 0; b <= numBands; b++ {
		hz := bandLowHz * math.Pow(bandHighHz/bandLowHz, float64(b)/numBands)
		bin := int(math.Round(hz * fftSize / sampleRate))
		if b > 0 && bin <= a.edges[b-1] {
			bin = a.edges[b-1] + 1
		}
		a.edges[b] = bin
	}
	for b := 0; b < numBands; b++ {
		// music falls about 3 dB per octave per FFT bin; lift the treble to match
		fc := bandLowHz * math.Pow(bandHighHz/bandLowHz, (float64(b)+0.5)/numBands)
		a.tilt[b] = 3 * math.Log2(fc/bandLowHz)
	}
	return a
}

func (a *analyzer) snapshot() sound {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.out
}

// feed takes interleaved stereo S16 and runs one analysis every hopSize frames.
func (a *analyzer) feed(buf []byte) {
	for i := 0; i+3 < len(buf); i += 4 {
		l := int16(binary.LittleEndian.Uint16(buf[i:]))
		r := int16(binary.LittleEndian.Uint16(buf[i+2:]))
		x := (float64(l) + float64(r)) / 65536
		a.dcOut = x - a.dcIn + 0.995*a.dcOut // one-pole high-pass, corner ~38 Hz at 48 kHz
		a.dcIn = x
		a.ring[a.pos] = a.dcOut
		a.pos = (a.pos + 1) % fftSize
		if a.since++; a.since >= hopSize {
			a.since = 0
			a.analyse()
		}
	}
}

func (a *analyzer) analyse() {
	var x [fftSize]complex128
	sum := 0.0
	for i := 0; i < fftSize; i++ {
		v := a.ring[(a.pos+i)%fftSize]
		sum += v * v
		x[i] = complex(v*a.window[i], 0)
	}
	fft(x[:])
	rmsDB := 10 * math.Log10(sum/fftSize+1e-12)

	dt := float64(hopSize) / sampleRate
	a.clock += dt
	var db [numBands]float64
	top := -200.0
	for b := 0; b < numBands; b++ {
		e := 0.0
		for k := a.edges[b]; k < a.edges[b+1] && k < fftSize/2; k++ {
			m := cmplx.Abs(x[k])
			e += m * m
		}
		db[b] = 10*math.Log10(e/float64(a.edges[b+1]-a.edges[b])+1e-12) + a.tilt[b]
		top = math.Max(top, db[b])
	}
	// auto-gain: the feedback is taken after the volume control, so loud and quiet
	// listening should look the same. The peak rises at once and sinks slowly.
	if top > a.peak {
		a.peak = top
	} else {
		a.peak = math.Max(a.peak-6*dt, -60) // 6 dB per second
	}
	floor := a.peak - 40
	var raw [numBands]float64
	for b := range raw {
		raw[b] = clamp01((db[b] - floor) / 40)
	}

	active := rmsDB > -72
	if active {
		a.heard = a.clock
	}

	bass := (raw[0] + raw[1] + raw[2]) / 3
	a.mu.Lock()
	defer a.mu.Unlock()
	o := &a.out
	o.DB = rmsDB
	o.Active = active || a.clock-a.heard < 1.5
	for b := range raw {
		v := raw[b]
		if !active {
			v = 0
		}
		// fast attack, soft release: bars jump with the beat and fall gracefully
		if v > o.Bands[b] {
			o.Bands[b] += (v - o.Bands[b]) * 0.6
		} else {
			o.Bands[b] += (v - o.Bands[b]) * (1 - math.Exp(-dt/0.18))
		}
	}
	lvl := 0.0
	for _, v := range o.Bands {
		lvl += v
	}
	o.Level = lvl / numBands

	// beat: bass well above its own recent average, at most ~8 per second
	a.bassAvg += (bass - a.bassAvg) * (1 - math.Exp(-dt/0.6))
	if active && bass > 0.45 && bass > a.bassAvg*1.35 && a.clock-a.lastHit > 0.12 {
		a.lastHit = a.clock
		o.Beat = 1
	} else {
		o.Beat *= math.Exp(-dt / 0.22)
	}
}

// fft is an in-place iterative radix-2 transform; len(x) must be a power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := x[start+k], x[start+k+size/2]*wk
				x[start+k], x[start+k+size/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

// ---- owning hw:0,1 ----

func loopbackOpen() bool {
	b, err := os.ReadFile(loopStatus)
	return err == nil && !strings.HasPrefix(strings.TrimSpace(string(b)), "closed")
}

func killDummy() {
	b, err := os.ReadFile(dummyPid)
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
		if cmd, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); strings.Contains(string(cmd), "arecord") {
			if pr, err := os.FindProcess(pid); err == nil {
				pr.Signal(syscall.SIGTERM)
			}
		}
	}
	for i := 0; i < 40 && loopbackOpen(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
}

// restoreDummy puts the firmware's own reader back, exactly as S40adc starts it.
func restoreDummy() {
	if loopbackOpen() {
		return
	}
	args := append([]string{"-S", "-b", "-m", "-p", dummyPid, "-x", "/usr/bin/arecord", "--"},
		"-D", "hw:0,1", "-r", "48000", "-f", "S16_LE", "-c", "2", "-t", "raw", "/dev/null")
	if err := exec.Command("start-stop-daemon", args...).Run(); err != nil {
		log.Printf("restore loopback dummy: %v", err)
	}
}

type tap struct {
	a    *analyzer
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	cmd  *exec.Cmd
}

// startTap replaces the dummy reader with ours and keeps it running until close.
func startTap(a *analyzer) *tap {
	t := &tap{a: a, stop: make(chan struct{}), done: make(chan struct{})}
	go t.loop()
	return t
}

func (t *tap) loop() {
	defer close(t.done)
	for {
		select {
		case <-t.stop:
			return
		default:
		}
		killDummy()
		cmd := exec.Command("arecord", arecordArgs...)
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			log.Printf("loopback tap: %v", err)
			restoreDummy()
			time.Sleep(2 * time.Second)
			continue
		}
		t.mu.Lock()
		t.cmd = cmd
		t.mu.Unlock()
		log.Printf("loopback tap: reading hw:0,1 (pid %d)", cmd.Process.Pid)
		r := bufio.NewReaderSize(out, 16384)
		buf := make([]byte, 3840) // 20 ms
		for {
			n, err := io.ReadFull(r, buf)
			if n > 0 {
				t.a.feed(buf[:n])
			}
			if err != nil {
				break
			}
		}
		cmd.Wait()
		t.mu.Lock()
		t.cmd = nil
		t.mu.Unlock()
		select {
		case <-t.stop:
			return
		default:
		}
		// arecord died (device busy, xrun abort): never leave the loopback unread
		log.Printf("loopback tap: arecord exited, restarting")
		restoreDummy()
		time.Sleep(time.Second)
	}
}

func (t *tap) close() {
	close(t.stop)
	t.mu.Lock()
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Signal(syscall.SIGTERM)
	}
	t.mu.Unlock()
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
	for i := 0; i < 40 && loopbackOpen(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	restoreDummy()
}
