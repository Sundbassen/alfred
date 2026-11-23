.PHONY: build all install clean prereqs run record review replay

# Whisper.cpp paths
WHISPER_DIR := /tmp/whisper.cpp
WHISPER_INCLUDE := $(WHISPER_DIR)/include
GGML_INCLUDE := $(WHISPER_DIR)/ggml/include
WHISPER_LIB := $(WHISPER_DIR)/build/bin
GGML_LIB := $(WHISPER_DIR)/build/bin

# ONNX Runtime paths (prebuilt release, loaded at runtime via dlopen)
ONNXRUNTIME_VERSION := 1.26.0
ONNXRUNTIME_DIR := /tmp/onnxruntime
ONNXRUNTIME_URL := https://github.com/microsoft/onnxruntime/releases/download/v$(ONNXRUNTIME_VERSION)/onnxruntime-linux-x64-$(ONNXRUNTIME_VERSION).tgz

# CGO flags
export CGO_CFLAGS := -I$(WHISPER_INCLUDE) -I$(GGML_INCLUDE)
export CGO_LDFLAGS := -L$(WHISPER_LIB) -L$(GGML_LIB) -lwhisper -lggml

# Library path for runtime
export LD_LIBRARY_PATH := $(WHISPER_LIB):$(GGML_LIB):$(ONNXRUNTIME_DIR)/lib

# Install system prerequisites
prereqs:
	@echo "Installing system prerequisites..."
	@echo "To be able to run this project you need to install cmake base-devel go curl"

# Install dependencies
install:
	@echo "Setting up whisper.cpp..."
	@if [ ! -d "$(WHISPER_DIR)" ]; then \
		echo "Cloning whisper.cpp..."; \
		git clone https://github.com/ggerganov/whisper.cpp.git $(WHISPER_DIR); \
	else \
		echo "whisper.cpp already cloned"; \
	fi
	@echo "Building whisper.cpp..."
	@cd $(WHISPER_DIR) && \
		mkdir -p build && \
		cd build && \
		cmake .. && \
		make
	@echo "whisper.cpp setup complete!"
	@echo "Setting up ONNX Runtime..."
	@if [ ! -f "$(ONNXRUNTIME_DIR)/lib/libonnxruntime.so" ]; then \
		echo "Downloading ONNX Runtime $(ONNXRUNTIME_VERSION)..."; \
		curl -fL -o /tmp/onnxruntime.tgz "$(ONNXRUNTIME_URL)"; \
		mkdir -p $(ONNXRUNTIME_DIR); \
		tar -xzf /tmp/onnxruntime.tgz -C $(ONNXRUNTIME_DIR) --strip-components=1; \
		rm /tmp/onnxruntime.tgz; \
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

# Build and run in recording mode: saves every detected utterance to
# .recordings/ for wake word training (say the wake word repeatedly)
record: build
	@echo "Running alfred in RECORDING mode..."
	./alfred -record

# Review collected recordings: plays each one and asks keep/delete.
# Kept files move to .recordings/validated for --custom-samples training.
review:
	@go run ./cmd/reviewrecordings

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f alfred
	@echo "Clean complete!"

# Replay a recorded wav (16 kHz mono s16) through the command pipeline:
#   make replay WAV=hey_alfred_lights.wav [VAD=silero]
replay:
	go run ./cmd/replaywav -vad $(or $(VAD),rms) $(WAV)
