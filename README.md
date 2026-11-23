# Alfred

A local-first voice assistant written in Go. Say "hey alfred, what time is it" and Alfred answers out loud. Wake word detection, speech-to-text and command matching all run on your own machine.

> **Status: early.** One command so far ("what time is it"), Linux only, and you train your own wake word model (see [Wake word model](#wake-word-model)). The roadmap lives in the [issues](https://github.com/Sundbassen/alfred/issues), starting with [#2](https://github.com/Sundbassen/alfred/issues/2).

## How it works

```
microphone (16 kHz mono)
   │
   ▼
wake word ─── openWakeWord model via ONNX Runtime, streamed in 80 ms steps;
   │          fires mid-sentence, so "hey alfred what time is it" works in one breath
   ▼
endpointing ─ records the command until a 600 ms silence gap (adaptive RMS or Silero VAD)
   │
   ▼
speech-to-text ─ whisper.cpp (base.en), biased toward the known command phrases
   │
   ▼
command matching ─ normalization + token overlap against each command's phrases
   │
   ▼
reply ─────── espeak-ng (a natural voice is planned: #5)
```

Unrecognized requests get "Sorry, I didn't catch that." A cloud LLM fallback for freeform questions is planned ([#2](https://github.com/Sundbassen/alfred/issues/2)).

## Requirements

- Linux (the mic goes through [miniaudio](https://miniaud.io/) via malgo; developed on Manjaro with PipeWire)
- Go ≥ 1.25, a C/C++ toolchain, cmake, git, curl
- `espeak-ng` for spoken replies (or run with `-tts none`)
- `pw-play` (PipeWire) for the review tool
- Python 3.11 and, ideally, an NVIDIA GPU for training the wake word model

## Setup

```sh
cd backend
make install        # builds whisper.cpp (pinned) and fetches ONNX Runtime into .deps/ (once)
./download-model.sh # Whisper base.en, openWakeWord feature models, Silero VAD -> models/
```

Both `.deps/` and `models/` are gitignored. After `make build`, `./alfred` can also be run directly, without `make`.

### Wake word model

Alfred needs a wake word classifier at `backend/models/alfred/hey_alfred.onnx`. No model is published (see [Licenses](#third-party-components-and-licenses)), so you train your own with [`trainwakeword/`](trainwakeword/), a wrapper around the [openWakeWord](https://github.com/dscripka/openWakeWord) training pipeline:

```sh
cd trainwakeword
python3.11 -m venv pythonvenv && . pythonvenv/bin/activate
pip install -r requirements.txt
git clone https://github.com/rhasspy/piper-sample-generator.git
./train-wakeword.py --phrase "hey alfred" --output ./models
cp models/hey_alfred.onnx ../backend/models/alfred/
```

Training generates synthetic samples with Piper, downloads ~3 GB of negative features and augmentation data, and takes hours. Once Alfred is running, you can retrain with your own voice (see [Improving the wake word](#improving-the-wake-word)).

## Usage

```sh
cd backend
make run                  # listen for "hey alfred"
make run SAVE=1           # also save wake word clips and commands to .recordings/
make run V=1              # verbose: audio level / wake word score stats every ~5 s, whisper.cpp output
make replay WAV=file.wav  # run a recorded wav (16 kHz mono s16) through the pipeline
make test                 # tests with the right cgo flags (models optional; tests skip without them)
```

Flags for `./alfred`: `-vad rms|silero` (silence detector), `-tts espeak|none`, `-save`, `-near-miss 0.005` (with `-save`: the score from which an undetected utterance counts as a near miss), `-v` (verbose; the stats are what you tune the silence and wake word thresholds with).

### Improving the wake word

With `make run SAVE=1`, every detection and every near miss is saved to `backend/.recordings/wakeword/`, with its score in the file name.

```sh
make review                # label each clip: y = the wake word, x = not the wake word, n = delete
make wakestats ATTEMPTS=40 # detection rate and false triggers per hour, at each threshold
```

Reviewed clips feed back into training:

```sh
./train-wakeword.py --phrase "hey alfred" --output ./models \
    --custom-samples ../backend/.recordings/validated \
    --custom-negatives ../backend/.recordings/negative
```

The measurement protocol is in [#3](https://github.com/Sundbassen/alfred/issues/3).

## Project layout

`backend/internal/` is split by role:

| Path | What's there |
|---|---|
| `voice/` | the voice framework: audio in → transcript, text → speech |
| `voice/listener` | listening → command state machine, endpointing, lookback |
| `voice/wakeword` | streaming openWakeWord port (melspectrogram → embedding → classifier) |
| `voice/stt` | whisper.cpp transcription |
| `voice/vad` | silence detection (adaptive RMS; `vad/silero`: Silero VAD) |
| `voice/mic`, `voice/tts`, `voice/recorder` | microphone, spoken replies, saving audio |
| `assistant/` | intents and the router: each intent is a Go type, `assistant.Handle[I]` registers its handler, middleware wraps them |
| `assistant/match` | the local recognizer: matches transcripts against each intent's example phrases |
| `commands/` | one package per command, each with a `Register(router, services...)`; `clock` is the first |
| `app/` | wires voice, assistant and commands together; `app.Router()` is the one list of registered commands |
| `platform/` | shared helpers: PCM/wav, ONNX Runtime, logging |

Development tools live in `backend/cmd/` (`replaywav`, `reviewrecordings`, `wakestats`, `micdebug`); training is in `trainwakeword/`.

## Privacy

Everything runs locally; no audio or text leaves your machine. With `SAVE=1`, voice recordings are written to `backend/.recordings/` (gitignored). The planned LLM fallback ([#2](https://github.com/Sundbassen/alfred/issues/2)) will send the *transcript* of unrecognized requests to the Claude API, and only when enabled.

## Third-party components and licenses

Alfred downloads or builds these; none are included in the repository.

| Component | Used for | License |
|---|---|---|
| [whisper.cpp](https://github.com/ggerganov/whisper.cpp) and its Whisper models | speech-to-text | MIT |
| [ONNX Runtime](https://github.com/microsoft/onnxruntime) | running the wake word and VAD models | MIT |
| [openWakeWord](https://github.com/dscripka/openWakeWord) | wake word feature models and training pipeline | code: Apache-2.0. Pre-trained models: **CC BY-NC-SA 4.0 (non-commercial)**. Its README doesn't say whether this covers the `melspectrogram.onnx` / `embedding_model.onnx` feature models Alfred downloads, so treat them as non-commercial. |
| [Silero VAD](https://github.com/snakers4/silero-vad) | optional silence detector | MIT |
| [miniaudio](https://miniaud.io/) via [malgo](https://github.com/gen2brain/malgo) | microphone | miniaudio: public domain / MIT-0; malgo: Unlicense |
| [Piper](https://github.com/rhasspy/piper) | synthetic samples (training only) | GPL-3.0 (piper-tts), not distributed with Alfred |

A wake word model you train yourself is shaped by its training data (openWakeWord's negative features, Piper voices), so check those licenses before redistributing one.

## License

Alfred's own code is [MIT](LICENSE).
