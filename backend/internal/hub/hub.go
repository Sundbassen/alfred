// Package hub is Alfred's hub side of satellites (#54): it connects to each
// satellite over ESPHome's native API (internal/esphome), runs the voice
// pipeline for what they hear (endpointing, speech-to-text, the assistant,
// text-to-speech), and serves the replies' audio for them to play. It also
// announces on them (ringing timers).
package hub

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/esphome"
	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
	"github.com/sundbassen/alfred/internal/voice"
	"github.com/sundbassen/alfred/internal/voice/sound"
	"github.com/sundbassen/alfred/internal/voice/tts"
)

// Config configures the hub.
type Config struct {
	// Satellites are the devices to connect to.
	Satellites []esphome.Config
	// MediaAddr is where the hub serves reply audio (default ":7301"). It
	// must listen on the address satellites reach the hub at: reply URLs
	// use the hub's address on each satellite connection with this port.
	MediaAddr string
	// Synthesizer speaks replies and announcements as WAV.
	Synthesizer tts.Synthesizer
	// NewSilenceDetector makes the silence detector each satellite's
	// endpointing uses (they keep state, so one per satellite).
	NewSilenceDetector func() (voice.SilenceDetector, error)
	Logger             *slog.Logger
}

// Hub runs the satellites' voice pipelines.
type Hub struct {
	cfg    Config
	media  *media
	chime  string // media id of the chime
	logger *slog.Logger

	mu       sync.Mutex
	sessions map[*session]struct{}
}

// New starts listening for media requests (Run serves them). Announce on
// the returned hub (it implements clock.Announcer) once Run is running.
func New(cfg Config) (*Hub, error) {
	if cfg.MediaAddr == "" {
		cfg.MediaAddr = ":7301"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	m, err := newMedia(cfg.MediaAddr)
	if err != nil {
		return nil, err
	}
	h := &Hub{cfg: cfg, media: m, logger: cfg.Logger, sessions: make(map[*session]struct{})}
	h.chime = m.put(sound.Chime(), true)
	return h, nil
}

// Run connects to every satellite (reconnecting as needed) and runs their
// pipelines with assistant answering and transcriber (shared; the hub
// transcribes one command at a time) until ctx ends.
func (h *Hub) Run(ctx context.Context, assistant *app.Assistant, transcriber voice.Transcriber) error {
	transcriber = &lockedTranscriber{t: transcriber}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- h.media.serve() }()

	var wg sync.WaitGroup
	for _, sat := range h.cfg.Satellites {
		wg.Go(func() {
			esphome.Keep(ctx, sat, h.logger, func(ctx context.Context, c *esphome.Conn) error {
				return h.serve(ctx, c, assistant, transcriber)
			})
		})
	}
	h.logger.Info("Hub running", slog.Int("satellites", len(h.cfg.Satellites)), slog.String("media", h.media.ln.Addr().String()))

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc: // the media server failed
	}
	cancel() // Keep closes the connections, so the sessions end
	_ = h.media.close()
	wg.Wait()
	return err
}

// serve runs one satellite connection until it fails.
func (h *Hub) serve(ctx context.Context, c *esphome.Conn, assistant *app.Assistant, transcriber voice.Transcriber) error {
	detector, err := h.cfg.NewSilenceDetector()
	if err != nil {
		return err
	}
	host := "127.0.0.1"
	if addr, ok := c.LocalAddr().(*net.TCPAddr); ok {
		host = addr.IP.String()
	}
	s := &session{
		hub: h, conn: c, host: host, assistant: assistant, transcriber: transcriber, detector: detector,
		logger: h.logger.With(slog.String("satellite", c.Name)), announced: make(chan bool, 1), done: make(chan struct{}),
	}
	if err := c.Send(&esphomepb.SubscribeVoiceAssistantRequest{
		Subscribe: true,
		Flags:     uint32(esphomepb.VoiceAssistantSubscribeFlag_VOICE_ASSISTANT_SUBSCRIBE_API_AUDIO),
	}); err != nil {
		return err
	}
	h.mu.Lock()
	h.sessions[s] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.sessions, s)
		h.mu.Unlock()
		close(s.done)
		s.stopRun()
	}()
	return s.loop(ctx)
}

func (h *Hub) connected() []*session {
	h.mu.Lock()
	defer h.mu.Unlock()
	sessions := make([]*session, 0, len(h.sessions))
	for s := range h.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// lockedTranscriber transcribes one command at a time (whisper on the
// hub's CPU or GPU is the bottleneck anyway).
type lockedTranscriber struct {
	mu sync.Mutex
	t  voice.Transcriber
}

func (l *lockedTranscriber) Transcribe(ctx context.Context, pcm []byte) (voice.Transcript, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, err := l.t.Transcribe(ctx, pcm)
	return t, errors.WithStack(err)
}
