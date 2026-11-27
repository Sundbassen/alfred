package esphome

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/flynn/noise"
	"gitlab.com/tozd/go/errors"
	"google.golang.org/protobuf/proto"

	"github.com/sundbassen/alfred/internal/esphome/esphomepb"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

func TestMessageIDsRoundTrip(t *testing.T) {
	// Every message the hub uses has an id, and the id leads back to it.
	for _, m := range []proto.Message{
		&esphomepb.HelloRequest{}, &esphomepb.DeviceInfoRequest{}, &esphomepb.SubscribeVoiceAssistantRequest{},
		&esphomepb.VoiceAssistantRequest{}, &esphomepb.VoiceAssistantResponse{}, &esphomepb.VoiceAssistantEventResponse{},
		&esphomepb.VoiceAssistantAudio{}, &esphomepb.VoiceAssistantAnnounceRequest{},
		&esphomepb.VoiceAssistantAnnounceFinished{}, &esphomepb.PingRequest{}, &esphomepb.DisconnectRequest{},
	} {
		id, ok := idOf(m)
		if !ok {
			t.Errorf("%T has no id", m)
			continue
		}
		mt, ok := typeOf(id)
		if !ok || mt.Descriptor().FullName() != m.ProtoReflect().Descriptor().FullName() {
			t.Errorf("%T: id %d leads to %v", m, id, mt)
		}
	}
}

func TestPlaintextFrames(t *testing.T) {
	var good bytes.Buffer
	if err := (plaintext{}).write(&good, 7, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := (plaintext{}).read(bufio.NewReader(bytes.NewReader(good.Bytes())))
	if err != nil || typ != 7 || string(payload) != "hello" {
		t.Fatalf("round trip: %d %q %v", typ, payload, err)
	}

	oversize := binary.AppendUvarint([]byte{0x00}, maxFrame+1)
	for _, tc := range []struct {
		name  string
		frame []byte
		want  error // nil: any error
	}{
		{"truncated payload", good.Bytes()[:good.Len()-2], nil},
		{"truncated header", []byte{0x00}, nil},
		{"encrypted device", []byte{0x01, 0x00, 0x00}, fault.ErrUnauthorized},
		{"bad preamble", []byte{0x07, 0x00, 0x00}, fault.ErrDecoding},
		{"oversize", append(oversize, 0x01), fault.ErrDecoding},
	} {
		_, _, err := (plaintext{}).read(bufio.NewReader(bytes.NewReader(tc.frame)))
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
}

// cipherPair returns two cipher states that talk to each other.
func cipherPair(t *testing.T) (enc, dec *noise.CipherState) {
	t.Helper()
	psk := bytes.Repeat([]byte{1}, 32)
	client, _ := noise.NewHandshakeState(handshakeConfig(psk, true))
	server, _ := noise.NewHandshakeState(handshakeConfig(psk, false))
	msg, _, _, err := client.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := server.ReadMessage(nil, msg); err != nil {
		t.Fatal(err)
	}
	reply, _, serverEnc, err := server.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The second state carries the responder's messages to the initiator.
	_, _, clientDec, err := client.ReadMessage(nil, reply)
	if err != nil {
		t.Fatal(err)
	}
	return serverEnc, clientDec
}

func TestEncryptedFrames(t *testing.T) {
	enc, dec := cipherPair(t)
	var buf bytes.Buffer
	if err := (encrypted{enc: enc}).write(&buf, 7, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := (encrypted{dec: dec}).read(bufio.NewReader(&buf))
	if err != nil || typ != 7 || string(payload) != "hello" {
		t.Fatalf("round trip: %d %q %v", typ, payload, err)
	}

	// A length field longer than the decrypted payload is a protocol error.
	enc, dec = cipherPair(t)
	plain := []byte{0x00, 0x07, 0x00, 0x09, 'h', 'i'}
	ciphertext, err := enc.Encrypt(nil, nil, plain)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := writeNoiseFrame(&buf, ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (encrypted{dec: dec}).read(bufio.NewReader(&buf)); !errors.Is(err, fault.ErrDecoding) {
		t.Errorf("bad length: %v, want ErrDecoding", err)
	}

	if err := (encrypted{enc: enc}).write(&buf, 7, make([]byte, maxFrame)); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("too large: %v, want ErrInvalidValue", err)
	}
	if _, err := readNoiseFrame(bufio.NewReader(bytes.NewReader([]byte{0x00, 0x00, 0x00}))); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("plaintext device: %v, want ErrInvalidValue", err)
	}
	if _, err := readNoiseFrame(bufio.NewReader(bytes.NewReader([]byte{0x05, 0x00, 0x00}))); !errors.Is(err, fault.ErrDecoding) {
		t.Errorf("bad marker: %v, want ErrDecoding", err)
	}
}
