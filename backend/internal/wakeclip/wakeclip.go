// Package wakeclip names and parses saved wake word clips:
//
//	<time>_hit_<score>.wav   a detection (-save)
//	<time>_near_<score>.wav  a near miss: scored high, no detection (-save)
//	<time>_hit.wav           files from the removed -record mode (no score)
//	<time>_miss.wav
//
// The score in the name survives review (files are moved, not renamed), so
// cmd/wakestats can relate review labels to scores.
package wakeclip

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const timeFormat = "20060102-150405.000"

// Kind is how a clip was captured.
type Kind string

const (
	Hit  Kind = "hit"
	Near Kind = "near"
	Miss Kind = "miss" // removed -record mode only
)

// Clip is the information in a clip's file name.
type Clip struct {
	Kind     Kind
	Score    float64
	HasScore bool // false for files from the removed -record mode
}

// Name returns the file name for a clip captured at t.
func Name(t time.Time, kind Kind, score float32) string {
	return fmt.Sprintf("%s_%s_%.3f.wav", t.Format(timeFormat), kind, score)
}

// Parse reads a clip's kind and score from its file name (a path is fine).
// ok is false for names that don't follow the scheme.
func Parse(name string) (clip Clip, ok bool) {
	base := strings.TrimSuffix(filepath.Base(name), ".wav")
	parts := strings.Split(base, "_")
	if len(parts) < 2 || len(parts) > 3 {
		return Clip{}, false
	}
	clip.Kind = Kind(parts[1])
	switch clip.Kind {
	case Hit, Near, Miss:
	default:
		return Clip{}, false
	}
	if len(parts) == 3 {
		score, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return Clip{}, false
		}
		clip.Score, clip.HasScore = score, true
	}
	return clip, true
}
