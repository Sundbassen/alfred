// Package onnxrt owns the process-global ONNX Runtime environment, which
// must be initialized exactly once even when several components (wake word
// activator, Silero VAD) load ONNX models.
package onnxrt

import (
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	initOnce sync.Once
	initErr  error
)

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
