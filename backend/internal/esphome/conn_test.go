package esphome_test

import (
	"context"
	"log/slog"
	"net"
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
		select {
		case <-d.Connected():
		case <-time.After(5 * time.Second):
			t.Fatalf("device %d didn't see the connection", i+1)
		}
		d.Drop() // the device restarts; Keep reconnects
	}
}

func TestSilentDeviceIsGivenUp(t *testing.T) {
	// A device that drops off Wi-Fi sends nothing, not even ping replies:
	// after 3 keep-alive intervals Recv gives it up.
	d := esphometest.Start(t, "kitchen", "")
	c, err := esphome.Dial(context.Background(), esphome.Config{Address: d.Addr(), KeepAlive: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Pings are answered: the connection stays up past the timeout.
	go func() { time.Sleep(300 * time.Millisecond); d.Mute() }()
	start := time.Now()
	_, err = c.Recv()
	if !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Fatalf("Recv from a silent device: %v, want ErrServiceUnavailable", err)
	}
	if took := time.Since(start); took < 300*time.Millisecond || took > 2*time.Second {
		t.Errorf("gave up after %s, want soon after the device went quiet at 300ms (+150ms)", took)
	}
}

func TestDeviceDisconnects(t *testing.T) {
	d := esphometest.Start(t, "kitchen", "")
	c := dial(t, d.Addr(), "")
	if err := d.Send(&esphomepb.DisconnectRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Recv(); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("after the device's DisconnectRequest: %v, want ErrServiceUnavailable", err)
	}
	var got proto.Message
	select {
	case got = <-d.Received():
	case <-time.After(5 * time.Second):
		t.Fatal("no DisconnectResponse")
	}
	if _, ok := got.(*esphomepb.DisconnectResponse); !ok {
		t.Errorf("device got %T, want a DisconnectResponse (and no DisconnectRequest back)", got)
	}
}

func TestKeepStopsWithItsContext(t *testing.T) {
	// fn blocks in Recv; cancelling ctx closes the connection, so Keep
	// returns.
	d := esphometest.Start(t, "kitchen", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	connected := make(chan struct{}, 1)
	go func() {
		defer close(done)
		esphome.Keep(ctx, esphome.Config{Address: d.Addr()}, slog.New(slog.DiscardHandler), func(ctx context.Context, c *esphome.Conn) error {
			connected <- struct{}{}
			for {
				if _, err := c.Recv(); err != nil {
					return err
				}
			}
		})
	}()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("didn't connect")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Keep didn't return after its context ended")
	}
}

func TestBadKeyIsNotRetried(t *testing.T) {
	d := esphometest.Start(t, "kitchen", key)
	if _, err := esphome.Dial(context.Background(), esphome.Config{Address: d.Addr(), Key: "not base64!"}); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("Dial with a malformed key: %v, want ErrInvalidValue", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		esphome.Keep(context.Background(), esphome.Config{Address: d.Addr(), Key: "short"}, slog.New(slog.DiscardHandler),
			func(context.Context, *esphome.Conn) error { return nil })
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Keep kept retrying a malformed key")
	}
}

func TestDialCancelled(t *testing.T) {
	// A listener that accepts but never answers hello.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	if _, err := esphome.Dial(ctx, esphome.Config{Address: ln.Addr().String()}); err == nil {
		t.Fatal("Dial succeeded against a silent listener")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Dial took %s after cancelling, want it to stop right away", took)
	}
}
