package esphome

import (
	"bufio"
	"encoding/binary"
	"io"

	"github.com/flynn/noise"
	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// maxFrame bounds a frame: ESPHome's 16-bit Noise frame length, and its
// plaintext limit.
const maxFrame = 65535

// codec reads and writes ESPHome API frames: a message type and its
// protobuf payload.
type codec interface {
	write(w io.Writer, typ uint32, payload []byte) error
	read(r *bufio.Reader) (typ uint32, payload []byte, err error)
}

// plaintext frames are: 0x00, varint length, varint type, payload.
type plaintext struct{}

func (plaintext) write(w io.Writer, typ uint32, payload []byte) error {
	frame := make([]byte, 0, 1+2*binary.MaxVarintLen32+len(payload))
	frame = append(frame, 0x00)
	frame = binary.AppendUvarint(frame, uint64(len(payload)))
	frame = binary.AppendUvarint(frame, uint64(typ))
	frame = append(frame, payload...)
	_, err := w.Write(frame)
	return errors.WithStack(err)
}

func (plaintext) read(r *bufio.Reader) (uint32, []byte, error) {
	preamble, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, errors.WithStack(err)
	}
	switch preamble {
	case 0x00:
	case 0x01:
		return 0, nil, errors.WithMessage(fault.ErrUnauthorized, "the device requires encryption: configure its key")
	default:
		return 0, nil, errors.WithMessagef(fault.ErrDecoding, "invalid preamble 0x%02x", preamble)
	}
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, errors.WithStack(err)
	}
	if length > maxFrame {
		return 0, nil, errors.WithMessagef(fault.ErrDecoding, "frame of %d bytes", length)
	}
	typ, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, errors.WithStack(err)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, errors.WithStack(err)
	}
	return uint32(typ), payload, nil
}

// encrypted frames are 0x01, a 16-bit big-endian length and a Noise
// ciphertext of: 16-bit type, 16-bit payload length, payload.
type encrypted struct {
	enc, dec *noise.CipherState
}

func (e encrypted) write(w io.Writer, typ uint32, payload []byte) error {
	if typ > 0xFFFF || len(payload) > maxFrame-4-16 {
		return errors.WithMessagef(fault.ErrInvalidValue, "message type %d of %d bytes is too large", typ, len(payload))
	}
	plain := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint16(plain[0:2], uint16(typ))
	binary.BigEndian.PutUint16(plain[2:4], uint16(len(payload)))
	plain = append(plain, payload...)
	ciphertext, err := e.enc.Encrypt(nil, nil, plain)
	if err != nil {
		return errors.WithStack(err)
	}
	return writeNoiseFrame(w, ciphertext)
}

func (e encrypted) read(r *bufio.Reader) (uint32, []byte, error) {
	frame, err := readNoiseFrame(r)
	if err != nil {
		return 0, nil, err
	}
	plain, err := e.dec.Decrypt(nil, nil, frame)
	if err != nil {
		return 0, nil, errors.WithMessage(fault.ErrDecoding, "decrypting a frame failed")
	}
	if len(plain) < 4 {
		return 0, nil, errors.WithMessagef(fault.ErrDecoding, "decrypted frame of %d bytes", len(plain))
	}
	return uint32(binary.BigEndian.Uint16(plain[0:2])), plain[4:], nil
}

// writeNoiseFrame writes 0x01, the 16-bit length and data.
func writeNoiseFrame(w io.Writer, data []byte) error {
	if len(data) > maxFrame {
		return errors.WithMessagef(fault.ErrInvalidValue, "frame of %d bytes", len(data))
	}
	frame := make([]byte, 3, 3+len(data))
	frame[0] = 0x01
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(data)))
	frame = append(frame, data...)
	_, err := w.Write(frame)
	return errors.WithStack(err)
}

// readNoiseFrame reads one 0x01-prefixed frame and returns its data.
func readNoiseFrame(r *bufio.Reader) ([]byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, errors.WithStack(err)
	}
	switch header[0] {
	case 0x01:
	case 0x00:
		return nil, errors.WithMessage(fault.ErrInvalidValue, "the device uses plaintext: remove its key from the configuration")
	default:
		return nil, errors.WithMessagef(fault.ErrDecoding, "invalid frame marker 0x%02x", header[0])
	}
	data := make([]byte, binary.BigEndian.Uint16(header[1:3]))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, errors.WithStack(err)
	}
	return data, nil
}

// noisePrologue and noiseHello are fixed by ESPHome's Noise handshake.
var (
	noisePrologue = []byte("NoiseAPIInit\x00\x00")
	noiseHello    = []byte{0x01, 0x00, 0x00}
	cipherSuite   = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
)

// handshakeConfig is Noise_NNpsk0_25519_ChaChaPoly_SHA256 with the device's
// pre-shared key.
func handshakeConfig(psk []byte, initiator bool) noise.Config {
	return noise.Config{
		CipherSuite:           cipherSuite,
		Pattern:               noise.HandshakeNN,
		Initiator:             initiator,
		Prologue:              noisePrologue,
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
	}
}

// clientHandshake runs the client side of ESPHome's Noise handshake and
// returns the encrypted codec plus the server's name and MAC address.
func clientHandshake(w io.Writer, r *bufio.Reader, psk []byte) (codec, string, string, error) {
	hs, err := noise.NewHandshakeState(handshakeConfig(psk, true))
	if err != nil {
		return nil, "", "", errors.WithStack(err)
	}
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, "", "", errors.WithStack(err)
	}
	if _, err := w.Write(noiseHello); err != nil {
		return nil, "", "", errors.WithStack(err)
	}
	if err := writeNoiseFrame(w, append([]byte{0x00}, msg...)); err != nil {
		return nil, "", "", err
	}

	// Server hello: the chosen protocol (1), then name and MAC, each
	// NUL-terminated.
	hello, err := readNoiseFrame(r)
	if err != nil {
		return nil, "", "", errors.WithMessage(err, "server hello")
	}
	if len(hello) == 0 || hello[0] != 0x01 {
		return nil, "", "", errors.WithMessage(fault.ErrDecoding, "unexpected server hello")
	}
	fields := splitNUL(hello[1:])
	var name, mac string
	if len(fields) > 0 {
		name = fields[0]
	}
	if len(fields) > 1 {
		mac = fields[1]
	}

	reply, err := readNoiseFrame(r)
	if err != nil {
		return nil, name, mac, errors.WithMessage(err, "handshake")
	}
	if len(reply) == 0 {
		return nil, name, mac, errors.WithMessage(fault.ErrDecoding, "empty handshake reply")
	}
	if reply[0] != 0x00 {
		if string(reply[1:]) == "Handshake MAC failure" {
			return nil, name, mac, errors.WithMessage(fault.ErrUnauthorized, "wrong encryption key")
		}
		return nil, name, mac, errors.WithMessagef(fault.ErrUnauthorized, "handshake failed: %s", reply[1:])
	}
	_, enc, dec, err := hs.ReadMessage(nil, reply[1:])
	if err != nil {
		return nil, name, mac, errors.WithMessage(fault.ErrUnauthorized, "wrong encryption key")
	}
	return encrypted{enc: enc, dec: dec}, name, mac, nil
}

func splitNUL(b []byte) []string {
	var fields []string
	for len(b) > 0 {
		i := 0
		for i < len(b) && b[i] != 0 {
			i++
		}
		if i == len(b) {
			break // not terminated
		}
		fields = append(fields, string(b[:i]))
		b = b[i+1:]
	}
	return fields
}
