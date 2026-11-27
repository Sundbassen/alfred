.PHONY: build all install clean prereqs run review replay wakestats test vet layers spotify-login sqlc sqlc-diff proto proto-diff

# Native dependencies live in a project-local, gitignored directory (not
# /tmp, which is wiped on reboot). Override with make install DEPS_DIR=...
DEPS_DIR ?= $(CURDIR)/.deps

# Whisper.cpp source, pinned so every machine (and CI) builds the same
# version the Go bindings were tested against
WHISPER_REPO := https://github.com/ggerganov/whisper.cpp.git
WHISPER_REF := 96ec4d9823e9d0211944172da35f9f29134af990
WHISPER_DIR := $(DEPS_DIR)/whisper.cpp
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
ONNXRUNTIME_DIR := $(DEPS_DIR)/onnxruntime
ONNXRUNTIME_WHEEL_URL := https://files.pythonhosted.org/packages/7c/43/2a4e04f8dbeffad19bbcced4bcd4289bf478921518437404d6b92bdf213b/onnxruntime-1.26.0-cp312-cp312-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl
ONNXRUNTIME_WHEEL_SHA256 := 9b6dd70599005bd1bf29779f04a91978b92b5e719c11a20068a8f8e535f725b6

# llama.cpp, for its llama-server: runs the local intent model as a child
# process of Alfred (internal/llm/llamaserver). Pinned like whisper.cpp;
# built without its web UI (Alfred only uses the API).
LLAMA_REPO := https://github.com/ggml-org/llama.cpp.git
LLAMA_REF := b1b770d90d94cad9e9ea5b993bdbc383a522fb28
LLAMA_DIR := $(DEPS_DIR)/llama.cpp
LLAMA_CMAKE_FLAGS ?=

# llama-server binary for the app (wiring.ConfigFromEnv)
export ALFRED_LLAMA_SERVER := $(LLAMA_DIR)/build-server/bin/llama-server

# CGO flags. The rpath lets ./alfred and the tools find libwhisper/libggml
# without LD_LIBRARY_PATH.
export CGO_CFLAGS := -I$(WHISPER_INCLUDE) -I$(GGML_INCLUDE)
export CGO_LDFLAGS := -L$(WHISPER_LIB) -L$(GGML_LIB) -lwhisper -lggml -Wl,-rpath,$(WHISPER_LIB)

# Library path for go run / go test binaries
export LD_LIBRARY_PATH := $(WHISPER_LIB):$(GGML_LIB):$(ONNXRUNTIME_DIR)/lib

# ONNX Runtime library for the app and the tests (internal/onnxrt.LibPath)
export ALFRED_ONNXRUNTIME_LIB := $(ONNXRUNTIME_DIR)/lib/libonnxruntime.so

# Install system prerequisites
prereqs:
	@echo "Installing system prerequisites..."
	@echo "To be able to run this project you need to install cmake base-devel go curl git espeak-ng libpulse"

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
	@echo "Setting up llama.cpp $(LLAMA_REF)..."
	@if [ ! -d "$(LLAMA_DIR)/.git" ]; then \
		git init -q $(LLAMA_DIR) && \
		git -C $(LLAMA_DIR) remote add origin $(LLAMA_REPO); \
	fi
	@# Go 1.27 refuses to build cgo code linked with -fopenmp (the whisper
	@# binding) when a GCC .spec file sits under the working directory, and
	@# llama.cpp ships packaging specs in .devops/: leave that folder out.
	@# The index refresh lets git remove files a cache restore re-timestamped.
	@git -C $(LLAMA_DIR) update-index -q --refresh >/dev/null 2>&1 || true
	@git -C $(LLAMA_DIR) sparse-checkout set --no-cone '/*' '!/.devops/'
	@if [ "$$(git -C $(LLAMA_DIR) rev-parse HEAD 2>/dev/null)" != "$(LLAMA_REF)" ]; then \
		echo "Fetching llama.cpp $(LLAMA_REF)..."; \
		git -C $(LLAMA_DIR) fetch -q --depth 1 origin $(LLAMA_REF) && \
		git -C $(LLAMA_DIR) checkout -q FETCH_HEAD; \
	else \
		echo "llama.cpp already at $(LLAMA_REF)"; \
	fi
	@echo "Building llama-server..."
	@cmake -S $(LLAMA_DIR) -B $(LLAMA_DIR)/build-server -DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS=OFF \
		-DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF $(LLAMA_CMAKE_FLAGS) && \
		cmake --build $(LLAMA_DIR)/build-server --target llama-server -j $$(nproc)
	@echo "llama.cpp setup complete!"
	@# The general rule behind the .devops exclusion: no GCC spec file
	@# (a file named *.spec or "specs", any case, as Go checks; directories
	@# don't count) may sit under the module tree, or Go 1.27 refuses the
	@# -fopenmp cgo build.
	@specs=$$(find $(DEPS_DIR)/ ! -type d \( -iname '*.spec' -o -iname specs \) 2>/dev/null); if [ -n "$$specs" ]; then \
		echo "Error: Go 1.27 won't build with GCC spec files (*.spec, specs) under $(DEPS_DIR); leave them out of the checkout:"; \
		echo "$$specs"; exit 1; fi

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
# .recordings/{wakeword,commands} (review wake word clips with make review).
# V=1 adds debug logs (level/score stats) and whisper.cpp output.
run: build
	@echo "Running alfred..."
	./alfred $(if $(SAVE),-save) $(if $(V),-v)

