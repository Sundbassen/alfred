package speechtotext

import (
	"context"
	"log/slog"
	"time"

	"github.com/sundbassen/alfred/component/debounce"
	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/pcm"
)

// commandConfig holds the command-phase sizing rules in PCM16 bytes.
type commandConfig struct {
	sampleRate      int
	silenceGapBytes int // silence that ends a command
	timeoutBytes    int // no speech this long after activation: give up
	maxBytes        int // forced end of a command
	preRollBytes    int // audio kept from before command speech starts
	minBytes        int // minimum command length before a silence gap may end it
}

type commandPhase int

const (
	phaseListening commandPhase = iota // feeding the streaming activator
	phaseCommand                       // recording the command after activation
)

// commandController is the command-phase state machine:
//
//	LISTENING: every chunk goes to the streaming activator, which fires
//	           mid-utterance on the wake word.
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
	activator speech.StreamingActivator
	detector  debounce.SilenceDetector
	processor speech.Processor
	config    commandConfig
	logger    *slog.Logger
	emit      func(Transcript)

	phase       commandPhase
	assembler   *utteranceAssembler
	streamBytes int // total audio seen, for timing logs
	activatedAt int // stream position of the activation
	waitedBytes int // audio since activation without command speech
}

func newCommandController(activator speech.StreamingActivator, detector debounce.SilenceDetector,
	processor speech.Processor, config commandConfig, logger *slog.Logger, emit func(Transcript),
) *commandController {
	return &commandController{
		activator: activator,
		detector:  detector,
		processor: processor,
		config:    config,
		logger:    logger,
		emit:      emit,
	}
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
		activated, err := c.activator.Feed(ctx, chunk)
		if err != nil {
			c.logger.Error("Wake word detection failed", slog.Any("error", err))
			return
		}
		if activated {
			c.startCommand()
		}

	case phaseCommand:
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

func (c *commandController) startCommand() {
	c.phase = phaseCommand
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

	result, err := c.processor.Process(ctx, command)
	if err != nil {
		c.logger.Error("Command transcription failed", slog.Any("error", err))
	} else {
		c.logger.Info("Command endpointed",
			slog.Float64("activated_at_s", c.seconds(c.activatedAt)),
			slog.Float64("command_end_s", c.seconds(commandEnd)),
			slog.Float64("command_seconds", c.seconds(len(command))),
			slog.Duration("stt_latency", time.Since(start)))
		c.emit(Transcript{
			Text:       string(result.Text),
			Confidence: float64(result.Confidence),
			IsFinal:    true,
		})
	}

	c.listen()
}

func (c *commandController) listen() {
	c.phase = phaseListening
	c.assembler = nil
	c.activator.Reset()
}

// drain transcribes a command that was in progress at shutdown.
func (c *commandController) drain(ctx context.Context) {
	if c.phase != phaseCommand || len(c.assembler.Pending()) < c.config.minBytes {
		return
	}
	c.finishCommand(ctx, c.assembler.Pending())
}
