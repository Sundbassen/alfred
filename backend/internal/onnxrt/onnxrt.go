// Package onnxrt owns the process-global ONNX Runtime environment, which
// must be initialized exactly once even when several components (wake word
// activator, Silero VAD) load ONNX models.
package onnxrt

import (
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	initOnce sync.Once
	initErr  error
)

// LibEnv names the environment variable holding the path of
// libonnxruntime.so. The Makefile sets it to the copy make install puts in
// .deps.
const LibEnv = "ALFRED_ONNXRUNTIME_LIB"

// defaultLib is where make install puts the library, relative to backend/
// (the directory the app runs from, like models/).
const defaultLib = ".deps/onnxruntime/lib/libonnxruntime.so"

// LibPath returns the ONNX Runtime library to load: $ALFRED_ONNXRUNTIME_LIB
// if set, else the make install location relative to the working
// directory.
func LibPath() string {
	if p := os.Getenv(LibEnv); p != "" {
		return p
	}
	return defaultLib
}

// Init loads the ONNX Runtime shared library and initializes the
// environment. Only the first call's libPath is used; later calls return
// the first call's result.
func Init(libPath string) error {
	initOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		initErr = ort.InitializeEnvironment()
	})
	return initErr
}
