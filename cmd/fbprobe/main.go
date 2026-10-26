// fbprobe measures how fast the Station 2 LED panel (/dev/fb0, 89x1, 32bpp)
// accepts frames written with write(2), and what the driver's frame queue does.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const dev = "/sys/class/graphics/fb0/device"

func readAttr(p string) string {
	b, _ := os.ReadFile(dev + "/" + p)
	return strings.TrimSpace(string(b))
}

func main() {
	n := flag.Int("n", 120, "frames to write")
	fps := flag.Int("fps", 0, "pace writes at this rate (0 = as fast as possible)")
	delay := flag.String("delay", "", "value to store in frame_queue/next_frame_delay first")
	ch := flag.Int("ch", 0, "byte within each 4-byte pixel to drive (0..3)")
	level := flag.Int("level", 40, "peak level of the test pulse (0..255)")
	flag.Parse()

	if *delay != "" {
		if err := os.WriteFile(dev+"/frame_queue/next_frame_delay", []byte(*delay), 0); err != nil {
			fmt.Println("set delay:", err)
		}
	}
	fmt.Println("delay attr:", readAttr("frame_queue/next_frame_delay"))

	f, err := os.OpenFile("/dev/fb0", os.O_WRONLY, 0)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer f.Close()

	spi0 := readAttr("statistics/spi_sync")
	q0 := readAttr("frame_queue/frames_in_queue")
	buf := make([]byte, 89*4)
	var step time.Duration
	if *fps > 0 {
		step = time.Second / time.Duration(*fps)
	}
	start := time.Now()
	var maxWrite time.Duration
	maxQ := q0
	for i := 0; i < *n; i++ {
		for j := range buf {
			buf[j] = 0
		}
		v := byte(*level * (i % 30) / 30)
		for p := 0; p < 88; p++ {
			buf[p*4+*ch] = v
		}
		t := time.Now()
		if _, err := f.WriteAt(buf, 0); err != nil {
			fmt.Println("write:", err)
			break
		}
		if d := time.Since(t); d > maxWrite {
			maxWrite = d
		}
		if q := readAttr("frame_queue/frames_in_queue"); q > maxQ {
			maxQ = q
		}
		if step > 0 {
			if sl := start.Add(time.Duration(i+1) * step).Sub(time.Now()); sl > 0 {
				time.Sleep(sl)
			}
		}
	}
	el := time.Since(start)
	time.Sleep(300 * time.Millisecond)
	spi1 := readAttr("statistics/spi_sync")
	f.WriteAt(make([]byte, 89*4), 0)
	fmt.Printf("wrote %d frames in %v (%.1f/s), slowest write %v\n", *n, el.Round(time.Millisecond), float64(*n)/el.Seconds(), maxWrite)
	fmt.Printf("spi_sync %s -> %s, queue start %s max %s now %s, overflows %s\n",
		spi0, spi1, q0, maxQ, readAttr("frame_queue/frames_in_queue"), readAttr("lp_common/frame_queue_overflows"))
}
