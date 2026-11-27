package hub

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"
	"google.golang.org/protobuf/proto"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/esphome"
	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
	"github.com/sundbassen/alfred/internal/esphome/esphometest"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/voice"
)

// fakeTranscriber returns a scripted transcript.
type fakeTranscriber struct {
	mu   sync.Mutex
	text string
}

func (f *fakeTranscriber) set(text string) { f.mu.Lock(); f.text = text; f.mu.Unlock() }

func (f *fakeTranscriber) Transcribe(context.Context, []byte) (voice.Transcript, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return voice.Transcript{Text: f.text, Confidence: 0.9}, nil
}

// fakeSynth returns a tiny "WAV" naming the text, or err.
type fakeSynth struct{ err error }

func (f fakeSynth) Synthesize(_ context.Context, text string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []byte("RIFF----WAVE " + text), nil
}

// zeroSilence treats all-zero audio as silence.
type zeroSilence struct{}

func (zeroSilence) IsSilent(_ context.Context, chunk []byte) bool {
	return bytes.Count(chunk, []byte{0}) == len(chunk)
}

type timeIntent struct{}

func (timeIntent) IntentName() string { return "time" }

type addIntent struct {
	Item string `slot:"item"`
	List string `slot:"list,optional"`
}

func (addIntent) IntentName() string { return "add" }

type neverMind struct{}

func (neverMind) IntentName() string { return "never_mind" }

func newAssistant() *app.Assistant {
	r := assistant.NewRouter()
	assistant.Handle(r, assistant.Spec{Phrases: []string{"what time is it"}},
		func(context.Context, timeIntent) (assistant.Response, error) {
			return assistant.Response{Text: "It's 10:00."}, nil
		})
	assistant.Handle(r, assistant.Spec{Phrases: []string{"add {item}"}},
		func(_ context.Context, in addIntent) (assistant.Response, error) {
			if in.List == "" {
				return assistant.Response{Text: "Add to which list?", Ask: &assistant.Question{Intent: in, Slot: "list"}}, nil
			}
			return assistant.Response{Text: "Added " + in.Item + " to " + in.List + "."}, nil
		})
	assistant.Handle(r, assistant.Spec{Phrases: []string{"never mind"}},
		func(context.Context, neverMind) (assistant.Response, error) { return assistant.Response{}, nil })
	return app.NewAssistant(r, match.New(r.Intents()))
}

type harness struct {
	t      *testing.T
	hub    *Hub
	device *esphometest.Device
	stt    *fakeTranscriber
	stop   context.CancelFunc
	done   chan error // Run's result
}

func start(t *testing.T) *harness { return startWith(t, fakeSynth{}) }

func startWith(t *testing.T, synth fakeSynth) *harness {
	t.Helper()
	device := esphometest.Start(t, "kitchen", "")
	h, err := New(Config{
		Satellites:         []esphome.Config{{Address: device.Addr()}},
		MediaAddr:          "127.0.0.1:0",
		Synthesizer:        synth,
		NewSilenceDetector: func() (voice.SilenceDetector, error) { return zeroSilence{}, nil },
		Logger:             slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stt := &fakeTranscriber{}
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx, newAssistant(), stt) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run didn't return after cancel")
		}
	})

	hs := &harness{t: t, hub: h, device: device, stt: stt, stop: cancel, done: done}
	select {
	case <-device.Connected():
	case <-time.After(5 * time.Second):
		t.Fatal("the hub didn't connect")
	}
	if m, ok := hs.next().(*esphomepb.SubscribeVoiceAssistantRequest); !ok || !m.Subscribe || m.Flags != 1 {
		t.Fatalf("first message %+v, want a subscription with API audio", m)
	}
	return hs
}

func (h *harness) next() proto.Message {
	h.t.Helper()
	select {
	case m := <-h.device.Received():
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("no message from the hub")
		return nil
	}
}

// events collects pipeline events up to RUN_END.
func (h *harness) events() []*esphomepb.VoiceAssistantEventResponse {
	h.t.Helper()
	var events []*esphomepb.VoiceAssistantEventResponse
	for {
		m, ok := h.next().(*esphomepb.VoiceAssistantEventResponse)
		if !ok {
			continue
		}
		events = append(events, m)
		if m.EventType == esphomepb.VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END {
			return events
		}
	}
}

// command plays a wake word, speech and silence from the device.
func (h *harness) command(speechMs int) {
	h.t.Helper()
	if err := h.device.Send(&esphomepb.VoiceAssistantRequest{Start: true, WakeWordPhrase: "hey alfred"}); err != nil {
		h.t.Fatal(err)
	}
	if m, ok := h.next().(*esphomepb.VoiceAssistantResponse); !ok || m.Port != 0 || m.Error {
		h.t.Fatalf("response %+v, want port 0 (API audio)", m)
	}
	chunk := func(b byte) []byte { return bytes.Repeat([]byte{b}, 1024) } // 32 ms at 16 kHz
	for range speechMs / 32 {
		_ = h.device.Send(&esphomepb.VoiceAssistantAudio{Data: chunk(7)})
	}
	for range 4500 / 32 { // enough silence to end a command, or time out
		_ = h.device.Send(&esphomepb.VoiceAssistantAudio{Data: chunk(0)})
	}
}

