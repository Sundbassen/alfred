package hub

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/esphome"
	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/voice"
	"github.com/sundbassen/alfred/internal/voice/listener"
)

// runTimeout bounds one voice pipeline run, from the wake word to the reply.
const runTimeout = 60 * time.Second

// session is one connected satellite.
type session struct {
	hub         *Hub
	conn        *esphome.Conn
	host        string // the hub's address as the satellite sees it, for media URLs
	assistant   *app.Assistant
	transcriber voice.Transcriber
	detector    voice.SilenceDetector
	logger      *slog.Logger

	mu  sync.Mutex
	run *run // the pipeline run in progress, if any

	announced chan bool // VoiceAssistantAnnounceFinished
}

// run is one voice pipeline run: the satellite heard its wake word (or
// continues a conversation) and streams the command.
type run struct {
	audio  chan []byte // nil chunk: the satellite stopped sending audio
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *session) loop(ctx context.Context) error {
	for {
		msg, err := s.conn.Recv()
		if err != nil {
			return err
		}
		switch m := msg.(type) {
		case *esphomepb.VoiceAssistantRequest:
			if m.Start {
				s.startRun(ctx, m)
			} else {
				s.stopRun()
			}
		case *esphomepb.VoiceAssistantAudio:
			s.mu.Lock()
			r := s.run
			s.mu.Unlock()
			if r == nil {
				continue
			}
			if len(m.Data) > 0 {
				select {
				case r.audio <- append([]byte(nil), m.Data...):
				default: // the run fell behind (transcribing); drop
				}
			}
			if m.End {
				select {
				case r.audio <- nil:
				default:
				}
			}
		case *esphomepb.VoiceAssistantAnnounceFinished:
			select {
			case s.announced <- m.Success:
			default:
			}
		}
	}
}

func (s *session) startRun(ctx context.Context, req *esphomepb.VoiceAssistantRequest) {
	s.stopRun()
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	r := &run{audio: make(chan []byte, 512), cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.run = r
	s.mu.Unlock()
	s.logger.Info("Satellite heard its wake word", slog.String("wake_word", req.WakeWordPhrase),
		slog.Bool("continuing", req.WakeWordPhrase == "" && req.ConversationId != ""))
	// Port 0: the audio comes over this API connection.
	if err := s.conn.Send(&esphomepb.VoiceAssistantResponse{Port: 0}); err != nil {
		cancel()
		return
	}
	go func() {
		defer close(r.done)
		defer cancel()
		s.pipeline(ctx, r, req.ConversationId)
		s.mu.Lock()
		if s.run == r {
			s.run = nil
		}
		s.mu.Unlock()
	}()
}

func (s *session) stopRun() {
	s.mu.Lock()
	r := s.run
	s.run = nil
	s.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}

// event sends a pipeline event (the satellite's LEDs and states follow them).
func (s *session) event(t esphomepb.VoiceAssistantEvent, data ...string) {
	ev := &esphomepb.VoiceAssistantEventResponse{EventType: t}
	for i := 0; i+1 < len(data); i += 2 {
		ev.Data = append(ev.Data, &esphomepb.VoiceAssistantEventData{Name: data[i], Value: data[i+1]})
	}
	if err := s.conn.Send(ev); err != nil {
		s.logger.Debug("Sending event failed", slog.String("event", t.String()), slog.String("error", err.Error()))
	}
}

// pipeline records the command until it ends, transcribes it, answers it
// and hands the satellite the reply's audio URL.
func (s *session) pipeline(ctx context.Context, r *run, conversationID string) {
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START)
	defer s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END)
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_STT_START)

	rec, err := listener.NewRecorder(listener.RecorderConfig{
		SilenceDetector: s.detector, Transcriber: s.transcriber, SilenceMs: app.CommandSilenceMs,
	}, s.logger)
	if err != nil {
		s.fail("stt-failed", err)
		return
	}
	rec.Start()
	var transcript *voice.Transcript
	ended := false
	for !ended {
		select {
		case <-ctx.Done():
			s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END)
			return
		case chunk := <-r.audio:
			if chunk == nil {
				transcript, ended = rec.Finish(ctx), true
				break
			}
			transcript, ended = rec.Feed(ctx, chunk)
		}
	}
	// The command is over: the satellite stops streaming.
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END)
	if transcript == nil || strings.TrimSpace(transcript.Text) == "" {
		s.logger.Info("No command after the wake word")
		s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_ERROR, "code", "stt-no-text-recognized", "message", "No speech")
		return
	}
	text := transcript.Text
	s.logger.Info("📝 Satellite command", slog.String("text", text), slog.Float64("confidence", transcript.Confidence))
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_STT_END, "text", text)

	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_START)
	reply, err := s.assistant.Respond(ctx, text)
	answer, listen := "", false
	switch {
	case errors.Is(err, fault.ErrNotFound):
		answer = app.NoMatchText
	case err != nil:
		s.logger.Error("Command failed", slog.String("error", err.Error()))
		answer = assistant.SpokenText(err)
	default:
		answer, listen = reply.Text, reply.Listen
		s.logger.Info("🗣️ Reply", slog.String("intent", reply.Recognition.Intent),
			slog.String("by", reply.Recognition.By), slog.String("text", answer))
	}
	continueConversation := "0"
	if listen {
		continueConversation = "1"
	}
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END,
		"conversation_id", conversationID, "continue_conversation", continueConversation)
	if answer == "" {
		return // a silent reply ("never mind", "stop")
	}

	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START, "text", answer)
	wav, err := s.hub.cfg.Synthesizer.Synthesize(ctx, answer)
	if err != nil {
		s.fail("tts-failed", err)
		return
	}
	url := s.hub.media.url(s.host, s.hub.media.put(wav, false))
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END, "url", url)
}

func (s *session) fail(code string, err error) {
	s.logger.Error("Voice pipeline failed", slog.String("code", code), slog.String("error", err.Error()))
	s.event(esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_ERROR, "code", code, "message", err.Error())
}

// announce plays a clip on the satellite and waits until it has played (or
// a timeout).
func (s *session) announce(ctx context.Context, mediaID, text string) error {
	select {
	case <-s.announced: // drop a stale confirmation
	default:
	}
	if err := s.conn.Send(&esphomepb.VoiceAssistantAnnounceRequest{
		MediaId: s.hub.media.url(s.host, mediaID), Text: text,
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case <-s.announced:
		return nil
	case <-ctx.Done():
		return errors.WithMessage(fault.ErrTimeout, "the satellite didn't confirm the announcement")
	}
}
