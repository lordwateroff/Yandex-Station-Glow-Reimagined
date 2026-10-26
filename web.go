package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

//go:embed ui/index.html
var indexHTML []byte

var indexGz = func() []byte {
	var b bytes.Buffer
	w, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	w.Write(indexHTML)
	w.Close()
	return b.Bytes()
}()

type catalog struct {
	Effects  []*effect  `json:"effects"`
	Palettes []*palette `json:"palettes"`
}

func serve(addr string, e *engine, fw *firmwareWatch) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(indexGz)
			return
		}
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, catalog{effects, palettes})
	})
	mux.HandleFunc("GET /api/audio", func(w http.ResponseWriter, r *http.Request) {
		if e.audio == nil {
			http.Error(w, "audio tap is off", http.StatusNotFound)
			return
		}
		writeJSON(w, e.audio.snapshot())
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, e.settings())
	})
	mux.HandleFunc("POST /api/state", func(w http.ResponseWriter, r *http.Request) {
		s := e.settings()
		// decoding reuses a slice's array: give the body its own, or it would rewrite the live settings
		s.Custom = append([]string(nil), s.Custom...)
		// partial update: only the fields present in the body change
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, e.update(s))
	})
	// live mirror of the panel: one line per frame, 88 sRGB colours + whether Alice has the panel
	mux.HandleFunc("GET /api/live", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no streaming", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		t := time.NewTicker(time.Second / 25)
		defer t.Stop()
		var sb strings.Builder
		for {
			select {
			case <-r.Context().Done():
				return
			case <-t.C:
				fr := e.frame()
				sb.Reset()
				sb.WriteString("data: ")
				if fw.busy.Load() {
					sb.WriteByte('A')
				} else {
					sb.WriteByte('-')
				}
				for _, c := range fr {
					sb.WriteString(c.hex())
				}
				sb.WriteString("\n\n")
				if _, err := fmt.Fprint(w, sb.String()); err != nil {
					return
				}
				fl.Flush()
			}
		}
	})
	// thumbnails for the effect list: the real effects, drawn with the current palette,
	// colour, speed and sound, at 10 fps. Only the effects the page asks for (one tab).
	mux.HandleFunc("GET /api/previews", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no streaming", http.StatusInternalServerError)
			return
		}
		type thumb struct {
			fx *effect
			t  float64
			pk peakTrack
		}
		var thumbs []*thumb
		for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
			if fx := findEffect(id); fx != nil && len(thumbs) < len(effects) {
				thumbs = append(thumbs, &thumb{fx: fx, t: float64(len(thumbs)) * 3.7})
			}
		}
		if len(thumbs) == 0 {
			http.Error(w, "ids: no known effects", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		const rate = 10
		t := time.NewTicker(time.Second / rate)
		defer t.Stop()
		var sb strings.Builder
		var fr frame
		for {
			select {
			case <-r.Context().Done():
				return
			case <-t.C:
				pal, col, snd, sp := e.look()
				var sndp *sound
				if e.audio != nil {
					sndp = &snd
				}
				sb.Reset()
				sb.WriteString("data: ")
				for i, th := range thumbs {
					th.t += sp / rate
					th.fx.draw(th.t, params{pal, col, sndp, &th.pk}, &fr)
					if i > 0 {
						sb.WriteByte(';')
					}
					sb.WriteString(th.fx.ID)
					sb.WriteByte('=')
					for _, c := range fr {
						sb.WriteString(c.hex())
					}
				}
				sb.WriteString("\n\n")
				if _, err := fmt.Fprint(w, sb.String()); err != nil {
					return
				}
				fl.Flush()
			}
		}
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
