package listener

import (
	"context"
	"log/slog"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/platform/pcm"
	"github.com/sundbassen/alfred/internal/voice"
)

// Recorder records and transcribes one command from audio that starts after
// an activation elsewhere, e.g. a satellite that heard the wake word itself.
// It has no wake word detection of its own: Start begins a command, Feed
// takes its audio until the command ends (a silence gap, the maximum length,
// or no speech within the timeout). Not safe for concurrent use; one per
// satellite.
type Recorder struct {
	c          *commandController
	transcript *voice.Transcript
}

// RecorderConfig configures a Recorder; zero durations take the listener's
// defaults.
type RecorderConfig struct {
	SampleRate      int                   // default 16000
	SilenceDetector voice.SilenceDetector // required, its own (it keeps state)
	Transcriber     voice.Transcriber     // required
	SilenceMs       int                   // silence that ends a command (default 600)
	TimeoutMs       int                   // no speech this long ends it (default 4000)
	MaxMs           int                   // force-end after this long (default 10000)
	MinMs           int                   // shortest command a silence gap may end (default 300)
}

// NewRecorder returns a recorder.
func NewRecorder(cfg RecorderConfig, logger *slog.Logger) (*Recorder, error) {
	if cfg.SilenceDetector == nil || cfg.Transcriber == nil {
		return nil, errors.WithMessage(fault.ErrInvalidValue, "a recorder needs a silence detector and a transcriber")
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	r := &Recorder{}
	command := commandConfig{
		sampleRate:      cfg.SampleRate,
		silenceGapBytes: pcm.MsToBytes(withDefault(cfg.SilenceMs, 600), cfg.SampleRate),
		timeoutBytes:    pcm.MsToBytes(withDefault(cfg.TimeoutMs, 4000), cfg.SampleRate),
		maxBytes:        pcm.MsToBytes(withDefault(cfg.MaxMs, 10000), cfg.SampleRate),
		preRollBytes:    pcm.MsToBytes(100, cfg.SampleRate),
		minBytes:        pcm.MsToBytes(withDefault(cfg.MinMs, 300), cfg.SampleRate),
		// The listening-phase settings are unused: the recorder never
		// listens for a wake word.
		listenPreRollBytes: pcm.MsToBytes(300, cfg.SampleRate),
	}
	r.c = newCommandController(neverWake{}, cfg.SilenceDetector, cfg.Transcriber, nil, command, logger,
		func(t voice.Transcript) { r.transcript = &t })
	return r, nil
}

// Start begins recording a command.
func (r *Recorder) Start() {
	r.transcript = nil
	r.c.followUp()
}

// Feed takes the next chunk of the command (16-bit mono PCM). It reports
// done once the command has ended, with its transcript, or nil if nobody
// spoke (or transcription failed). Chunks after that are ignored until the
// next Start.
func (r *Recorder) Feed(ctx context.Context, chunk []byte) (transcript *voice.Transcript, done bool) {
	if r.c.phase != phaseCommand {
		return r.transcript, true
	}
	r.c.handle(ctx, chunk)
	if r.c.phase == phaseCommand {
		return nil, false
	}
	return r.transcript, true
}

// Finish ends a command early (the satellite stopped sending audio): what
// was recorded is transcribed if it is long enough.
func (r *Recorder) Finish(ctx context.Context) *voice.Transcript {
	if r.c.phase == phaseCommand {
		r.c.drain(ctx)
		r.c.listen()
	}
	return r.transcript
}

// neverWake is a wake word detector that never fires.
type neverWake struct{}

func (neverWake) Feed(context.Context, []byte) (float32, bool, error) { return 0, false, nil }
func (neverWake) Reset()                                              {}
