// reviewrecordings plays each wake word recording in .recordings (from
// -record) and .recordings/wakeword (from -save) and asks what it is:
//
//	y  the wake word: positive sample -> .recordings/validated
//	   (train-wakeword.py --custom-samples)
//	x  not the wake word: negative sample -> .recordings/negative
//	   (train-wakeword.py --custom-negatives). Anything without the wake
//	   word, including noise and silence; keep false triggers this way
//	n  delete: a damaged or unclear wake word (cut off, drowned out),
//	   which would be harmful as either label, or a private clip
//
// Only use x when sure the wake word is not in the clip: a real wake word
// labeled negative teaches the model to reject it.
//
// Files keep their names, so cmd/wakestats can relate labels to scores.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sundbassen/alfred/internal/wakeclip"
)

const (
	recordingsDir = ".recordings"
	wakewordDir   = ".recordings/wakeword"
	validatedDir  = ".recordings/validated"
	negativeDir   = ".recordings/negative"
)

func play(path string) error {
	cmd := exec.Command("pw-play", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	var files []string
	for _, dir := range []string{recordingsDir, wakewordDir} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.wav"))
		if err != nil {
			fmt.Println("failed to list recordings:", err)
			os.Exit(1)
		}
		files = append(files, matches...)
	}
	sort.Slice(files, func(i, j int) bool { return filepath.Base(files[i]) < filepath.Base(files[j]) })

	if len(files) == 0 {
		fmt.Printf("no recordings to review in %s or %s\n", recordingsDir, wakewordDir)
		return
	}

	for _, dir := range []string{validatedDir, negativeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Println("failed to create directory:", err)
			os.Exit(1)
		}
	}

	fmt.Printf("reviewing %d recordings\n", len(files))
	fmt.Println("y = it's the wake word (positive)")
	fmt.Println("x = not the wake word (negative: false triggers, other speech, noise); only if sure")
	fmt.Println("n = delete (damaged/unclear wake word, or private)   r = replay   q = quit")

	reader := bufio.NewReader(os.Stdin)
	positive, negative, deleted := 0, 0, 0

review:
	for i, file := range files {
		fmt.Printf("\n[%d/%d] %s%s\n", i+1, len(files), filepath.Base(file), describe(file))
		if err := play(file); err != nil {
			fmt.Println("playback failed:", err)
		}

		for {
			fmt.Print("wake word? [y / x / n / r / q]: ")
			line, err := reader.ReadString('\n')
			if err != nil {
				break review
			}

			switch strings.ToLower(strings.TrimSpace(line)) {
			case "y":
				move(file, validatedDir)
				positive++
			case "x":
				move(file, negativeDir)
				negative++
			case "n":
				if err := os.Remove(file); err != nil {
					fmt.Println("failed to delete file:", err)
					os.Exit(1)
				}
				deleted++
			case "r":
				if err := play(file); err != nil {
					fmt.Println("playback failed:", err)
				}
				continue
			case "q":
				break review
			default:
				continue
			}
			break
		}
	}

	fmt.Printf("\ndone: %d positive (%s), %d negative (%s), %d deleted, %d left unreviewed\n",
		positive, validatedDir, negative, negativeDir, deleted, len(files)-positive-negative-deleted)
}

// describe explains a clip's file name, e.g. " (near miss, score 0.012)".
func describe(file string) string {
	clip, ok := wakeclip.Parse(file)
	if !ok {
		return ""
	}
	kind := map[wakeclip.Kind]string{
		wakeclip.Hit:  "detected",
		wakeclip.Near: "near miss",
		wakeclip.Miss: "not detected",
	}[clip.Kind]
	if !clip.HasScore {
		return fmt.Sprintf(" (%s)", kind)
	}
	return fmt.Sprintf(" (%s, score %.3f)", kind, clip.Score)
}

func move(file, dir string) {
	if err := os.Rename(file, filepath.Join(dir, filepath.Base(file))); err != nil {
		fmt.Println("failed to move file:", err)
		os.Exit(1)
	}
}
