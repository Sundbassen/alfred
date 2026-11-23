package wav

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	data := []byte{1, 0, 2, 0, 0xff, 0x7f, 0, 0x80}
	if err := Write(path, data, 16000); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Read = %v, want %v", got, data)
	}
}

func TestReadRejectsOtherSampleRate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	if err := Write(path, []byte{0, 0}, 22050); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, 16000); err == nil {
		t.Error("Read of a 22050 Hz file at 16000 Hz succeeded, want error")
	}
}
