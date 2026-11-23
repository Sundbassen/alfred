// reviewrecordings plays each wake word recording in .recordings (from
// -record) and .recordings/wakeword (from -save) and asks whether to keep
// it. Kept files move to .recordings/validated (the
// directory to pass to training via --custom-samples); rejected files are
// deleted.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	recordingsDir = ".recordings"
	wakewordDir   = ".recordings/wakeword"
	validatedDir  = ".recordings/validated"
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

	if err := os.MkdirAll(validatedDir, 0o755); err != nil {
		fmt.Println("failed to create validated directory:", err)
		os.Exit(1)
	}

	fmt.Printf("reviewing %d recordings\n", len(files))

	reader := bufio.NewReader(os.Stdin)
	kept, deleted := 0, 0

review:
	for i, file := range files {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(files), filepath.Base(file))
		if err := play(file); err != nil {
			fmt.Println("playback failed:", err)
		}

		for {
			fmt.Print("keep? [y=keep / n=delete / r=replay / q=quit]: ")
			line, err := reader.ReadString('\n')
			if err != nil {
				break review
			}

			switch strings.ToLower(strings.TrimSpace(line)) {
			case "y":
				dest := filepath.Join(validatedDir, filepath.Base(file))
				if err := os.Rename(file, dest); err != nil {
					fmt.Println("failed to move file:", err)
					os.Exit(1)
				}
				kept++
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

	fmt.Printf("\ndone: %d kept (moved to %s), %d deleted, %d left unreviewed\n",
		kept, validatedDir, deleted, len(files)-kept-deleted)
}
