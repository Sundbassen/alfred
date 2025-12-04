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
    exit 0
fi

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