// wantEvents checks the event sequence before a test looks into it.
func wantEvents(t *testing.T, events []*esphomepb.VoiceAssistantEventResponse, want ...string) {
	t.Helper()
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func eventTypes(events []*esphomepb.VoiceAssistantEventResponse) []string {
	var names []string
	for _, e := range events {
		names = append(names, e.EventType.String()[len("VOICE_ASSISTANT_"):])
	}
	return names
}

func data(e *esphomepb.VoiceAssistantEventResponse, name string) string {
	for _, d := range e.Data {
		if d.Name == name {
			return d.Value
		}
	}
	return ""
}

func TestCommandRoundTrip(t *testing.T) {
	h := start(t)
	h.stt.set("What time is it?")
	h.command(600)
	events := h.events()
	wantEvents(t, events, "RUN_START", "STT_START", "STT_VAD_END", "STT_END", "INTENT_START", "INTENT_END", "TTS_START", "TTS_END", "RUN_END")
	if text := data(events[3], "text"); text != "What time is it?" {
		t.Errorf("STT_END text %q", text)
	}
	if c := data(events[5], "continue_conversation"); c != "0" {
		t.Errorf("continue_conversation %q, want 0", c)
	}
	if text := data(events[6], "text"); text != "It's 10:00." {
		t.Errorf("TTS_START text %q", text)
	}
	url := data(events[7], "url")
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" || string(body) != "RIFF----WAVE It's 10:00." {
		t.Errorf("GET %s: %d %q %q", url, resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

func TestNoSpeech(t *testing.T) {
	h := start(t)
	h.command(0)
	events := h.events()
	wantEvents(t, events, "RUN_START", "STT_START", "STT_VAD_END", "ERROR", "RUN_END")
	if data(events[3], "code") != "stt-no-text-recognized" {
		t.Errorf("error code %q", data(events[3], "code"))
	}
}

func TestFollowUpContinuesTheConversation(t *testing.T) {
	h := start(t)
	h.stt.set("Add milk.")
	h.command(600)
	events := h.events()
	wantEvents(t, events, "RUN_START", "STT_START", "STT_VAD_END", "STT_END", "INTENT_START", "INTENT_END", "TTS_START", "TTS_END", "RUN_END")
	if c := data(events[5], "continue_conversation"); c != "1" {
		t.Fatalf("continue_conversation %q, want 1 after a question", c)
	}
	// The satellite listens again without the wake word.
	h.stt.set("The shopping list.")
	if err := h.device.Send(&esphomepb.VoiceAssistantRequest{Start: true, ConversationId: "c1"}); err != nil {
		t.Fatal(err)
	}
	h.next() // VoiceAssistantResponse
	chunk := bytes.Repeat([]byte{7}, 1024)
	for range 20 {
		_ = h.device.Send(&esphomepb.VoiceAssistantAudio{Data: chunk})
	}
	_ = h.device.Send(&esphomepb.VoiceAssistantAudio{End: true})
	events = h.events()
	wantEvents(t, events, "RUN_START", "STT_START", "STT_VAD_END", "STT_END", "INTENT_START", "INTENT_END", "TTS_START", "TTS_END", "RUN_END")
	if text := data(events[6], "text"); text != "Added milk to the shopping list." {
		t.Errorf("answer %q (events %v)", text, eventTypes(events))
	}
}

func TestSilentReplySkipsSpeech(t *testing.T) {
	h := start(t)
	h.stt.set("Never mind.")
	h.command(600)
	wantEvents(t, h.events(), "RUN_START", "STT_START", "STT_VAD_END", "STT_END", "INTENT_START", "INTENT_END", "RUN_END")
}

func TestAnnounce(t *testing.T) {
	h := start(t)
	done := make(chan error, 1)
	go func() { done <- h.hub.Speak(context.Background(), "Your timer is done.") }()
	m, ok := h.next().(*esphomepb.VoiceAssistantAnnounceRequest)
	if !ok || m.Text != "Your timer is done." || m.MediaId == "" {
		t.Fatalf("announce %+v", m)
	}
	resp, err := http.Get(m.MediaId)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("announcement audio: %d", resp.StatusCode)
	}
	_ = h.device.Send(&esphomepb.VoiceAssistantAnnounceFinished{Success: true})
	if err := <-done; err != nil {
		t.Errorf("Speak: %v", err)
	}

	go func() { done <- h.hub.Chime(context.Background()) }()
	if m, ok := h.next().(*esphomepb.VoiceAssistantAnnounceRequest); !ok || m.MediaId == "" {
		t.Fatalf("chime %+v", m)
	}
	_ = h.device.Send(&esphomepb.VoiceAssistantAnnounceFinished{Success: true})
	if err := <-done; err != nil {
		t.Errorf("Chime: %v", err)
	}
}

func TestShutdownWhileConnected(t *testing.T) {
	h := start(t)
	h.stop()
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		h.done <- nil // for the cleanup
	case <-time.After(3 * time.Second):
		t.Fatal("Run didn't return while a satellite was connected")
	}
}

func TestSatelliteStopsTheRun(t *testing.T) {
	// The satellite aborts mid-command (VoiceAssistantRequest{Start:
	// false}): the run ends quietly, without an error event.
	h := start(t)
	h.stt.set("What time is it?")
	if err := h.device.Send(&esphomepb.VoiceAssistantRequest{Start: true, WakeWordPhrase: "hey alfred"}); err != nil {
		t.Fatal(err)
	}
	h.next() // VoiceAssistantResponse
	_ = h.device.Send(&esphomepb.VoiceAssistantAudio{Data: bytes.Repeat([]byte{7}, 1024)})
	_ = h.device.Send(&esphomepb.VoiceAssistantRequest{Start: false})
	got := eventTypes(h.events())
	if slices.Contains(got, "ERROR") || got[len(got)-1] != "RUN_END" {
		t.Errorf("events %v, want the run ended without an error", got)
	}
}

func TestTTSFailure(t *testing.T) {
	h := startWith(t, fakeSynth{err: errors.New("espeak broke")})
	h.stt.set("What time is it?")
	h.command(600)
	events := h.events()
	wantEvents(t, events, "RUN_START", "STT_START", "STT_VAD_END", "STT_END", "INTENT_START", "INTENT_END", "TTS_START", "ERROR", "RUN_END")
	if data(events[7], "code") != "tts-failed" {
		t.Errorf("error code %q", data(events[7], "code"))
	}
}

func TestAnnounceConfirmedWithoutSuccess(t *testing.T) {
	// linux-voice-assistant confirms with an empty AnnounceFinished
	// (Success false): that still counts as played.
	h := start(t)
	done := make(chan error, 1)
	go func() { done <- h.hub.Chime(context.Background()) }()
	h.next() // the announce request
	_ = h.device.Send(&esphomepb.VoiceAssistantAnnounceFinished{})
	if err := <-done; err != nil {
		t.Errorf("Chime confirmed like linux-voice-assistant does: %v", err)
	}
}

func TestAnnouncementsTakeTurns(t *testing.T) {
	// Two timers ring at once: the second announcement waits for the
	// first one's confirmation, and each gets its own.
	h := start(t)
	done := make(chan error, 2)
	go func() { done <- h.hub.Speak(context.Background(), "First.") }()
	go func() { done <- h.hub.Speak(context.Background(), "Second.") }()
	first, ok := h.next().(*esphomepb.VoiceAssistantAnnounceRequest)
	if !ok {
		t.Fatal("the first message isn't an announcement")
	}
	select {
	case m := <-h.device.Received():
		t.Fatalf("got %+v before the first announcement was confirmed", m)
	case <-time.After(200 * time.Millisecond):
	}
	_ = h.device.Send(&esphomepb.VoiceAssistantAnnounceFinished{Success: true})
	second, ok := h.next().(*esphomepb.VoiceAssistantAnnounceRequest)
	if !ok {
		t.Fatal("the second message isn't an announcement")
	}
	if first.Text == second.Text {
		t.Errorf("the same announcement twice: %q", first.Text)
	}
	_ = h.device.Send(&esphomepb.VoiceAssistantAnnounceFinished{Success: true})
	for range 2 {
		if err := <-done; err != nil {
			t.Errorf("Speak: %v", err)
		}
	}
}

func TestAnnounceWithoutSatellites(t *testing.T) {
	h, err := New(Config{MediaAddr: "127.0.0.1:0", Synthesizer: fakeSynth{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Chime(context.Background()); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("Chime with no satellite: %v, want ErrServiceUnavailable", err)
	}
}

func TestAnnounceSatelliteDisconnects(t *testing.T) {
	// The satellite drops while an announcement waits: it returns at once,
	// not after the 30 s confirmation timeout.
	h := start(t)
	done := make(chan error, 1)
	go func() { done <- h.hub.Chime(context.Background()) }()
	h.next() // the announce request
	h.device.Drop()
	select {
	case err := <-done:
		if !errors.Is(err, fault.ErrServiceUnavailable) {
			t.Errorf("Chime on a satellite that dropped: %v, want ErrServiceUnavailable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Chime still waiting after the satellite disconnected")
	}
}
