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
	"encoding/binary"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/sundbassen/alfred/internal/logging"
	"github.com/sundbassen/alfred/internal/pipeline"
)

func main() {
	vad := flag.String("vad", "rms", "silence detector: rms or silero")
	chunkSamples := flag.Int("chunk", 480, "samples per chunk fed to the pipeline")
	speed := flag.Float64("speed", 1, "playback speed multiple of real time")
	tailMs := flag.Int("tail", 2000, "milliseconds of silence appended so the final command is endpointed")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: replaywav [flags] file.wav")
		os.Exit(2)
	}

	logger := logging.New()

	data, err := readWav(flag.Arg(0))
	if err != nil {
		logger.Error("Failed to read wav", slog.Any("error", err))
		os.Exit(1)
	}
	data = append(data, make([]byte, *tailMs*pipeline.SampleRate/1000*2)...)

	service, cleanup, err := pipeline.New(pipeline.Options{VAD: *vad}, logger)
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
		}
	}()

	chunkBytes := *chunkSamples * 2
	interval := time.Duration(float64(*chunkSamples) / pipeline.SampleRate / *speed * float64(time.Second))
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
		float64(len(data))/float64(pipeline.SampleRate*2), transcripts)
}

// readWav returns the PCM data of a 16 kHz mono 16-bit wav file, walking the
// RIFF chunks so files with extra chunks (LIST, fact) are handled.
func readWav(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s: not a RIFF/WAVE file", path)
	}

	var formatOK bool
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := raw[off+8 : min(off+8+size, len(raw))]

		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, fmt.Errorf("%s: short fmt chunk", path)
			}
			channels := binary.LittleEndian.Uint16(body[2:4])
			rate := binary.LittleEndian.Uint32(body[4:8])
			bits := binary.LittleEndian.Uint16(body[14:16])
			if channels != 1 || rate != pipeline.SampleRate || bits != 16 {
				return nil, fmt.Errorf("%s: need 16 kHz mono s16, got %d Hz, %d channels, %d bits",
					path, rate, channels, bits)
			}
			formatOK = true
		case "data":
			if !formatOK {
				return nil, fmt.Errorf("%s: data chunk before fmt chunk", path)
			}
			return body, nil
		}
		off += 8 + size + size%2
	}
	return nil, fmt.Errorf("%s: no data chunk", path)
}
