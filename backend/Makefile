.PHONY: build all install clean prereqs run review replay wakestats test vet

# Whisper.cpp source, pinned so every machine (and CI) builds the same
# version the Go bindings were tested against
WHISPER_REPO := https://github.com/ggerganov/whisper.cpp.git
WHISPER_REF := 96ec4d9823e9d0211944172da35f9f29134af990
WHISPER_DIR := /tmp/whisper.cpp
# Extra cmake flags, e.g. -DGGML_NATIVE=OFF for a portable build (CI)
WHISPER_CMAKE_FLAGS ?=
WHISPER_INCLUDE := $(WHISPER_DIR)/include
GGML_INCLUDE := $(WHISPER_DIR)/ggml/include
WHISPER_LIB := $(WHISPER_DIR)/build/bin
GGML_LIB := $(WHISPER_DIR)/build/bin

# ONNX Runtime (loaded at runtime via dlopen). The shared library is taken
# from Microsoft's official PyPI wheel, pinned by version and sha256: the
# GitHub release tarball URL used before returns 404.
ONNXRUNTIME_VERSION := 1.26.0
ONNXRUNTIME_DIR := /tmp/onnxruntime
ONNXRUNTIME_WHEEL_URL := https://files.pythonhosted.org/packages/7c/43/2a4e04f8dbeffad19bbcced4bcd4289bf478921518437404d6b92bdf213b/onnxruntime-1.26.0-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl
ONNXRUNTIME_WHEEL_SHA256 := 9b6dd70599005bd1bf29779f04a91978b92b5e719c11a20068a8f8e535f725b6

# CGO flags
export CGO_CFLAGS := -I$(WHISPER_INCLUDE) -I$(GGML_INCLUDE)
export CGO_LDFLAGS := -L$(WHISPER_LIB) -L$(GGML_LIB) -lwhisper -lggml

# Library path for runtime
export LD_LIBRARY_PATH := $(WHISPER_LIB):$(GGML_LIB):$(ONNXRUNTIME_DIR)/lib

# Install system prerequisites
prereqs:
	@echo "Installing system prerequisites..."
	@echo "To be able to run this project you need to install cmake base-devel go curl git espeak-ng"

# Install dependencies
install:
	@echo "Setting up whisper.cpp $(WHISPER_REF)..."
	@if [ ! -d "$(WHISPER_DIR)/.git" ]; then \
		git init -q $(WHISPER_DIR) && \
		git -C $(WHISPER_DIR) remote add origin $(WHISPER_REPO); \
	fi
	@if [ "$$(git -C $(WHISPER_DIR) rev-parse HEAD 2>/dev/null)" != "$(WHISPER_REF)" ]; then \
		echo "Fetching whisper.cpp $(WHISPER_REF)..."; \
		git -C $(WHISPER_DIR) fetch -q --depth 1 origin $(WHISPER_REF) && \
		git -C $(WHISPER_DIR) checkout -q FETCH_HEAD; \
	else \
		echo "whisper.cpp already at $(WHISPER_REF)"; \
	fi
	@echo "Building whisper.cpp..."
	@cmake -S $(WHISPER_DIR) -B $(WHISPER_DIR)/build $(WHISPER_CMAKE_FLAGS) && \
		cmake --build $(WHISPER_DIR)/build -j $$(nproc)
	@echo "whisper.cpp setup complete!"
	@echo "Setting up ONNX Runtime..."
	@if [ ! -f "$(ONNXRUNTIME_DIR)/lib/libonnxruntime.so" ]; then \
		echo "Downloading ONNX Runtime $(ONNXRUNTIME_VERSION)..."; \
		mkdir -p $(ONNXRUNTIME_DIR)/lib && \
		curl -fsSL -o $(ONNXRUNTIME_DIR)/onnxruntime.whl "$(ONNXRUNTIME_WHEEL_URL)" && \
		echo "$(ONNXRUNTIME_WHEEL_SHA256)  $(ONNXRUNTIME_DIR)/onnxruntime.whl" | sha256sum -c - && \
		unzip -p $(ONNXRUNTIME_DIR)/onnxruntime.whl 'onnxruntime/capi/libonnxruntime.so.$(ONNXRUNTIME_VERSION)' \
			> $(ONNXRUNTIME_DIR)/lib/libonnxruntime.so && \
		rm -f $(ONNXRUNTIME_DIR)/onnxruntime.whl || exit 1; \
	else \
		echo "ONNX Runtime already installed"; \
	fi
	@echo "ONNX Runtime setup complete!"

# Build the application
build:
	@echo "Building alfred..."
	@if [ ! -d "$(WHISPER_DIR)/build" ]; then \
		echo "Error: whisper.cpp not built. Run 'make install' first."; \
		exit 1; \
	fi
	go build -o alfred ./cmd/alfred/main.go
	@echo "Build complete!"

# Build and run. SAVE=1 saves wake word clips, near misses and commands to
# .recordings/{wakeword,commands} (review wake word clips with make review)
run: build
	@echo "Running alfred..."
	./alfred $(if $(SAVE),-save)

# Review collected wake word clips: plays each one and asks
#   y = the wake word  -> .recordings/validated (--custom-samples)
#   x = not it         -> .recordings/negative  (--custom-negatives)
#   n = delete (damaged/unclear wake word, or private)
review:
	@go run ./cmd/reviewrecordings

# Wake word detection and false-trigger rates per threshold, from reviewed
# clips and listening time:  make wakestats [ATTEMPTS=40]
wakestats:
	@go run ./cmd/wakestats $(if $(ATTEMPTS),-attempts $(ATTEMPTS))

# Run the tests / go vet with the whisper.cpp cgo flags above (plain
# `go test ./...` fails with "whisper.h: No such file or directory").
# Tests that need downloaded models skip when they are missing.
test:
	go test ./...

vet:
	go vet ./...

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f alfred
	@echo "Clean complete!"

# Replay a recorded wav (16 kHz mono s16) through the command pipeline:
#   make replay WAV=hey_alfred_lights.wav [VAD=silero]
replay:
	go run ./cmd/replaywav -vad $(or $(VAD),rms) $(WAV)
