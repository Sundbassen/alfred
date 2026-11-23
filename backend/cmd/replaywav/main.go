// Command replaywav streams a wav file through the same speech pipeline as
// the live app, chunk by chunk at (a multiple of) real-time pace, to verify
// wake word activation and command endpointing repeatably without a mic.
//
//	go run ./cmd/replaywav [-vad rms|silero] [-chunk 480] [-speed 1] file.wav
//
// The input must be 16 kHz mono s16 (e.g. pw-record --rate 16000
// --channels 1 --format s16 file.wav). The pipeline logs activated_at_s and
// command_end_s positions relative to the start of the file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/sundbassen/alfred/cmd/internal/wiring"
	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/logging"
	"github.com/sundbassen/alfred/internal/platform/wav"
	"gitlab.com/tozd/go/errors"
)

func main() {
	vad := flag.String("vad", "rms", "silence detector: rms or silero")
	chunkSamples := flag.Int("chunk", 480, "samples per chunk fed to the pipeline")
	speed := flag.Float64("speed", 1, "playback speed multiple of real time")
	tailMs := flag.Int("tail", 2000, "milliseconds of silence appended so the final command is endpointed")
	save := flag.String("save", "", "save wake word clips, near misses and commands under this directory")
	verbose := flag.Bool("v", false,
		"verbose: debug logs (audio level and wake word score stats every ~5 s) and whisper.cpp output")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: replaywav [flags] file.wav")
		os.Exit(2)
	}

	logger := logging.New(*verbose)

	data, err := wav.Read(flag.Arg(0), app.SampleRate)
	if err != nil {
		logger.Error("Failed to read wav", slog.Any("error", err))
		os.Exit(1)
	}
	data = append(data, make([]byte, *tailMs*app.SampleRate/1000*2)...)

	router := assistant.NewRouter()
	if err := wiring.RegisterSkills(router); err != nil {
		logger.Error("Failed to set up skills", slog.Any("error", err))
		os.Exit(1)
	}
	asst := app.NewAssistant(router)
	service, cleanup, err := app.New(app.Options{VAD: *vad, Prompt: asst.Prompt(), SaveDir: *save, Verbose: *verbose}, logger)
	if err != nil {
		logger.Error("Failed to create speech pipeline", slog.Any("error", err))
		os.Exit(1)
	}
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		logger.Error("Failed to start pipeline", slog.Any("error", err))
		os.Exit(1)
	}

	transcripts := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for transcript := range service.TranscriptChannel() {
			transcripts++
			fmt.Printf("TRANSCRIPT: %q (confidence %.2f)\n", transcript.Text, transcript.Confidence)

			reply, err := asst.Respond(ctx, transcript.Text)
			var noMatch *match.NoMatchError
			switch {
			case errors.As(err, &noMatch) && noMatch.Best.Name != "":
				fmt.Printf("NO MATCH (best: %s %.2f)\n", noMatch.Best.Name, noMatch.Best.Score)
			case errors.As(err, &noMatch):
				fmt.Println("NO MATCH")
			case err != nil:
				fmt.Printf("COMMAND FAILED: %v (says %q)\n", err, assistant.SpokenText(err))
			default:
				fmt.Printf("RESPONSE: [%s %.2f] %q\n", reply.Match.Name, reply.Match.Score, reply.Text)
			}
		}
	}()

	chunkBytes := *chunkSamples * 2
	interval := time.Duration(float64(*chunkSamples) / app.SampleRate / *speed * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for off := 0; off < len(data); off += chunkBytes {
		<-ticker.C
		if err := service.ProcessAudio(data[off:min(off+chunkBytes, len(data))]); err != nil {
			logger.Warn("Chunk dropped", slog.Int("offset_bytes", off), slog.Any("error", err))
		}
	}

	// Allow the last command's transcription to finish.
	time.Sleep(3 * time.Second)
	cancel()
	service.Stop()
	fmt.Printf("replayed %.2f s of audio, %d transcript(s)\n",
		float64(len(data))/float64(app.SampleRate*2), transcripts)
}