# Review collected wake word clips: plays each one and asks
#   y = the wake word  -> .recordings/validated (--custom-samples)
#   x = not it         -> .recordings/negative  (--custom-negatives)
#   n = delete (damaged/unclear wake word, or private)
review:
	@go run ./cmd/reviewrecordings

# Log Alfred in to Spotify once (needs ALFRED_SPOTIFY_CLIENT_ID); writes the
# token the music skill refreshes from (see README "Music (Spotify)")
spotify-login:
	@go run ./cmd/spotify-login

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

# Generate the database query code (internal/store/storedb) from
# internal/store/queries and the schema in internal/store/migrations; see
# sqlc.yaml. sqlc is pinned as a tool in tools.mod (no global install; the
# first run builds it, ~20 s). sqlc-diff fails if the generated code is out
# of date (CI).
SQLC = go tool -modfile=tools.mod sqlc
sqlc:
	$(SQLC) generate

sqlc-diff:
	$(SQLC) diff

# Generate Go from the vendored ESPHome API definition (proto/esphome, from
# aioesphomeapi) into internal/esphome/esphomepb; see buf.gen.yaml. buf and
# protoc-gen-go are pinned tools in tools.mod. proto-diff regenerates and
# fails if that changed anything (CI, on a clean checkout).
BUF = go tool -modfile=tools.mod buf
proto:
	$(BUF) generate

proto-diff: proto
	@git diff --exit-code --stat -- internal/esphome/esphomepb || \
		(echo "internal/esphome/esphomepb is out of date: run make proto and commit"; exit 1)
	@test -z "$$(git ls-files --others --exclude-standard -- internal/esphome/esphomepb)" || \
		(echo "make proto created files that aren't committed"; exit 1)

# Enforce the layer rule (.claude/rules/project-patterns.md): cmd may import
# anything, skills/ may import internal/, internal/ may import only
# internal/. Checks direct imports, test files included.
LAYER_IMPORTS = {{.ImportPath}}: {{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}
layers:
	@bad=$$(go list -f '$(LAYER_IMPORTS)' ./internal/... | grep -E ' github.com/sundbassen/alfred/(skills|cmd)(/| |$$)'); \
	if [ -n "$$bad" ]; then echo "internal/ must not import skills/ or cmd/:"; echo "$$bad"; exit 1; fi
	@bad=$$(go list -f '$(LAYER_IMPORTS)' ./skills/... | grep -E ' github.com/sundbassen/alfred/cmd(/| |$$)'); \
	if [ -n "$$bad" ]; then echo "skills/ must not import cmd/:"; echo "$$bad"; exit 1; fi
	@echo "layers ok"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f alfred
	@echo "Clean complete!"

# Replay a recorded wav (16 kHz mono s16) through the command pipeline:
#   make replay WAV=hey_alfred_lights.wav [VAD=silero] [V=1]
replay:
	go run ./cmd/replaywav -vad $(or $(VAD),rms) $(if $(V),-v) $(WAV)
