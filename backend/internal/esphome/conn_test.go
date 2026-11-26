package esphome_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"
	"google.golang.org/protobuf/proto"

	"github.com/sundbassen/alfred/internal/esphome"
	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
	"github.com/sundbassen/alfred/internal/esphome/esphometest"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Base64 32-byte keys.
const (
	key      = "px7tsbK3C7bpXHr2OevEV2ZMg/FXNUw4ZJUmhRZhFHo="
	otherKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
)

func dial(t *testing.T, addr, key string) *esphome.Conn {
	t.Helper()
	c, err := esphome.Dial(context.Background(), esphome.Config{Address: addr, Key: key, ClientInfo: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func receive(t *testing.T, ch <-chan proto.Message) proto.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no message")
		return nil
	}
}

func TestPlaintextAndEncrypted(t *testing.T) {
	for name, k := range map[string]string{"plaintext": "", "encrypted": key} {
		t.Run(name, func(t *testing.T) {
			d := esphometest.Start(t, "kitchen", k)
			c := dial(t, d.Addr(), k)
			if c.Name != "kitchen" || c.Info.GetMacAddress() != "02:00:00:00:00:01" {
				t.Errorf("name %q, info %+v", c.Name, c.Info)
			}

			// Messages both ways, including large audio frames.
			audio := make([]byte, 4096)
			for i := range audio {
				audio[i] = byte(i)
			}
			if err := c.Send(&esphomepb.SubscribeVoiceAssistantRequest{Subscribe: true, Flags: 1}); err != nil {
				t.Fatal(err)
			}
			if m, ok := receive(t, d.Received()).(*esphomepb.SubscribeVoiceAssistantRequest); !ok || !m.Subscribe || m.Flags != 1 {
				t.Errorf("device got %+v", m)
			}
			if err := d.Send(&esphomepb.VoiceAssistantAudio{Data: audio}); err != nil {
				t.Fatal(err)
			}
			msg, err := c.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if m, ok := msg.(*esphomepb.VoiceAssistantAudio); !ok || len(m.Data) != len(audio) || m.Data[4095] != audio[4095] {
				t.Errorf("client got %T", msg)
			}

			// Pings from the device are answered without reaching Recv.
			if err := d.Send(&esphomepb.PingRequest{}); err != nil {
				t.Fatal(err)
			}
			if err := d.Send(&esphomepb.VoiceAssistantRequest{Start: true, WakeWordPhrase: "hey alfred"}); err != nil {
				t.Fatal(err)
			}
			msg, err = c.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if m, ok := msg.(*esphomepb.VoiceAssistantRequest); !ok || m.WakeWordPhrase != "hey alfred" {
				t.Errorf("client got %T %+v", msg, msg)
			}
		})
	}
}

func TestEncryptionMismatches(t *testing.T) {
	ctx := context.Background()
	encrypted := esphometest.Start(t, "kitchen", key)
	if _, err := esphome.Dial(ctx, esphome.Config{Address: encrypted.Addr(), Key: otherKey}); !errors.Is(err, fault.ErrUnauthorized) {
		t.Errorf("wrong key: %v, want ErrUnauthorized", err)
	}
	if _, err := esphome.Dial(ctx, esphome.Config{Address: encrypted.Addr()}); !errors.Is(err, fault.ErrUnauthorized) {
		t.Errorf("no key for an encrypted device: %v, want ErrUnauthorized", err)
	}
	plain := esphometest.Start(t, "kitchen", "")
	if _, err := esphome.Dial(ctx, esphome.Config{Address: plain.Addr(), Key: key, DialTimeout: time.Second}); err == nil {
		t.Error("a key for a plaintext device connected")
	}
	if _, err := esphome.Dial(ctx, esphome.Config{Address: plain.Addr(), Key: "short"}); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("malformed key: %v, want ErrInvalidValue", err)
	}
	if _, err := esphome.Dial(ctx, esphome.Config{Address: "127.0.0.1:1"}); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("nothing listening: %v, want ErrServiceUnavailable", err)
	}
}

func TestKeepReconnects(t *testing.T) {
	d := esphometest.Start(t, "kitchen", key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessions := make(chan string, 4)
	go esphome.Keep(ctx, esphome.Config{Address: d.Addr(), Key: key}, slog.New(slog.DiscardHandler),
		func(ctx context.Context, c *esphome.Conn) error {
			sessions <- c.Name
			for {
				if _, err := c.Recv(); err != nil {
					return err
				}
			}
		})
	for i := range 2 {
		select {
		case <-sessions:
		case <-time.After(5 * time.Second):
			t.Fatalf("session %d didn't start", i+1)
		}
		<-d.Connected()
		d.Drop() // the device reboots; Keep reconnects
	}
}

func TestMessagesHaveIDs(t *testing.T) {
	// Sanity check that every message the hub uses has an id.
	for _, m := range []proto.Message{
		&esphomepb.HelloRequest{}, &esphomepb.DeviceInfoRequest{}, &esphomepb.SubscribeVoiceAssistantRequest{},
		&esphomepb.VoiceAssistantRequest{}, &esphomepb.VoiceAssistantResponse{}, &esphomepb.VoiceAssistantEventResponse{},
		&esphomepb.VoiceAssistantAudio{}, &esphomepb.VoiceAssistantAnnounceRequest{}, &esphomepb.PingRequest{},
	} {
		d := esphometest.Start(t, "x", "")
		c := dial(t, d.Addr(), "")
		if err := c.Send(m); err != nil {
			t.Errorf("%T: %v", m, err)
		}
	}
}
