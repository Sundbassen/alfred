// wakestats reports how well the wake word model performs, from the clips
// saved with `make run SAVE=1` and labeled with `make review`:
//
//	.recordings/wakeword   unreviewed hits and near misses
//	.recordings/validated  reviewed: the wake word (positives)
//	.recordings/negative   reviewed: not the wake word (e.g. false triggers)
//	.recordings/sessions.tsv  listening time, for false triggers per hour
//
// Scores come from the file names (internal/wakeclip). Utterances scoring
// below the near-miss bar (default 0.005) are never saved, so missed wake
// words can only be counted fully with -attempts: how many times "hey
// alfred" was said in the measured sessions.
//
//	go run ./cmd/wakestats [-dir .recordings] [-attempts 40] [-threshold 0.03]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/sundbassen/alfred/internal/wakeclip"
)

var thresholds = []float64{0.01, 0.02, 0.03, 0.05, 0.1, 0.2, 0.3}

func main() {
	dir := flag.String("dir", ".recordings", "recordings directory")
	attempts := flag.Int("attempts", 0, `times "hey alfred" was said in the measured sessions (0 = unknown)`)
	threshold := flag.Float64("threshold", 0.03, "the detection threshold currently in use")
	flag.Parse()

	unreviewed := scan(filepath.Join(*dir, "wakeword"))
	positives := scan(filepath.Join(*dir, "validated"))
	negatives := scan(filepath.Join(*dir, "negative"))
	hours, sessions := listeningHours(filepath.Join(*dir, "sessions.tsv"))

	fmt.Println("Unreviewed (.recordings/wakeword)")
	printKind(unreviewed, wakeclip.Hit, "hits")
	printKind(unreviewed, wakeclip.Near, "near misses")

	fmt.Printf("\nListening time: %.2f h over %d session(s)\n", hours, sessions)

	pos, neg := scores(positives), scores(negatives)
	fmt.Printf("\nReviewed: %d positive (%d with a score), %d negative (%d with a score)\n",
		len(positives), len(pos), len(negatives), len(neg))
	if len(pos)+len(neg) == 0 {
		fmt.Println("Nothing reviewed with a score yet: run make review.")
		return
	}

	fmt.Printf("\n%-10s %-22s %-16s %s\n", "threshold", "positives detected", "false triggers", "per hour")
	for _, row := range thresholdTable(pos, neg, thresholds) {
		marker := ""
		if row.threshold == *threshold {
			marker = "  <- current"
		}
		perHour := "-"
		if hours > 0 {
			perHour = fmt.Sprintf("%.2f", float64(row.falseTriggers)/hours)
		}
		fmt.Printf("%-10.2f %-22s %-16d %s%s\n", row.threshold,
			fmt.Sprintf("%d/%d (%.0f%%)", row.detected, len(pos), pct(row.detected, len(pos))),
			row.falseTriggers, perHour, marker)
	}

	detected := countAtLeast(pos, *threshold)
	if *attempts > 0 {
		fmt.Printf("\nAt the current threshold %.2f: %d of %d attempts detected (%.0f%%), %d missed "+
			"(including attempts too quiet to be saved)\n",
			*threshold, detected, *attempts, pct(detected, *attempts), *attempts-detected)
	} else {
		fmt.Println("\nPositives only include saved clips (score >= the near-miss bar). For the full " +
			"miss rate, pass -attempts N: how many times you said the wake word.")
	}
}

// scan returns the wake word clips in dir (missing dir: none).
func scan(dir string) []wakeclip.Clip {
	files, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	var clips []wakeclip.Clip
	for _, f := range files {
		if clip, ok := wakeclip.Parse(f); ok {
			clips = append(clips, clip)
		}
	}
	return clips
}

func scores(clips []wakeclip.Clip) []float64 {
	var s []float64
	for _, c := range clips {
		if c.HasScore {
			s = append(s, c.Score)
		}
	}
	return s
}

func printKind(clips []wakeclip.Clip, kind wakeclip.Kind, label string) {
	var s []float64
	for _, c := range clips {
		if c.Kind == kind && c.HasScore {
			s = append(s, c.Score)
		}
	}
	if len(s) == 0 {
		fmt.Printf("  %-12s 0\n", label)
		return
	}
	slices.Sort(s)
	fmt.Printf("  %-12s %d (score min %.3f, median %.3f, max %.3f)\n",
		label, len(s), s[0], s[len(s)/2], s[len(s)-1])
}

type tableRow struct {
	threshold     float64
	detected      int // positives scoring >= threshold
	falseTriggers int // negatives scoring >= threshold
}

func thresholdTable(pos, neg []float64, thresholds []float64) []tableRow {
	rows := make([]tableRow, len(thresholds))
	for i, t := range thresholds {
		rows[i] = tableRow{threshold: t, detected: countAtLeast(pos, t), falseTriggers: countAtLeast(neg, t)}
	}
	return rows
}

func countAtLeast(scores []float64, t float64) int {
	n := 0
	for _, s := range scores {
		if s >= t {
			n++
		}
	}
	return n
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

// listeningHours sums the seconds column of sessions.tsv.
func listeningHours(path string) (hours float64, sessions int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 3 {
			continue
		}
		if seconds, err := strconv.ParseFloat(fields[2], 64); err == nil {
			hours += seconds / 3600
			sessions++
		}
	}
	return hours, sessions
}
