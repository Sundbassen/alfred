// Package mic captures 16 kHz mono PCM16 audio from the default input device
// (miniaudio via malgo).
package mic

import (
	"context"
	"log/slog"

	"github.com/gen2brain/malgo"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"gitlab.com/tozd/go/errors"
)

// MicrophoneService defines the public interface for microphone operations
type MicrophoneService interface {
	// Start begins capturing audio from the microphone
	Start(ctx context.Context) error

	// Stop stops capturing audio and cleans up resources
	Stop() error

	// AudioChannel returns a channel that receives audio data chunks
	AudioChannel() <-chan []byte
}

// micService is the private implementation of MicrophoneService
type micService struct {
	context      *malgo.AllocatedContext
	device       *malgo.Device
	deviceConfig malgo.DeviceConfig
	audioChan    chan []byte
	isRunning    bool
	logger       *slog.Logger
}

// Config holds configuration for the microphone service
type Config struct {
	SampleRate uint32 // Sample rate in Hz (e.g., 16000 for speech)
	Channels   uint32 // Number of channels (1 for mono, 2 for stereo)
	BufferSize uint32 // Buffer size in frames
}

// DefaultConfig returns a configuration optimized for speech recognition
func DefaultConfig() Config {
	return Config{
		SampleRate: 16000, // 16kHz is standard for STT
		Channels:   1,     // Mono
		BufferSize: 480,   // 30ms at 16kHz
	}
}

// NewMicService creates a new microphone service instance
func NewMicService(config Config, logger *slog.Logger) (MicrophoneService, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, errors.WithMessage(err, "initialize malgo context")
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Duplex)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = config.Channels
	deviceConfig.SampleRate = config.SampleRate
	deviceConfig.Alsa.NoMMap = 1

	service := &micService{
		context:      ctx,
		deviceConfig: deviceConfig,
		audioChan:    make(chan []byte, 100),
		isRunning:    false,
		logger:       logger,
	}

	return service, nil
}

// Start begins capturing audio from the microphone
func (m *micService) Start(ctx context.Context) error {
	if m.isRunning {
		return errors.WithMessage(fault.ErrBadState, "microphone service is already running")
	}

	var captureCallbacks malgo.DeviceCallbacks

	// Callback function that receives audio data
	captureCallbacks.Data = func(pOutputSample, pInputSamples []byte, framecount uint32) {
		select {
		case <-ctx.Done():
			return
		case m.audioChan <- append([]byte{}, pInputSamples...):
			// Successfully sent audio data
		default:
			// Channel full, drop the frame
			m.logger.Warn("Audio channel full, dropping frame")
		}
	}

	device, err := malgo.InitDevice(m.context.Context, m.deviceConfig, captureCallbacks)
	if err != nil {
		return errors.WithMessage(err, "initialize device")
	}

	m.device = device

	if err := m.device.Start(); err != nil {
		m.device.Uninit()
		return errors.WithMessage(err, "start device")
	}

	m.isRunning = true

	// Monitor context cancellation
	go func() {
		<-ctx.Done()
		m.Stop()
	}()

	return nil
}

// Stop stops capturing audio and cleans up resources
func (m *micService) Stop() error {
	if !m.isRunning {
		return nil
	}

	m.isRunning = false

	if m.device != nil {
		m.device.Uninit()
		m.device = nil
	}

	close(m.audioChan)

	if m.context != nil {
		_ = m.context.Uninit()
		m.context.Free()
		m.context = nil
	}

	return nil
}

// AudioChannel returns a channel that receives audio data chunks
func (m *micService) AudioChannel() <-chan []byte {
	return m.audioChan
}
