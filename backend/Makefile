.PHONY: build all install clean prereqs

# Whisper.cpp paths
WHISPER_DIR := /tmp/whisper.cpp
WHISPER_INCLUDE := $(WHISPER_DIR)/include
GGML_INCLUDE := $(WHISPER_DIR)/ggml/include
WHISPER_LIB := $(WHISPER_DIR)/build/src
GGML_LIB := $(WHISPER_DIR)/build/ggml/src

# CGO flags
export CGO_CFLAGS := -I$(WHISPER_INCLUDE) -I$(GGML_INCLUDE)
export CGO_LDFLAGS := -L$(WHISPER_LIB) -L$(GGML_LIB) -lwhisper -lggml

# Library path for runtime
export LD_LIBRARY_PATH := $(WHISPER_LIB):$(GGML_LIB)

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

# Build the application
build:
	@echo "Building alfred..."
	@if [ ! -d "$(WHISPER_DIR)/build" ]; then \
		echo "Error: whisper.cpp not built. Run 'make install' first."; \
		exit 1; \
	fi
	go build -o alfred ./cmd/alfred/main.go
	@echo "Build complete!"

# Build and run
run: build
	@echo "Running alfred..."
	./alfred

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f alfred
	@echo "Clean complete!"
