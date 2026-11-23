// Package recorder saves audio from normal use (the -save option) as wav
// files:
//
//	<dir>/wakeword/<time>_hit_<score>.wav   wake word detections
//	<dir>/wakeword/<time>_near_<score>.wav  near misses (scored high, no detection)
//	<dir>/commands/<time>.wav               command audio as transcribed
//	<dir>/commands/index.tsv                file, confidence, transcript per command
//	<dir>/sessions.tsv                      start, end, seconds per run (listening time)
//
// Wake word clips are reviewed with cmd/reviewrecordings and used for
// training; commands are for replaying (make replay) and measuring
// transcription accuracy.
package recorder

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sundbassen/alfred/internal/platform/wakeclip"
	"github.com/sundbassen/alfred/internal/platform/wav"
	"github.com/sundbassen/alfred/internal/voice"
	"github.com/sundbassen/alfred/internal/voice/listener"
	"gitlab.com/tozd/go/errors"
)

const timeFormat = "20060102-150405.000"

// Recorder implements listener.AudioSink by writing wav files. Write
// errors are logged, never fatal.
type Recorder struct {
	dir        string
	started    time.Time
	wakeDir    string
	commandDir string
	sampleRate int
	logger     *slog.Logger
	now        func() time.Time
}

var _ listener.AudioSink = (*Recorder)(nil)

// New creates the output directories under dir.
func New(dir string, sampleRate int, logger *slog.Logger) (*Recorder, error) {
	r := &Recorder{
		dir:        dir,
		wakeDir:    filepath.Join(dir, "wakeword"),
		commandDir: filepath.Join(dir, "commands"),
		sampleRate: sampleRate,
		logger:     logger,
		now:        time.Now,
	}
	for _, d := range []string{r.wakeDir, r.commandDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, errors.WithMessage(err, "create recordings directory")
		}
	}
	r.started = r.now()
	return r, nil
}

// Close appends this run's listening time to sessions.tsv, the denominator
// for false triggers per hour (cmd/wakestats).
func (r *Recorder) Close() {
	end := r.now()
	line := fmt.Sprintf("%s\t%s\t%.1f\n", r.started.Format(time.RFC3339), end.Format(time.RFC3339),
		end.Sub(r.started).Seconds())
	path := filepath.Join(r.dir, "sessions.tsv")
	if err := appendLine(path, line); err != nil {
		r.logger.Error("Failed to record session", slog.String("path", path), slog.Any("error", err))
	}
}

func (r *Recorder) WakeWord(audio []byte, score float32) {
	r.save(filepath.Join(r.wakeDir, wakeclip.Name(r.now(), wakeclip.Hit, score)), audio)
}

func (r *Recorder) NearMiss(audio []byte, score float32) {
	r.save(filepath.Join(r.wakeDir, wakeclip.Name(r.now(), wakeclip.Near, score)), audio)
}

func (r *Recorder) Command(audio []byte, transcript voice.Transcript) {
	name := r.now().Format(timeFormat) + ".wav"
	if !r.save(filepath.Join(r.commandDir, name), audio) {
		return
	}

	// Tabs and newlines in the transcript would break the TSV row.
	text := strings.Join(strings.Fields(transcript.Text), " ")
	line := fmt.Sprintf("%s\t%.3f\t%s\n", name, transcript.Confidence, text)
	index := filepath.Join(r.commandDir, "index.tsv")
	if err := appendLine(index, line); err != nil {
		r.logger.Error("Failed to update command index", slog.String("path", index), slog.Any("error", err))
	}
}

func (r *Recorder) save(path string, audio []byte) bool {
	if err := wav.Write(path, audio, r.sampleRate); err != nil {
		r.logger.Error("Failed to save recording", slog.String("path", path), slog.Any("error", err))
		return false
	}
	r.logger.Info("💾 Saved", slog.String("path", path),
		slog.Float64("seconds", float64(len(audio))/float64(2*r.sampleRate)))
	return true
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
