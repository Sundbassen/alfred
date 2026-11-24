// Package sound plays short sounds, such as the chime of a ringing timer,
// through the default audio device. The chime is generated, so there is no
// asset file to ship (or license).
package sound

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"strings"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/platform/wav"
)

// player plays wav data from stdin on PulseAudio or PipeWire (pipewire-pulse).
const player = "paplay"

// Player plays wav data with paplay.
type Player struct {
	path string
}

// New returns a Player. It is fault.ErrNotFound if paplay is not on the
// PATH (install PulseAudio's or PipeWire's pulse utilities).
func New() (*Player, error) {
	path, err := exec.LookPath(player)
	if err != nil {
		return nil, errors.Prefix(errors.WithMessagef(err, "%s not found", player), fault.ErrNotFound)
	}
	return &Player{path: path}, nil
}

// Play plays wav file bytes and blocks until playback finishes.
func (p *Player) Play(ctx context.Context, data []byte) error {
	cmd := exec.CommandContext(ctx, p.path)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.WithMessagef(err, "%s (%s)", player, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// chimeRate is the chime's sample rate; 16 kHz carries its tones fine.
const chimeRate = 16000

// Chime returns a two-tone ding (a high then a lower bell-like tone) as
// 16 kHz mono 16-bit wav bytes.
func Chime() []byte {
	var pcm []byte
	pcm = appendTone(pcm, 880, 0.35)
	pcm = appendTone(pcm, 660, 0.6)
	return wav.Encode(pcm, chimeRate)
}

// appendTone appends a sine tone with a short attack and an exponential
// decay, plus a quiet octave overtone so it sounds like a bell, not a beep.
func appendTone(pcm []byte, hz, seconds float64) []byte {
	const (
		amplitude = 0.4
		attack    = 0.005 // seconds
		decay     = 5.0   // 1/seconds
	)
	n := int(seconds * chimeRate)
	for i := range n {
		t := float64(i) / chimeRate
		env := math.Exp(-decay * t)
		if t < attack {
			env *= t / attack
		}
		v := math.Sin(2*math.Pi*hz*t) + 0.3*math.Sin(2*math.Pi*2*hz*t)
		sample := int16(amplitude / 1.3 * env * v * math.MaxInt16)
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(sample))
	}
	return pcm
}
