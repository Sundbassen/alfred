package esphome

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"net"

	"github.com/flynn/noise"
	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// AcceptConfig is the device side of a connection (see Accept).
type AcceptConfig struct {
	// Key is the API encryption key (base64, 32 bytes); empty for plaintext.
	Key string
	// Name and MAC go into the Noise server hello.
	Name, MAC string
}

// Accept runs the device side of the transport handshake on an accepted
// connection and returns a Conn for exchanging messages as the device. It
// is for tests and tools that stand in for a device; the hub only dials.
// Hello and device info are the caller's to answer.
func Accept(nc net.Conn, cfg AcceptConfig) (*Conn, error) {
	c := &Conn{net: nc, r: bufio.NewReader(nc), codec: plaintext{}, stop: make(chan struct{}), Name: cfg.Name}
	if cfg.Key == "" {
		return c, nil
	}
	psk, err := base64.StdEncoding.DecodeString(cfg.Key)
	if err != nil || len(psk) != 32 {
		return nil, errors.WithMessage(fault.ErrInvalidValue, "the encryption key must be 32 bytes, base64")
	}

	var hello [3]byte
	if _, err := io.ReadFull(c.r, hello[:]); err != nil {
		return nil, errors.WithStack(err)
	}
	if !bytes.Equal(hello[:], noiseHello) {
		// Like a real device: answer with an encrypted-style frame, which a
		// plaintext client reads as "encryption required".
		_ = writeNoiseFrame(nc, append([]byte{0x01}, "Bad indicator byte"...))
		return nil, errors.WithMessage(fault.ErrDecoding, "client did not start a Noise handshake")
	}
	frame, err := readNoiseFrame(c.r)
	if err != nil {
		return nil, err
	}
	if len(frame) == 0 || frame[0] != 0x00 {
		return nil, errors.WithMessage(fault.ErrDecoding, "bad handshake frame")
	}

	serverHello := append([]byte{0x01}, []byte(cfg.Name+"\x00"+cfg.MAC+"\x00")...)
	if err := writeNoiseFrame(nc, serverHello); err != nil {
		return nil, err
	}

	hs, err := noise.NewHandshakeState(handshakeConfig(psk, false))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	if _, _, _, err := hs.ReadMessage(nil, frame[1:]); err != nil {
		_ = writeNoiseFrame(nc, append([]byte{0x01}, "Handshake MAC failure"...))
		return nil, errors.WithMessage(fault.ErrUnauthorized, "client used the wrong key")
	}
	msg, dec, enc, err := hs.WriteMessage(nil, nil) // responder: first state decrypts, second encrypts
	if err != nil {
		return nil, errors.WithStack(err)
	}
	if err := writeNoiseFrame(nc, append([]byte{0x00}, msg...)); err != nil {
		return nil, err
	}
	c.codec = encrypted{enc: enc, dec: dec}
	return c, nil
}
