package listener

import (
	"context"
	"log/slog"
	"time"

	"github.com/sundbassen/alfred/internal/platform/pcm"
	"github.com/sundbassen/alfred/internal/voice"
)

// commandConfig holds the command-phase sizing rules in PCM16 bytes.
type commandConfig struct {
	sampleRate      int
	silenceGapBytes int // silence that ends a command
	timeoutBytes    int // no speech this long after activation: give up
	maxBytes        int // forced end of a command
	preRollBytes    int // audio kept from before command speech starts
	minBytes        int // minimum command length before a silence gap may end it

	// lookbackBytes of audio from just before the activation are prepended
	// to the command when speech runs continuously across the activation,
	// so a word begun before a late detection is not clipped.
	lookbackBytes      int
	listenPreRollBytes int     // pre-roll of listening-phase utterances
	hitClipBytes       int     // maximum length of a saved wake word clip
	nearMissThreshold  float32 // listening utterances peaking at or above this (without detection) are near misses
}

// AudioSink receives audio worth keeping (the -save option): wake word
// clips and near misses for training, commands for debugging and accuracy
// measurements. It is called from the audio goroutine, so implementations
// must be quick. The buffers are copies owned by the sink.
type AudioSink interface {
	WakeWord(audio []byte, score float32)
	NearMiss(audio []byte, score float32)
	Command(audio []byte, transcript voice.Transcript)
}

type commandPhase int

const (
	phaseListening commandPhase = iota // feeding the streaming activator
	phaseCommand                       // recording the command after activation
)

// commandController is the command-phase state machine:
//
//	LISTENING: every chunk goes to the streaming activator, which fires
//	           mid-utterance on the wake word, and to a listening
//	           utteranceAssembler that keeps the current utterance (for
//	           the command lookback, wake word clips and near misses).
//	COMMAND:   chunks go to an utteranceAssembler until a silence gap
//	           endpoints the command, which is then transcribed. If no
//	           speech starts within the timeout, the command is abandoned.
//
// Either way it resets the activator and returns to LISTENING. The silence
// detector sees every chunk in both phases so its filter/model state and
// level statistics stay continuous.
//
// Not safe for concurrent use.
type commandController struct {
	activator   voice.WakeWordDetector
	detector    voice.SilenceDetector
	transcriber voice.Transcriber
	sink        AudioSink // nil: save nothing
	config      commandConfig
	logger      *slog.Logger
	emit        func(voice.Transcript)

	phase       commandPhase
	assembler   *utteranceAssembler // command phase
	listener    *utteranceAssembler // listening phase
	listenPeak  float32             // highest wake word score in the current listening utterance
	lookback    []byte              // audio from before the activation, prepended to the command
	firstChunk  bool                // the next chunk is the first of the command phase
	streamBytes int                 // total audio seen, for timing logs
	activatedAt int                 // stream position of the activation
	waitedBytes int                 // audio since activation without command speech
}

func newCommandController(activator voice.WakeWordDetector, detector voice.SilenceDetector,
	transcriber voice.Transcriber, sink AudioSink, config commandConfig, logger *slog.Logger,
	emit func(voice.Transcript),
) *commandController {
	c := &commandController{
		activator:   activator,
		detector:    detector,
		transcriber: transcriber,
		sink:        sink,
		config:      config,
		logger:      logger,
		emit:        emit,
	}
	c.listener = c.newListener()
	return c
}

func (c *commandController) newListener() *utteranceAssembler {
	return newUtteranceAssembler(assemblerConfig{
		preRollBytes:      c.config.listenPreRollBytes,
		silenceGapBytes:   c.config.silenceGapBytes,
		minUtteranceBytes: c.config.minBytes,
		maxUtteranceBytes: c.config.maxBytes,
	})
}

func (c *commandController) seconds(bytes int) float64 {
	return pcm.BytesToSeconds(bytes, c.config.sampleRate)
}

