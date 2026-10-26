# AGENTS.md: notes for AI coding assistants

Read this before changing or deploying this project. It is written for any coding assistant. Humans should start with `README.md` and `docs/GUIDE.md`.

## What this is

`glow` is a single static Go binary that runs **on a rooted Yandex Station 2** (Linux, arm64). It draws ambient and music-reactive light on the station's 88-LED panel at 60 fps and
serves a web control panel on `:15321`. It replaces the community `ledart` plugin. Rooting and device
access come from [Marker284/yandex-station-extended](https://github.com/Marker284/yandex-station-extended).

## Layout

| file | role |
|---|---|
| `main.go` | flags (`-listen`, `-state`, `-no-audio`), wiring, clean shutdown |
| `engine.go` | `settings` (persisted JSON), the 60 fps loop, cross-fades between effects and palettes, yield-to-Alice fade |
| `effects.go` | the effect catalogue (`effects` slice: `ID`, Russian `Name`, `UsesPal`/`UsesCol`/`Music`, `draw`) |
| `color.go` | linear-light `rgb` helpers, hex parsing, `palettes` |
| `noise.go` | smooth noise used by the ambient effects |
| `panel.go` | LED geometry (`layout`, 15x13 checkerboard) and framebuffer output with error-diffusion dithering and RGB to RGBW split |
| `quasar.go` | `firmwareWatch`: tails `maind.log` to know when the firmware (Alice) is using the panel |
| `audio.go` | reads the speaker loopback `hw:0,1` through `arecord`, FFT into 16 bands, beat/level analysis |
| `web.go` | HTTP API and the embedded UI (`//go:embed ui/index.html`, served gzip) |
| `ui/index.html` | the whole web panel, one file, no build step |
| `deploy/S99zglow` | init script: starts glow, plus a guard loop that restores the dummy `hw:0,1` reader |
| `deploy/S99zyam` | optional init script for `yamd` (YAM plugins, station-airplay) with `--model yandexstation_2` |
| `cmd/fbprobe` | standalone tool that measures how the panel framebuffer accepts frames |
| `glow_test.go` | unit tests; they run on the host and do not need the device |

## Build and test

```sh
go test ./...          # on the host, any OS
go vet ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o build/glow .
```

Go 1.27+, standard library only, no cgo. `build/` is git-ignored. Keep it dependency-free.

## Hardware facts the code relies on

- **Panel:** `/dev/fb0` is 89x1 pixels at 32 bpp, byte order **R G B W**. Pixel 88 is not wired, so
  `numLEDs = 88`. LEDs 84..87 are the four logo LEDs in the centre hole. Map:
  hackalice.me/devices/station-2/#led. The PWM is linear in light, so values are written linear
  (no gamma) and dithered.
- **The firmware does not draw through `/dev/fb0` memory**, so you cannot detect Alice's animations by
  reading the framebuffer back. `quasar.go` parses `Play animations: [ ... ]` lines in
  `/bkupgrade/data/quasar/daemons_logs/maind.log`; when the first bracket ends in `idle.led`, the panel
  is free again.
- **Audio loopback:** the speaker signal is fed back into ADC device `hw:0,1` for echo cancellation,
  and only flows while someone reads it. `/etc/init.d/S40adc` normally starts a dummy
  `arecord -D hw:0,1 -r 48000 -f S16_LE -c 2 -t raw /dev/null` (pid in `/var/run/hw_feedback.pid`).
  glow stops that dummy, runs its own `arecord` piped into itself, and puts the dummy back on exit.
  **The device must never stay unread**, or Alice's echo cancelling degrades. The guard in
  `S99zglow` checks `/proc/asound/card0/pcm1c/sub0/status` and restarts the dummy after a second of
  `closed`. Keep both safety nets when touching `audio.go` or the init script.
- **Do not touch** the panel FPGA driver's `jtag` sysfs nodes.

## HTTP API (`web.go`)

| method and path | what it does |
|---|---|
| `GET /` | the UI |
| `GET /api/catalog` | `{effects, palettes}` |
| `GET /api/state` | current `settings` |
| `POST /api/state` | partial update; only the JSON fields present change. Body limit 4 KB. Returns the sanitised settings |
| `GET /api/live` | SSE, 25 fps: `A` or `-` (Alice owns the panel) followed by 88 six-digit hex colours |
| `GET /api/previews?ids=a,b` | SSE, 10 fps: `id=<88 hex colours>;id=...` thumbnails drawn with the current look |
| `GET /api/audio` | current sound analysis; 404 with `-no-audio` |

`settings` fields: `power`, `effect`, `palette` (`custom` uses `custom`), `color` `#rrggbb`,
`speed` 0..1 (0.5 = 1x, maps to 1/4x..4x), `brightness` 0..1, `yield`, `custom` (2..5 hex colours).
`sanitize()` clamps everything; keep new fields going through it.

## Adding an effect

1. Append an `effect` to `effects` in `effects.go` with a unique lowercase `ID` and a Russian `Name`.
   Set `Music: true` if it reads `p.snd`; it must still draw something sensible when `p.snd` is nil
   (no audio) or silent.
2. Draw in linear light into `out *frame`, using `layout[i]` for positions. Keep `draw` stateless where
   you can (a function of `t`). State that must persist between frames belongs to the drawing, never
   to package globals, because the panel and every preview thumbnail draw the same effect separately:
   follow `drawPeaks`, whose state is `params.pk`, owned by `layerState` in `engine.go` and by `thumb`
   in `web.go`.
3. Add its Russian description to the descriptions map in `ui/index.html` (keyed by `ID`), and a test
   in `glow_test.go` if it has logic.

## Deploying (needs a rooted station reachable by adb or SSH)

Binary at `/data/opt/glow/glow`, init script at `/etc/init.d/S99zglow`, settings at
`/data/opt/glow/state.json`, log at `/tmp/glow.log`. Update: push to `/tmp/glow.new`, move it over the
binary, run `/etc/init.d/S99zglow restart`. Full steps: `docs/GUIDE.md`.

## Rules

- Never commit device-specific data: keys, MAC addresses, serial numbers, `devices.json`, logs.
  The station's hostname in logs contains its serial number.
- User-facing text (UI, effect names, README, guide) is in **Russian**; code comments are in English.
- Shell scripts in `deploy/` must stay LF (`.gitattributes` enforces it) and plain POSIX `sh`: `/bin/sh`
  on the device is BusyBox and there is no bash.
- Do not change the station's network services, OTA settings or Alice configuration from this project.
