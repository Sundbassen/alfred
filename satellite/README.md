# Satellites

Satellites are the devices in each room: they hear "hey alfred", stream the
command to the hub, and play Alfred's replies. They talk to the hub over
**ESPHome's native API** (the hub connects to them, port 6053), so the same hub
code serves both kinds below. See [#54](https://github.com/Sundbassen/alfred/issues/54).

| Satellite | What it runs | Wake word |
|---|---|---|
| **Laptop / Raspberry Pi** (`make laptop`) | The Open Home Foundation's [linux-voice-assistant](https://github.com/OHF-Voice/linux-voice-assistant) (Apache-2.0), pinned to v1.1.18 (by commit) | Alfred's openWakeWord model, exported to TFLite |
| **ESP32 speaker**, e.g. the Home Assistant Voice Preview Edition (coming) | ESPHome firmware with Alfred's configuration | a microWakeWord "hey alfred" (coming) |

## Laptop satellite

Needs Python 3.11+, `mpv` (libmpv), ALSA (libasound) and PipeWire/PulseAudio.
The wake word export uses the training environment (`trainwakeword/pythonvenv`)
and the trained model in `backend/models/alfred/`.

**On a Raspberry Pi:** the training environment (TensorFlow, torch) doesn't
install there. Run `make laptop-model` on the laptop, copy
`satellite/.deps/openwakeword/` to the Pi, and run
`make laptop WAKE_DIR=<that folder>` there.

```sh
cd satellite
make laptop-install   # clones linux-voice-assistant and runs its setup (a .venv in .deps/)
make laptop           # exports hey_alfred.tflite if needed, then runs the satellite
make laptop DEBUG=1   # with its debug log
```

The satellite listens on port 6053 (`PORT=…` to change it) and announces itself
over mDNS as `alfred-laptop` (`NAME=…`). This version talks plaintext; the ESP32
speakers will use ESPHome's encryption.

**Wake word sensitivity:** `linux/hey_alfred.json` sets `probability_cutoff` to
0.03, the threshold Alfred's own detector uses for this model
(`backend/internal/app/app.go`). Tune it the same way (`make wakestats` in
`backend/`).

**Why TFLite:** linux-voice-assistant's wake word library (pyopen-wakeword)
only loads TFLite, and openWakeWord's own ONNX-to-TFLite converter depends on
onnx-tf, which no longer works with current onnx.
`trainwakeword/export-tflite.py` rebuilds the small classifier in TensorFlow
from the ONNX weights and checks that both give the same scores.