// handle processes one microphone chunk.
func (c *commandController) handle(ctx context.Context, chunk []byte) {
	isSilent := c.detector.IsSilent(ctx, chunk)
	c.streamBytes += len(chunk)

	switch c.phase {
	case phaseListening:
		c.handleListening(ctx, chunk, isSilent)

	case phaseCommand:
		if c.firstChunk {
			c.firstChunk = false
			if isSilent {
				// The speaker paused after the wake word: nothing from
				// before the activation belongs to the command.
				c.lookback = nil
			}
		}

		command, reason := c.assembler.Add(chunk, isSilent)
		switch reason {
		case flushSilence:
			c.finishCommand(ctx, command)
		case flushMaxDuration:
			c.logger.Warn("No silence gap ended the command, forcing its end; "+
				"the silence threshold may be below the ambient noise level",
				slog.Float64("command_seconds", c.seconds(len(command))))
			c.finishCommand(ctx, command)
		default:
			if !c.assembler.Speaking() {
				c.waitedBytes += len(chunk)
				if c.waitedBytes >= c.config.timeoutBytes {
					c.logger.Info("No command followed the wake word, listening again",
						slog.Float64("waited_seconds", c.seconds(c.waitedBytes)))
					c.listen()
				}
			}
		}
	}
}

// handleListening feeds one listening-phase chunk to the listener (first, so
// its utterance includes the activation chunk) and the activator.
func (c *commandController) handleListening(ctx context.Context, chunk []byte, isSilent bool) {
	utterance, reason := c.listener.Add(chunk, isSilent)

	score, activated, err := c.activator.Feed(ctx, chunk)
	if err != nil {
		c.logger.Error("Wake word detection failed", slog.Any("error", err))
	}
	c.listenPeak = max(c.listenPeak, score)

	switch {
	case activated:
		// The wake word is at the end of the current utterance, or of the
		// one this very chunk completed.
		source := c.listener.Pending()
		if reason != flushNone {
			source = utterance
		}
		if c.sink != nil {
			c.sink.WakeWord(tail(source, c.config.hitClipBytes), score)
		}
		c.lookback = nil
		if !isSilent {
			c.lookback = tail(source, c.config.lookbackBytes)
		}
		c.startCommand()

	case reason != flushNone:
		if c.sink != nil && c.listenPeak >= c.config.nearMissThreshold && c.config.nearMissThreshold > 0 {
			c.logger.Info("Wake word near miss", slog.Float64("score", float64(c.listenPeak)))
			c.sink.NearMiss(append([]byte(nil), utterance...), c.listenPeak)
		}
		c.listenPeak = 0

	case !c.listener.Speaking():
		c.listenPeak = 0
	}
}

// tail returns a copy of the last n bytes of data (all of it if shorter),
// or nil if n <= 0.
func tail(data []byte, n int) []byte {
	if n <= 0 || len(data) == 0 {
		return nil
	}
	n -= n % 2 // whole PCM16 samples
	return append([]byte(nil), data[max(0, len(data)-n):]...)
}

func (c *commandController) startCommand() {
	c.phase = phaseCommand
	c.firstChunk = true
	c.activatedAt = c.streamBytes
	c.waitedBytes = 0
	c.assembler = newUtteranceAssembler(assemblerConfig{
		preRollBytes:      c.config.preRollBytes,
		silenceGapBytes:   c.config.silenceGapBytes,
		minUtteranceBytes: c.config.minBytes,
		maxUtteranceBytes: c.config.maxBytes,
	})
	c.logger.Info("Wake word activated, listening for command",
		slog.Float64("activated_at_s", c.seconds(c.activatedAt)))
}

func (c *commandController) finishCommand(ctx context.Context, command []byte) {
	commandEnd := c.streamBytes
	start := time.Now()

	lookbackBytes := len(c.lookback)
	if lookbackBytes > 0 {
		command = append(c.lookback, command...)
		c.lookback = nil
	}

	result, err := c.transcriber.Transcribe(ctx, command)
	if err != nil {
		c.logger.Error("Command transcription failed", slog.Any("error", err))
	} else {
		c.logger.Info("Command endpointed",
			slog.Float64("activated_at_s", c.seconds(c.activatedAt)),
			slog.Float64("command_end_s", c.seconds(commandEnd)),
			slog.Float64("command_seconds", c.seconds(len(command))),
			slog.Float64("lookback_seconds", c.seconds(lookbackBytes)),
			slog.Duration("stt_latency", time.Since(start)))
		transcript := result
		if c.sink != nil {
			c.sink.Command(append([]byte(nil), command...), transcript)
		}
		c.emit(transcript)
	}

	c.listen()
}

func (c *commandController) listen() {
	c.phase = phaseListening
	c.assembler = nil
	c.lookback = nil
	c.listener = c.newListener()
	c.listenPeak = 0
	c.activator.Reset()
}

// drain transcribes a command that was in progress at shutdown.
func (c *commandController) drain(ctx context.Context) {
	if c.phase != phaseCommand || len(c.assembler.Pending()) < c.config.minBytes {
		return
	}
	c.finishCommand(ctx, c.assembler.Pending())
}
