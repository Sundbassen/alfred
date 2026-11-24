#!/bin/bash

# Download whisper model for speech-to-text
# Available models: tiny, base, small, medium, large
# For English-only, add .en suffix (e.g., base.en)

MODEL="base.en"
MODEL_URL="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${MODEL}.bin"
MODEL_DIR="models"
MODEL_FILE="${MODEL_DIR}/ggml-${MODEL}.bin"

mkdir -p "${MODEL_DIR}"

if [ -f "${MODEL_FILE}" ]; then
    echo "Model already exists: ${MODEL_FILE}"
else
    echo "Downloading whisper model: ${MODEL}"
    echo "From: ${MODEL_URL}"
    echo "To: ${MODEL_FILE}"

    curl -L -o "${MODEL_FILE}" "${MODEL_URL}"

    if [ $? -eq 0 ]; then
        echo "Model downloaded successfully!"
        ls -lh "${MODEL_FILE}"
    else
        echo "Failed to download model"
        exit 1
    fi
fi

# Download openWakeWord shared preprocessing models (melspectrogram + embedding)
# Required by the ONNX wake word activator alongside the trained wake word model
OWW_DIR="${MODEL_DIR}/openwakeword"
OWW_BASE_URL="https://github.com/dscripka/openWakeWord/releases/download/v0.5.1"

mkdir -p "${OWW_DIR}"

for OWW_MODEL in melspectrogram.onnx embedding_model.onnx; do
    OWW_FILE="${OWW_DIR}/${OWW_MODEL}"

    if [ -f "${OWW_FILE}" ]; then
        echo "Model already exists: ${OWW_FILE}"
        continue
    fi

    echo "Downloading openWakeWord model: ${OWW_MODEL}"
    curl -L -o "${OWW_FILE}" "${OWW_BASE_URL}/${OWW_MODEL}"

    if [ $? -eq 0 ]; then
        echo "Model downloaded successfully!"
        ls -lh "${OWW_FILE}"
    else
        echo "Failed to download model"
        exit 1
    fi
done

# Download Silero VAD v5 (optional alternative silence detector, -vad silero)
SILERO_DIR="${MODEL_DIR}/silero"
SILERO_FILE="${SILERO_DIR}/silero_vad.onnx"
SILERO_URL="https://github.com/snakers4/silero-vad/raw/v5.1.2/src/silero_vad/data/silero_vad.onnx"

mkdir -p "${SILERO_DIR}"

if [ -f "${SILERO_FILE}" ]; then
    echo "Model already exists: ${SILERO_FILE}"
else
    echo "Downloading Silero VAD model"
    if curl -fL -o "${SILERO_FILE}" "${SILERO_URL}"; then
        echo "Model downloaded successfully!"
        ls -lh "${SILERO_FILE}"
    else
        echo "Failed to download model"
        exit 1
    fi
fi

# Local intent model (opt-in, ~2.1 GB): ./download-model.sh --llm
# Ministral 3 3B Instruct by Mistral AI (Apache-2.0), Q4_K_M GGUF for
# llama.cpp, pinned to a repo revision and checked by sha256.
if [ "$1" = "--llm" ]; then
    LLM_DIR="${MODEL_DIR}/llm"
    LLM_NAME="Ministral-3-3B-Instruct-2512-Q4_K_M.gguf"
    LLM_REV="eb599d408350ea2bb60452cb86be7c7b2fc28227"
    LLM_SHA256="9ed150d4367e68df0ac8e1540f6ddc65b42d0ee26378329d1ecbca60f93fc5f8"
    LLM_URL="https://huggingface.co/mistralai/Ministral-3-3B-Instruct-2512-GGUF/resolve/${LLM_REV}/${LLM_NAME}"
    LLM_FILE="${LLM_DIR}/${LLM_NAME}"

    mkdir -p "${LLM_DIR}"
    if [ -f "${LLM_FILE}" ]; then
        echo "Model already exists: ${LLM_FILE}"
    else
        echo "Downloading intent model ${LLM_NAME} (~2.1 GB)"
        if curl -fL -o "${LLM_FILE}.part" "${LLM_URL}" &&
            echo "${LLM_SHA256}  ${LLM_FILE}.part" | sha256sum -c - &&
            mv "${LLM_FILE}.part" "${LLM_FILE}"; then
            echo "Model downloaded successfully!"
            ls -lh "${LLM_FILE}"
        else
            echo "Failed to download or verify the intent model (partial file: ${LLM_FILE}.part)"
            exit 1
        fi
    fi
fi
