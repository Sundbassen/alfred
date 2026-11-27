# Alfred's satellites: the devices in each room that hear "hey alfred" and
# play Alfred's replies, talking to the hub over ESPHome's native API (#54).
#
# The laptop satellite is the Open Home Foundation's linux-voice-assistant
# (Apache-2.0), pinned below, with Alfred's own openWakeWord model. It's not
# our code: we install, configure and run it.
#
#   make laptop-install   # clone linux-voice-assistant and set up its virtualenv
#   make laptop           # run it (exports the wake word model first if needed)

.PHONY: laptop-install laptop-model laptop

DEPS_DIR ?= $(CURDIR)/.deps

LVA_REPO := https://github.com/OHF-Voice/linux-voice-assistant.git
# v1.1.18, pinned by commit (tags can move). Its own Python dependencies
# are version ranges, so the install isn't fully frozen.
LVA_REF := b55d36af522c263eeed4e023d6ebf30da47e4710
LVA_DIR := $(DEPS_DIR)/linux-voice-assistant

# Alfred's wake word, exported from ONNX to TFLite (pyopen-wakeword only
# loads TFLite) with the training environment's Python.
WAKE_MODEL_ONNX ?= $(CURDIR)/../backend/models/alfred/hey_alfred.onnx
TRAIN_PYTHON ?= $(CURDIR)/../trainwakeword/pythonvenv/bin/python
# Where the exported model and its config go (--wake-word-dir). Override it
# to run with a model exported on another machine, e.g. on a Raspberry Pi.
WAKE_DIR ?= $(DEPS_DIR)/openwakeword

NAME ?= alfred-laptop
PORT ?= 6053

laptop-install:
	@if [ ! -d "$(LVA_DIR)/.git" ]; then \
		git init -q $(LVA_DIR) && git -C $(LVA_DIR) remote add origin $(LVA_REPO); \
	fi
	@if [ "$$(git -C $(LVA_DIR) rev-parse HEAD 2>/dev/null)" != "$(LVA_REF)" ]; then \
		git -C $(LVA_DIR) fetch -q --depth 1 origin $(LVA_REF) && git -C $(LVA_DIR) checkout -q FETCH_HEAD; \
	fi
	cd $(LVA_DIR) && ./script/setup
	@echo "linux-voice-assistant $(LVA_REF) installed in $(LVA_DIR)"

laptop-model: $(WAKE_DIR)/hey_alfred.tflite $(WAKE_DIR)/hey_alfred.json

# The weights live in the .onnx.data file next to the .onnx. The script
# writes the .tflite only once it matches the ONNX model's scores; a change
# to it exports again.
EXPORT_SCRIPT := ../trainwakeword/export-tflite.py
$(WAKE_DIR)/hey_alfred.tflite: $(WAKE_MODEL_ONNX) $(wildcard $(WAKE_MODEL_ONNX).data) $(EXPORT_SCRIPT)
	mkdir -p $(WAKE_DIR)
	$(TRAIN_PYTHON) -I $(EXPORT_SCRIPT) $(WAKE_MODEL_ONNX) $@

$(WAKE_DIR)/hey_alfred.json: linux/hey_alfred.json
	mkdir -p $(WAKE_DIR)
	cp linux/hey_alfred.json $@

# Run the satellite: it listens on PORT for the hub (ESPHome native API,
# plaintext in this version) and announces itself over mDNS. DEBUG=1 for its
# debug log. Without the ONNX model on this machine (a Pi, with WAKE_DIR set
# to a model exported on the laptop), it runs with what is in WAKE_DIR.
laptop: $(if $(wildcard $(WAKE_MODEL_ONNX)),laptop-model)
	@if [ ! -x "$(LVA_DIR)/.venv/bin/linux-voice-assistant" ]; then \
		echo "Run make laptop-install first."; exit 1; \
	fi
	@if [ ! -f "$(WAKE_DIR)/hey_alfred.tflite" ] || [ ! -f "$(WAKE_DIR)/hey_alfred.json" ]; then \
		echo "No wake word model in $(WAKE_DIR): train one (trainwakeword/), or set WAKE_DIR."; exit 1; \
	fi
	$(LVA_DIR)/.venv/bin/linux-voice-assistant \
		--name $(NAME) \
		--port $(PORT) \
		--wake-word-dir $(WAKE_DIR) \
		--wake-model hey_alfred \
		--preferences-file $(DEPS_DIR)/preferences.json \
		--download-dir $(DEPS_DIR)/downloads \
		$(if $(DEBUG),--debug)
