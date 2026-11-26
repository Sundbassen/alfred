# Alfred

A local-first voice assistant written in Go. Say "hey alfred, what time is it" and Alfred answers out loud. Wake word detection, speech-to-text and command matching all run on your own machine.

> **Status: early.** A few commands so far (the time, timers/alarms/reminders, Spotify), Linux only, and you train your own wake word model (see [Wake word model](#wake-word-model)). The roadmap lives in the [issues](https://github.com/Sundbassen/alfred/issues), starting with [#2](https://github.com/Sundbassen/alfred/issues/2).

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

When Alfred asks something back ("You have 2 timers. Which one?", "Should I create it?"), it listens for the answer for a few seconds without the wake word. Unrecognized requests get "Sorry, I didn't catch that." A cloud LLM fallback for freeform questions is planned ([#2](https://github.com/Sundbassen/alfred/issues/2)).

## Requirements

- Linux (the mic goes through [miniaudio](https://miniaud.io/) via malgo; developed on Manjaro with PipeWire)
- Go ≥ 1.25, a C/C++ toolchain, cmake, git, curl
- `espeak-ng` for spoken replies (or run with `-tts none`)
- `paplay` (PulseAudio or PipeWire's pulse utilities; `libpulse` on Arch) for the timer chime; without it timers only speak
- `wpctl` (WirePlumber) for the volume commands (or point `ALFRED_WPCTL` at it); without it they're left out
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

## Local intent model (optional)

The phrase templates only understand phrasings they list. A small local model catches the rest ("could you throw on some queen", "turn the music off"). It's tried only when the templates miss, and runs on your machine via llama.cpp:

```sh
cd backend
make install                 # also builds llama.cpp's llama-server into .deps/
./download-model.sh --llm    # Ministral 3 3B Instruct, Q4_K_M (~2.1 GB, Apache-2.0)
make run
```

Alfred starts `llama-server` as a child process, loads and warms the model in the background, and stops it on exit. Until the model is warm, misses get "Sorry, I didn't catch that." Without the model file the tier is left out.

Settings: `ALFRED_INTENT_MODEL_FILE`, `ALFRED_INTENT_MODEL_THREADS` (default 4), and `ALFRED_INTENT_MODEL_TIMEOUT` (default 3s; slower answers fall through).

On a laptop CPU, Ministral 3 3B answers in ~2–3 s, so it's a fallback, not a fast path. Model choice is tracked in [#26](https://github.com/Sundbassen/alfred/issues/26).

## Everyday commands

- "what time is it", "what's the date", "what day is it"
- "never mind" (Alfred stays quiet), "repeat that", "what can you do"
- "volume up", "quieter", "set the volume to 4", "mute", "unmute": the speaker's output (PipeWire's default sink, through `wpctl`), which covers Alfred's voice and the music together

## Timers, alarms and reminders

One command, phrased three ways. What you say decides which it is:

| Say | It is | When it goes off |
|---|---|---|
| "set a timer for 10 minutes", "set a 5 minute timer" | timer | chime + "Your timer for 10 minutes is done." |
| "wake me up at 7 a.m.", "set an alarm for half past 6" | alarm | chime only |
| "remind me to call my mom in 10 minutes", "remind me at 7 to take my pills" | reminder | chime + "Reminder: call your mom." |

It repeats every 10 seconds, for up to 5 minutes, until you say "hey alfred, stop". "How much time is left?" and "what alarms do I have?" list them; "cancel the timer", "cancel my 7 o'clock alarm", "cancel the reminder to call mom" and "cancel all timers" cancel them. "Snooze" (9 minutes, or "snooze for 5 minutes") puts a ringing one off; "add 5 minutes to the timer" extends one. A clock time without am/pm means the next one: at 22:00, "at 7" is 07:00 tomorrow; at 10:00, it's 19:00. Timers are saved in Alfred's local database (`~/.local/state/alfred/alfred.db`, or `ALFRED_DB`), so they survive a restart. One that came due while Alfred was off goes off late if it's less than an hour overdue; older ones are dropped.

## Lists

Lists you create and fill by voice, kept in the local database (`~/.local/state/alfred/alfred.db`) for now; a cloud backend that shares them with your phone is planned ([#39](https://github.com/Sundbassen/alfred/issues/39)).

- "create a list called shopping", "start a packing list"
- "add milk, eggs and bread to the shopping list", "put socks on my packing list"; "add milk to the list" (or "to it") goes to the list you used in the last 5 minutes, or your only list, or Alfred asks which one
- "remove eggs from the shopping list", "what's on my shopping list", "clear the shopping list", "delete the packing list" (Alfred asks before throwing items away), "what lists do I have"
- Adding to a list that doesn't exist: "You don't have a list called camping. Should I create it?"

## Music (Spotify)

The music skill plays Spotify on Alfred's own speaker: "play daft punk", "play a radio with daft punk and justice" (a shuffled mix of only those artists), "pause", "play" (resume), "next song", "go back", "what's playing", "shuffle" / "shuffle off", and a sleep timer: "stop the music in 30 minutes".

Alfred logs in **once** with your own Spotify app (OAuth Authorization Code with PKCE, as in Home Assistant and OpenVoiceOS) and controls playback through the Spotify Web API. It plays on a Spotify Connect receiver running on the same box.

1. **Create a Spotify app** at [developer.spotify.com](https://developer.spotify.com/dashboard): select *Web API*, and add the redirect URI `http://127.0.0.1:8888/callback`. The app owner needs **Spotify Premium** (Development Mode rule since February 2026).
2. **Configure Alfred:**
   ```sh
   export ALFRED_SPOTIFY_CLIENT_ID=<your app's client ID>
   # optional: ALFRED_SPOTIFY_DEVICE (default "Alfred"), ALFRED_SPOTIFY_TOKEN_FILE (default ~/.config/alfred/spotify-token.json)
   ```
3. **Log in once:** `make spotify-login`, open the printed URL and approve. On a headless box, run it on a machine with a browser and copy the token file over.
4. **Make the box a speaker:** run a Spotify Connect receiver named like `ALFRED_SPOTIFY_DEVICE`, e.g. [spotifyd](https://github.com/Spotifyd/spotifyd) (`pacman -S spotifyd`, `device_name = "Alfred"` in `~/.config/spotifyd/spotifyd.conf`). Any Connect receiver works (go-librespot, Spotify Soloist), since Alfred addresses it by name.
5. `make run`, then say "hey alfred, play daft punk".

Without `ALFRED_SPOTIFY_CLIENT_ID`, or without a login, Alfred runs without the music skill and logs why. Spotify removed its recommendations API for new apps, so a "radio" is a mix of the named artists, not a discovery station.

## Project layout

`backend/` has three layers: `cmd/` may import anything, `skills/` may import `internal/`, and `internal/` may import only `internal/` (`make layers` checks this). The full description is in [`.claude/rules/project-patterns.md`](.claude/rules/project-patterns.md).

| Path (under `backend/`) | What's there |
|---|---|
| `cmd/alfred`, `cmd/replaywav`, … | the binaries; development tools are `replaywav`, `reviewrecordings`, `wakestats`, `micdebug` |
| `cmd/internal/wiring` | the composition root: builds the providers and registers every skill (`skills.go`) |
| `skills/<skill>` | what Alfred can do, one package per skill: intent types, handlers, `Register(router, deps...)`. `clock` is the first; providers for a skill go in `skills/<skill>/<skill>service` |
| `internal/assistant` | intents and the router: each intent is a Go type, `assistant.Handle[I]` registers its handler |
| `internal/assistant/match` | the local recognizer: matches transcripts against each intent's example phrases |
| `internal/app` | wires the voice framework and answers transcripts (`Assistant`) |
| `internal/voice` | the voice framework: `listener` (wake word → command), `wakeword`, `stt` (whisper.cpp), `vad`, `mic`, `tts`, `recorder` |
| `internal/platform` | shared helpers: `fault` (error categories), PCM/wav, ONNX Runtime, logging |

Wake word training is in `trainwakeword/`.

## Privacy

Everything runs locally; no audio or text leaves your machine. With `SAVE=1`, voice recordings are written to `backend/.recordings/` (gitignored). Timers, alarms and reminders (including reminder messages) are kept in a local SQLite file, `~/.local/state/alfred/alfred.db` (mode 0600). The planned LLM fallback ([#2](https://github.com/Sundbassen/alfred/issues/2)) will send the *transcript* of unrecognized requests to the Claude API, and only when enabled.

## Third-party components and licenses

Alfred downloads or builds these; none are included in the repository.

| Component | Used for | License |
|---|---|---|
| [whisper.cpp](https://github.com/ggerganov/whisper.cpp) and its Whisper models | speech-to-text | MIT |
| [llama.cpp](https://github.com/ggml-org/llama.cpp) (`llama-server`) | runs the local intent model | MIT |
| [Ministral 3 3B Instruct](https://huggingface.co/mistralai/Ministral-3-3B-Instruct-2512-GGUF) (Mistral AI) | local intent model (optional) | Apache-2.0 |
| [ONNX Runtime](https://github.com/microsoft/onnxruntime) | running the wake word and VAD models | MIT |
| [openWakeWord](https://github.com/dscripka/openWakeWord) | wake word feature models and training pipeline | code: Apache-2.0. Pre-trained models: **CC BY-NC-SA 4.0 (non-commercial)**. Its README doesn't say whether this covers the `melspectrogram.onnx` / `embedding_model.onnx` feature models Alfred downloads, so treat them as non-commercial. |
| [SQLite](https://sqlite.org) via [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) | the local database (timers) | SQLite: public domain; modernc.org/sqlite: BSD-3-Clause |
| [sqlc](https://github.com/sqlc-dev/sqlc) | generates the database query code (development only) | MIT |
| [Silero VAD](https://github.com/snakers4/silero-vad) | optional silence detector | MIT |
| [miniaudio](https://miniaud.io/) via [malgo](https://github.com/gen2brain/malgo) | microphone | miniaudio: public domain / MIT-0; malgo: Unlicense |
| [Piper](https://github.com/rhasspy/piper) | synthetic samples (training only) | GPL-3.0 (piper-tts), not distributed with Alfred |

A wake word model you train yourself is shaped by its training data (openWakeWord's negative features, Piper voices), so check those licenses before redistributing one.

## License

Alfred's own code is [MIT](LICENSE).
