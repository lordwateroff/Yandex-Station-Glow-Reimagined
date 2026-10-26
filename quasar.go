package main

import (
	"bufio"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// The firmware (maind) does not draw through the framebuffer we write to, so its
// frames cannot be detected by reading /dev/fb0 back. It does log every panel
// animation it starts:
//
//	Play animations: [ /system/vendor/quasar/ledpatterns/panel//alice_listening.led ] [ …oknix… ] source=onSDKState
//	Play animations: [ spectrogram ] [ …oknix//alice_speaking.led ] source=onSDKState
//	Play animations: [ …panel//alice_listening_out.led idle.led ] [ … ] source=onSDKState
//
// The first bracket is the main panel. Ending in idle.led means the firmware is done.

const maindLog = "/bkupgrade/data/quasar/daemons_logs/maind.log"

// panelBusy reports whether a "Play animations" line hands the panel to the firmware;
// ok is false when the line is not one.
func panelBusy(line string) (busy, ok bool) {
	i := strings.Index(line, "Play animations: [")
	if i < 0 {
		return false, false
	}
	rest := line[i+len("Play animations: ["):]
	j := strings.IndexByte(rest, ']')
	if j < 0 {
		return false, false
	}
	items := strings.Fields(rest[:j])
	if len(items) == 0 {
		return false, true
	}
	return items[len(items)-1] != "idle.led", true
}

type firmwareWatch struct {
	busy  atomic.Bool
	since atomic.Int64 // unix nanos of the last change
}

func (w *firmwareWatch) set(b bool) {
	if w.busy.Swap(b) != b {
		w.since.Store(time.Now().UnixNano())
		log.Printf("firmware panel busy=%v", b)
	}
}

// run follows the log like tail -F: it re-opens the file after rotation and
// reads only what is appended after start-up (except a look at the tail to learn
// the current state).
func (w *firmwareWatch) run() {
	var f *os.File
	var cur os.FileInfo
	var off int64
	var r *bufio.Reader
	partial := ""
	open := func(fromTail bool) {
		if f != nil {
			f.Close()
			f = nil
		}
		nf, err := os.Open(maindLog)
		if err != nil {
			return
		}
		st, _ := nf.Stat()
		cur = st
		off = 0
		if fromTail {
			off = st.Size() - 64*1024
			if off < 0 {
				off = 0
			}
		}
		nf.Seek(off, io.SeekStart)
		f, r, partial = nf, bufio.NewReader(nf), ""
	}
	open(true)
	for {
		if f == nil {
			time.Sleep(time.Second)
			open(false)
			continue
		}
		for {
			s, err := r.ReadString('\n')
			off += int64(len(s))
			if err != nil {
				partial += s
				break
			}
			line := partial + s
			partial = ""
			if b, ok := panelBusy(line); ok {
				w.set(b)
			}
		}
		time.Sleep(50 * time.Millisecond)
		st, err := os.Stat(maindLog)
		if err != nil {
			continue
		}
		if !os.SameFile(cur, st) || st.Size() < off {
			open(false) // rotated: the new file starts from scratch
		}
	}
}
