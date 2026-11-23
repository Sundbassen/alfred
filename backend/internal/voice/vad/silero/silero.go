// Package silero implements voice.SilenceDetector with the Silero VAD
// v5 ONNX model, as an alternative to the RMS detector. A neural VAD tells
// speech from loud non-speech noise (music, typing, fans) that an energy
// threshold cannot.
package silero

import (
	"context"
	"log/slog"

	"github.com/sundbassen/alfred/internal/platform/onnxrt"
	"github.com/sundbassen/alfred/internal/platform/pcm"
	"github.com/sundbassen/alfred/internal/voice"
	ort "github.com/yalue/onnxruntime_go"
	"gitlab.com/tozd/go/errors"
)

// Silero VAD v5 parameters at 16 kHz: the model scores 512-sample windows,
// each prefixed with the last 64 samples of the previous window as context
// (as in the reference OnnxWrapper), and carries a [2, 1, 128] RNN state.
const (
	supportedRate = 16000
	windowSamples = 512
	contextSize   = 64
	stateSize     = 2 * 1 * 128
	// hysteresis: once speech is detected it continues until the
	// probability drops below threshold - hysteresis (Silero's
	// VADIterator uses the same 0.15 margin).
	hysteresis   = 0.15
	statWindowMs = 5000
)

type Config struct {
	ModelPath          string       // Path to silero_vad.onnx (v5)
	OnnxRuntimeLibPath string       // Path to libonnxruntime.so, loaded at runtime
	SampleRate         int          // Must be 16000
	Threshold          float32      // Speech probability threshold (default 0.5)
	Logger             *slog.Logger // Logger instance
}

type detector struct {
	session   *ort.DynamicAdvancedSession
	sr        *ort.Scalar[int64]
	threshold float32
	logger    *slog.Logger

	state    []float32
	context  []float32
	pending  []float32
	speaking bool

	statSamples int
	statWindows int
	statSum     float64
	statMax     float32
	statChunks  int
	statSilent  int
}

func New(config Config) (voice.SilenceDetector, error) {
	if config.SampleRate != supportedRate {
		return nil, errors.Errorf("silero VAD supports %d Hz, got %d", supportedRate, config.SampleRate)
	}
	if err := onnxrt.Init(config.OnnxRuntimeLibPath); err != nil {
		return nil, errors.Errorf("initialize ONNX runtime (%s): %w", config.OnnxRuntimeLibPath, err)
	}

	session, err := ort.NewDynamicAdvancedSession(config.ModelPath,
		[]string{"input", "state", "sr"}, []string{"output", "stateN"}, nil)
	if err != nil {
		return nil, errors.Errorf("load silero VAD model: %w", err)
	}

	sr, err := ort.NewScalar(int64(supportedRate))
	if err != nil {
		session.Destroy()
		return nil, err
	}

	threshold := config.Threshold
	if threshold == 0 {
		threshold = 0.5
	}

	return &detector{
		session:   session,
		sr:        sr,
		threshold: threshold,
		logger:    config.Logger,
		state:     make([]float32, stateSize),
		context:   make([]float32, contextSize),
	}, nil
}

// IsSilent scores every complete 512-sample window the chunk completes and
// classifies the chunk by the highest probability among them. A chunk that
// completes no window keeps the previous classification.
func (d *detector) IsSilent(ctx context.Context, audioData []byte) bool {
	d.pending = append(d.pending, pcm.ToFloat32Normalized(audioData)...)

	var maxProb float32 = -1
	consumed := 0
	for len(d.pending)-consumed >= windowSamples {
		prob, err := d.score(d.pending[consumed : consumed+windowSamples])
		consumed += windowSamples
		if err != nil {
			if d.logger != nil {
				d.logger.Error("Silero VAD inference failed", slog.Any("error", err))
			}
			continue
		}
		maxProb = max(maxProb, prob)
		d.statWindows++
		d.statSum += float64(prob)
		d.statMax = max(d.statMax, prob)
	}
	d.pending = append(d.pending[:0], d.pending[consumed:]...)

	if maxProb >= 0 {
		if d.speaking {
			d.speaking = maxProb >= d.threshold-hysteresis
		} else {
			d.speaking = maxProb >= d.threshold
		}
	}

	d.logStats(len(audioData) / pcm.BytesPerSample)
	return !d.speaking
}

// score runs one window through the model, updating the RNN state and the
// context samples, and returns the speech probability.
func (d *detector) score(window []float32) (float32, error) {
	input := make([]float32, 0, contextSize+windowSamples)
	input = append(append(input, d.context...), window...)

	inputTensor, err := ort.NewTensor(ort.NewShape(1, int64(len(input))), input)
	if err != nil {
		return 0, err
	}
	defer inputTensor.Destroy()

	stateTensor, err := ort.NewTensor(ort.NewShape(2, 1, 128), d.state)
	if err != nil {
		return 0, err
	}
	defer stateTensor.Destroy()

	outputs := []ort.Value{nil, nil}
	if err := d.session.Run([]ort.Value{inputTensor, stateTensor, d.sr}, outputs); err != nil {
		return 0, err
	}
	output := outputs[0].(*ort.Tensor[float32])
	defer output.Destroy()
	stateN := outputs[1].(*ort.Tensor[float32])
	defer stateN.Destroy()

	copy(d.state, stateN.GetData())
	copy(d.context, window[windowSamples-contextSize:])

	return output.GetData()[0], nil
}

// logStats logs speech probability statistics every ~5 s, the tuning signal
// for Threshold.
func (d *detector) logStats(samples int) {
	d.statChunks++
	if !d.speaking {
		d.statSilent++
	}
	d.statSamples += samples
	if d.logger == nil || d.statSamples < statWindowMs*supportedRate/1000 {
		return
	}

	avg := 0.0
	if d.statWindows > 0 {
		avg = d.statSum / float64(d.statWindows)
	}
	d.logger.Info("VAD stats",
		slog.Float64("prob_avg", avg),
		slog.Float64("prob_max", float64(d.statMax)),
		slog.Float64("silent_fraction", float64(d.statSilent)/float64(d.statChunks)),
		slog.Float64("threshold", float64(d.threshold)))

	d.statSamples, d.statWindows, d.statSum, d.statMax = 0, 0, 0, 0
	d.statChunks, d.statSilent = 0, 0
}
