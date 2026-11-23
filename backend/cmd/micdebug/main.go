// micdebug captures a few seconds from the microphone through the same
// mic service the app uses and reports byte-level statistics, to diagnose
// sample format problems (e.g. float32 data being parsed as int16).
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/sundbassen/alfred/component/mic"
	"github.com/sundbassen/alfred/internal/logging"
)

func main() {
	logger := logging.New(true)

	config := mic.DefaultConfig()
	micService, err := mic.NewMicService(config, logger)
	if err != nil {
		fmt.Println("failed to create mic service:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := micService.Start(ctx); err != nil {
		fmt.Println("failed to start mic:", err)
		os.Exit(1)
	}

	var data []byte
	for chunk := range micService.AudioChannel() {
		data = append(data, chunk...)
	}

	fmt.Printf("captured %d bytes (%.2f s if int16 mono @16kHz)\n",
		len(data), float64(len(data))/32000.0)
	if len(data) < 64 {
		fmt.Println("not enough data captured")
		os.Exit(1)
	}

	fmt.Printf("first 64 bytes: % x\n", data[:64])

	if len(os.Args) > 1 {
		if err := os.WriteFile(os.Args[1], data, 0o644); err != nil {
			fmt.Println("failed to write dump:", err)
		} else {
			fmt.Println("raw dump written to", os.Args[1])
		}
	}

	// Interpret as little-endian int16
	var sumI16 float64
	nI16 := len(data) / 2
	for i := 0; i < nI16; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(data[i*2:]))) / 32767.0
		sumI16 += s * s
	}
	fmt.Printf("as int16:   RMS=%.5f\n", math.Sqrt(sumI16/float64(nI16)))

	// Interpret as little-endian float32
	var sumF32 float64
	valid := 0
	nF32 := len(data) / 4
	for i := 0; i < nF32; i++ {
		f := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:])))
		if !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) <= 1.5 {
			sumF32 += f * f
			valid++
		}
	}
	if valid > 0 {
		fmt.Printf("as float32: RMS=%.5f (%d/%d samples in valid [-1.5,1.5] range)\n",
			math.Sqrt(sumF32/float64(valid)), valid, nF32)
	}
}
