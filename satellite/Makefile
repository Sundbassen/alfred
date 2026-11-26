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
LVA_REF := v1.1.18
LVA_DIR := $(DEPS_DIR)/linux-voice-assistant

# Alfred's wake word, exported from ONNX to TFLite (pyopen-wakeword only
# loads TFLite) with the training environment's Python.
WAKE_MODEL_ONNX ?= $(CURDIR)/../backend/models/alfred/hey_alfred.onnx
TRAIN_PYTHON ?= $(CURDIR)/../trainwakeword/pythonvenv/bin/python
# The name contains "openwakeword" on purpose: linux-voice-assistant then
# prefers openWakeWord models for --wake-model.
WAKE_DIR := $(DEPS_DIR)/openwakeword

NAME ?= alfred-laptop
PORT ?= 6053

laptop-install:
	@if [ ! -d "$(LVA_DIR)/.git" ]; then \
		git clone -q --depth 1 --branch $(LVA_REF) $(LVA_REPO) $(LVA_DIR); \
	else \
		git -C $(LVA_DIR) fetch -q --depth 1 origin tag $(LVA_REF) && git -C $(LVA_DIR) checkout -q $(LVA_REF); \
	fi
	cd $(LVA_DIR) && ./script/setup --makeflags=-j4
	@echo "linux-voice-assistant $(LVA_REF) installed in $(LVA_DIR)"

laptop-model: $(WAKE_DIR)/hey_alfred.tflite

$(WAKE_DIR)/hey_alfred.tflite: $(WAKE_MODEL_ONNX) linux/hey_alfred.json
	mkdir -p $(WAKE_DIR)
	$(TRAIN_PYTHON) -I ../trainwakeword/export-tflite.py $(WAKE_MODEL_ONNX) $@
	cp linux/hey_alfred.json $(WAKE_DIR)/hey_alfred.json

# Run the satellite: it listens on PORT for the hub (ESPHome native API,
# plaintext in this version) and announces itself over mDNS. DEBUG=1 for its
# debug log.
laptop: laptop-model
	@if [ ! -x "$(LVA_DIR)/.venv/bin/linux-voice-assistant" ]; then \
		echo "Run make laptop-install first."; exit 1; \
	fi
	$(LVA_DIR)/.venv/bin/linux-voice-assistant \
		--name $(NAME) \
		--port $(PORT) \
		--wake-word-dir $(WAKE_DIR) \
		--wake-model hey_alfred \
		--preferences-file $(DEPS_DIR)/preferences.json \
		--download-dir $(DEPS_DIR)/downloads \
		$(if $(DEBUG),--debug)
