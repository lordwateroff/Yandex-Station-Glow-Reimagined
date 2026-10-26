// glow — smooth ambient light for the Yandex Station 2 LED panel, with a web panel.
//
//	glow [-listen :15321] [-state /data/opt/glow/state.json]
//
// Renders at 60 fps into /dev/fb0 and steps aside while Alice uses the panel.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	listen := flag.String("listen", ":15321", "web panel address")
	state := flag.String("state", "/data/opt/glow/state.json", "settings file")
	noTap := flag.Bool("no-audio", false, "do not take over the hw:0,1 loopback (no music effects)")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	out, err := openOutput()
	if err != nil {
		log.Fatalf("open %s: %v", fbDevice, err)
	}
	fw := &firmwareWatch{}
	go fw.run()
	e := newEngine(*state, out, fw)
	var tp *tap
	if !*noTap {
		e.audio = newAnalyzer()
		tp = startTap(e.audio)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { e.run(stop); close(done) }()
	go func() {
		if err := serve(*listen, e, fw); err != nil {
			log.Fatalf("web: %v", err)
		}
	}()
	log.Printf("glow: web panel on %s, settings in %s", *listen, *state)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	close(stop)
	<-done
	if tp != nil {
		tp.close() // hand hw:0,1 back to the firmware's own reader
	}
}
