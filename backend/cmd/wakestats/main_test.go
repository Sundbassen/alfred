package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThresholdTable(t *testing.T) {
	pos := []float64{0.012, 0.057, 0.178, 0.244}
	neg := []float64{0.006, 0.031}
	rows := thresholdTable(pos, neg, []float64{0.01, 0.03, 0.1})

	want := []tableRow{
		{threshold: 0.01, detected: 4, falseTriggers: 1},
		{threshold: 0.03, detected: 3, falseTriggers: 1},
		{threshold: 0.1, detected: 2, falseTriggers: 0},
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

func TestScanAndListeningHours(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a_hit_0.244.wav", "b_near_0.012.wav", "c_hit.wav", "notes.txt", "x_error.wav"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clips := scan(dir)
	if len(clips) != 3 {
		t.Fatalf("scan found %d clips, want 3 (hit, near, legacy hit)", len(clips))
	}
	if got := scores(clips); len(got) != 2 {
		t.Errorf("scores = %v, want the 2 scored clips", got)
	}

	sessions := filepath.Join(dir, "sessions.tsv")
	data := "2026-11-23T18:00:00+01:00\t2026-11-23T19:00:00+01:00\t3600.0\n" +
		"2026-11-23T20:00:00+01:00\t2026-11-23T20:30:00+01:00\t1800.0\nbroken line\n"
	if err := os.WriteFile(sessions, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if hours, n := listeningHours(sessions); hours != 1.5 || n != 2 {
		t.Errorf("listeningHours = %v h, %d sessions; want 1.5 h, 2", hours, n)
	}
}
