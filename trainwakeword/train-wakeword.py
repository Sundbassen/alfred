#!/usr/bin/env python3
"""
Wake word model training script using OpenWakeWord.

Wraps the official OpenWakeWord training pipeline (openwakeword/train.py)
driven by a YAML config file. Handles dependency setup, sample generation,
augmentation, feature extraction, model training, and ONNX export.

Dependencies:
    pip install openwakeword torch torchinfo torchmetrics onnx piper-tts
    pip install audiomentations pyroomacoustics librosa datasets

    Also requires the piper-sample-generator repo:
        git clone https://github.com/rhasspy/piper-sample-generator.git

Usage:
    ./train_wakeword.py --phrase "hey alfred" --output ./models
    ./train_wakeword.py --phrase "hey alfred" --output ./models --samples 100000
    ./train_wakeword.py --phrase "hey alfred" --output ./models --custom-samples ./my_recordings
    ./train_wakeword.py --phrase "hey alfred" --output ./models --max-negative-weight 500
"""

import argparse
import logging
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Optional

import yaml


# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Dependency checks
# ---------------------------------------------------------------------------

def check_dependencies(piper_gen_dir: Path) -> None:
    """Verify all required packages and repos are present."""
    packages = {
        "torch":           "torch",
        "torchinfo":       "torchinfo",
        "torchmetrics":    "torchmetrics",
        "onnx":            "onnx",
        "yaml":            "pyyaml",
        "librosa":         "librosa",
        "audiomentations": "audiomentations",
        "openwakeword":    "openwakeword",
    }

    missing = []
    for module, package in packages.items():
        try:
            __import__(module)
        except ImportError:
            missing.append(package)

    if missing:
        log.error("Missing required packages: %s", ", ".join(missing))
        log.error("Install with: pip install %s", " ".join(missing))
        sys.exit(1)

    if not piper_gen_dir.exists():
        log.error("piper-sample-generator not found at: %s", piper_gen_dir)
        log.error("Clone it with:")
        log.error("  git clone https://github.com/rhasspy/piper-sample-generator.git")
        sys.exit(1)

    main_module = piper_gen_dir / "generate_samples.py"
    if not main_module.exists():
        log.error("generate_samples.py not found in: %s", piper_gen_dir)
        sys.exit(1)


# ---------------------------------------------------------------------------
# Download background/RIR data for augmentation
# ---------------------------------------------------------------------------

RIR_DATASET_REPO = "davidscripka/MIT_environmental_impulse_responses"
AUDIOSET_REPO = "agkphysics/AudioSet"
AUDIOSET_SHARDS = 3       # 500 ten-second clips per shard (~700MB download each)
MIN_RIR_FILES = 100       # the MIT dataset contains ~270 responses
MIN_BACKGROUND_FILES = 1000


def download_rirs(rir_dir: Path) -> None:
    """
    Download the MIT environmental impulse response dataset.

    The repo stores ~270 room impulse responses as ready-to-use 16 kHz wav
    files under 16khz/, so a snapshot download is all that is needed.

    Args:
        rir_dir: Directory to store the RIR wav files.
    """
    from huggingface_hub import snapshot_download

    if len(list(rir_dir.rglob("*.wav"))) >= MIN_RIR_FILES:
        log.info("RIR data already present")
        return

    log.info("Downloading room impulse response data (~270 wav files)...")
    snapshot_download(
        repo_id=RIR_DATASET_REPO,
        repo_type="dataset",
        allow_patterns="16khz/*.wav",
        local_dir=str(rir_dir),
    )
    log.info("RIR data downloaded: %d files", len(list(rir_dir.rglob("*.wav"))))


def download_background(background_dir: Path) -> None:
    """
    Download background noise audio from the AudioSet balanced train split.

    AudioSet clips are stored as flac inside parquet shards on HuggingFace.
    Each shard holds 500 ten-second clips (~83 minutes of audio). The clips
    are decoded, downmixed to mono, resampled to 16 kHz, and written as
    int16 wav files - the format the openwakeword augmentation expects.

    Args:
        background_dir: Directory to store the background wav files.
    """
    import io

    import numpy as np
    import pyarrow.parquet as pq
    import scipy.io.wavfile
    import scipy.signal
    import soundfile
    from huggingface_hub import hf_hub_download

    if len(list(background_dir.rglob("*.wav"))) >= MIN_BACKGROUND_FILES:
        log.info("Background audio already present")
        return

    log.info("Downloading background audio (%d AudioSet shards)...", AUDIOSET_SHARDS)
    for shard in range(AUDIOSET_SHARDS):
        shard_name = f"data/bal_train/{shard:02d}.parquet"
        log.info("Processing shard %s...", shard_name)
        parquet_path = hf_hub_download(
            repo_id=AUDIOSET_REPO,
            filename=shard_name,
            repo_type="dataset",
        )

        parquet_file = pq.ParquetFile(parquet_path)
        for batch in parquet_file.iter_batches(batch_size=50, columns=["video_id", "audio"]):
            for row in batch.to_pylist():
                out_path = background_dir / f"audioset_{row['video_id']}.wav"
                if out_path.exists():
                    continue
                try:
                    data, sr = soundfile.read(io.BytesIO(row["audio"]["bytes"]))
                except Exception as e:
                    log.warning("Skipping undecodable clip %s: %s", row["video_id"], e)
                    continue
                if data.ndim > 1:
                    data = data.mean(axis=1)
                if sr != 16000:
                    data = scipy.signal.resample_poly(data, 16000, sr)
                scipy.io.wavfile.write(
                    out_path, 16000, (data * 32767).astype(np.int16))

    log.info("Background audio downloaded: %d files",
             len(list(background_dir.rglob("*.wav"))))


def download_augmentation_data(data_dir: Path) -> tuple[Path, Path]:
    """
    Download room impulse responses and background audio needed for augmentation.

    OpenWakeWord uses RIR data to simulate different room acoustics and
    background audio to improve noise robustness. These augmentations
    (AddBackgroundNoise at probability 0.75, RIR at 0.5) are essential for
    a model that must work with real microphone audio, so missing data is
    a fatal error rather than a warning.

    Args:
        data_dir: Directory to store downloaded augmentation data.

    Returns:
        Tuple of (rir_dir, background_dir) paths.
    """
    rir_dir = data_dir / "rirs"
    background_dir = data_dir / "background"

    rir_dir.mkdir(parents=True, exist_ok=True)
    background_dir.mkdir(parents=True, exist_ok=True)

    try:
        download_rirs(rir_dir)
        download_background(background_dir)
    except Exception as e:
        log.error("Failed to download augmentation data: %s", e)
        log.error("Re-run to resume, or pass --skip-augmentation to train without it")
        log.error("(not recommended: the model will not handle real microphone audio well)")
        sys.exit(1)

    n_rirs = len(list(rir_dir.rglob("*.wav")))
    n_background = len(list(background_dir.rglob("*.wav")))
    if n_rirs < MIN_RIR_FILES or n_background < MIN_BACKGROUND_FILES:
        log.error("Augmentation data incomplete: %d RIR files (need %d), "
                  "%d background files (need %d)",
                  n_rirs, MIN_RIR_FILES, n_background, MIN_BACKGROUND_FILES)
        log.error("Re-run to resume the downloads")
        sys.exit(1)

    return rir_dir, background_dir


# ---------------------------------------------------------------------------
# Download false-positive validation data
# ---------------------------------------------------------------------------

# Both files are needed: negative training data and validation set
# URLs from the official openWakeWord training notebook
FP_DATA_FILES: dict[str, str] = {
    "openwakeword_features_ACAV100M_2000_hrs_16bit.npy": (
        "https://huggingface.co/datasets/davidscripka/openwakeword_features"
        "/resolve/main/openwakeword_features_ACAV100M_2000_hrs_16bit.npy"
    ),
    "validation_set_features.npy": (
        "https://huggingface.co/datasets/davidscripka/openwakeword_features"
        "/resolve/main/validation_set_features.npy"
    ),
}


def download_fp_validation_data(data_dir: Path) -> tuple[Path, Path]:
    """
    Download pre-computed negative training features and validation set.

    OpenWakeWord training requires two files from the openwakeword_features
    dataset: a large negative feature set (ACAV100M, ~2000 hrs of audio)
    used during training, and a smaller validation set used to measure
    false-positive rate during training.

    Requires a HuggingFace account. Log in first with:
        pip install huggingface_hub
        huggingface-cli login

    Args:
        data_dir: Directory to store the downloaded .npy files.

    Returns:
        Tuple of (acav_features_path, validation_features_path).
    """
    data_dir.mkdir(parents=True, exist_ok=True)

    # Try huggingface_hub first - handles auth cleanly
    try:
        from huggingface_hub import hf_hub_download

        acav_path = data_dir / "openwakeword_features_ACAV100M_2000_hrs_16bit.npy"
        val_path  = data_dir / "validation_set_features.npy"

        if not acav_path.exists():
            log.info("Downloading ACAV100M negative features (~3GB)...")
            downloaded = hf_hub_download(
                repo_id="davidscripka/openwakeword_features",
                filename="openwakeword_features_ACAV100M_2000_hrs_16bit.npy",
                repo_type="dataset",
                local_dir=str(data_dir),
            )
            log.info("ACAV100M features downloaded: %s", downloaded)
        else:
            log.info("ACAV100M features already present")

        if not val_path.exists():
            log.info("Downloading validation set features...")
            downloaded = hf_hub_download(
                repo_id="davidscripka/openwakeword_features",
                filename="validation_set_features.npy",
                repo_type="dataset",
                local_dir=str(data_dir),
            )
            log.info("Validation features downloaded: %s", downloaded)
        else:
            log.info("Validation features already present")

        return acav_path, val_path

    except ImportError:
        log.error("huggingface_hub is not installed.")
        log.error("Install it with: pip install huggingface_hub")
        log.error("Then log in with: huggingface-cli login")
        sys.exit(1)
    except Exception as e:
        log.error("Failed to download feature data: %s", e)
        log.error("")
        log.error("The openwakeword_features dataset requires a HuggingFace account.")
        log.error("1. Create a free account at https://huggingface.co")
        log.error("2. Run: pip install huggingface_hub")
        log.error("3. Run: huggingface-cli login")
        log.error("4. Re-run this script")
        sys.exit(1)


# ---------------------------------------------------------------------------
# Build YAML training config
# ---------------------------------------------------------------------------

def build_training_config(
    phrase: str,
    output_dir: Path,
    work_dir: Path,
    piper_gen_dir: Path,
    rir_dir: Path,
    background_dir: Path,
    acav_features_path: Path,
    fp_validation_path: Path,
    n_samples: int,
    max_negative_weight: int,
    target_fp_per_hour: float,
) -> Path:
    """
    Generate the YAML configuration file consumed by openwakeword/train.py.

    The config controls all aspects of training: sample counts, augmentation
    settings, model architecture, and paths to data files.

    Args:
        phrase:              Wake word phrase.
        output_dir:          Where to write the final model.
        work_dir:            Working directory for intermediate files.
        piper_gen_dir:       Path to piper-sample-generator repo.
        rir_dir:             Path to room impulse response audio files.
        background_dir:      Path to background noise audio files.
        acav_features_path:  Path to ACAV100M negative features .npy file.
        fp_validation_path:  Path to false-positive validation .npy file.
        n_samples:           Number of synthetic training samples to generate.
        max_negative_weight: Loss weight for negative examples at the end of
                             training. Higher values push all scores down
                             (fewer false positives, lower positive scores).
        target_fp_per_hour:  Target false positives per hour; training doubles
                             the negative weight until this is met.

    Returns:
        Path to the generated YAML config file.
    """
    safe_name = phrase.replace(" ", "_").lower()

    # Background paths: use downloaded data if available, otherwise empty.
    # Recursive glob: downloads may nest files in subdirectories, and paths
    # must point at the directories that directly contain the wav files
    # (openwakeword scans them non-recursively).
    background_paths = sorted({str(p.parent) for p in background_dir.rglob("*.wav")})
    rir_paths = sorted({str(p.parent) for p in rir_dir.rglob("*.wav")})

    config = {
        # Identity
        "model_name": safe_name,
        "target_phrase": [phrase],
        "output_dir": str(output_dir),

        # Piper sample generation - invoked as a module from its directory
        "piper_sample_generator_path": str(piper_gen_dir),
        "n_samples": n_samples,
        "n_samples_val": max(500, min(3000, n_samples // 10)),
        "tts_batch_size": 8,  # sized for a 4GB GPU

        # Augmentation: each round re-augments every clip with fresh
        # noise/reverb draws, multiplying the effective training data
        "augmentation_rounds": 3,
        "augmentation_batch_size": 64,
        "rir_paths": rir_paths,
        "background_paths": background_paths,
        "background_paths_duplication_rate": [1] * len(background_paths),

        # Feature data files for negative examples during training
        # Uses pre-computed features from a large speech dataset
        "feature_data_files": {
            "acav_100m": str(acav_features_path),
        },

        # False positive validation
        "false_positive_validation_data_path": str(fp_validation_path),

        # Model architecture
        "model_type": "dnn",
        "layer_size": 128,
        "batch_n_per_class": 0,

        # Training
        "steps": 50000,
        "max_negative_weight": max_negative_weight,
        "target_false_positives_per_hour": target_fp_per_hour,

        # Adversarial negatives - similar-sounding phrases to reduce false positives
        "custom_negative_phrases": [],
    }

    config_path = work_dir / "training_config.yaml"
    with open(config_path, "w") as f:
        yaml.dump(config, f, default_flow_style=False)

    log.info("Training config written to: %s", config_path)
    return config_path


# ---------------------------------------------------------------------------
# Run training stages
# ---------------------------------------------------------------------------

# torchaudio removed the top-level info() function in v2.9+, but
# torch_audiomentations (used by the augmentation stage) still calls it.
# This bootstrap restores a minimal soundfile-backed implementation before
# running train.py in the subprocess.
TORCHAUDIO_COMPAT_BOOTSTRAP = """
import runpy
import sys

import torchaudio

if not hasattr(torchaudio, "info"):
    import soundfile

    class _AudioMetaData:
        def __init__(self, sample_rate, num_frames):
            self.sample_rate = sample_rate
            self.num_frames = num_frames

    def _info(filepath, *args, **kwargs):
        info = soundfile.info(str(filepath))
        return _AudioMetaData(info.samplerate, info.frames)

    torchaudio.info = _info

sys.argv = sys.argv[1:]
runpy.run_path(sys.argv[0], run_name="__main__")
"""


def run_training_stage(train_script: Path, config_path: Path, flag: str) -> None:
    """
    Run a single stage of the OpenWakeWord training pipeline.

    The official train.py uses CLI flags to control which stage runs:
    --generate_clips, --augment_clips, --train_model.

    Args:
        train_script: Path to openwakeword/train.py.
        config_path:  Path to the YAML training config.
        flag:         CLI flag for the stage to run.
    """
    cmd = [
        sys.executable,
        "-c", TORCHAUDIO_COMPAT_BOOTSTRAP,
        str(train_script),
        "--training_config", str(config_path),
        flag,
    ]

    log.info("Running: %s --training_config %s %s", train_script, config_path, flag)

    env = os.environ.copy()
    env["PYTORCH_CUDA_ALLOC_CONF"] = "expandable_segments:True"

    # The DNN model is tiny; force CPU for the train stage to avoid OOM
    # caused by openwakeword loading the entire validation set as one GPU batch.
    # GPU is only beneficial for TTS sample generation (--generate_clips).
    if flag == "--train_model":
        log.info("Forcing CPU for --train_model to avoid validation OOM")
        env["CUDA_VISIBLE_DEVICES"] = ""

    result = subprocess.run(cmd, check=False, env=env)
    if result.returncode != 0:
        log.error("Training stage '%s' failed with exit code %d", flag, result.returncode)
        sys.exit(result.returncode)


# ---------------------------------------------------------------------------
# Custom voice samples
# ---------------------------------------------------------------------------

# How many times each custom recording is copied into the training set.
# 50-100 real recordings would be drowned out by ~100k synthetic clips;
# duplication gives them weight, and the augmentation stage still produces
# a different noise/reverb variant for every copy.
CUSTOM_SAMPLE_DUPLICATION = 20


def install_custom_samples(
    custom_samples: list[Path],
    output_dir: Path,
    safe_name: str,
) -> None:
    """
    Copy user-recorded wake word samples into the generated positive clip
    directories so the openwakeword pipeline picks them up.

    openwakeword's train.py has no config input for extra positive data; it
    simply globs *.wav from the positive_train/positive_test directories.
    Roughly 10% of the recordings go to the test split (unduplicated) so the
    final evaluation reflects the user's real voice, and the rest go to the
    training split, duplicated CUSTOM_SAMPLE_DUPLICATION times.

    The positive feature files are deleted to force the augmentation stage
    to recompute features including the new clips.

    Args:
        custom_samples: User-recorded .wav files (16 kHz mono, one utterance each).
        output_dir:     Training output directory.
        safe_name:      Model name (phrase with underscores).
    """
    train_dir = output_dir / safe_name / "positive_train"
    test_dir = output_dir / safe_name / "positive_test"
    train_dir.mkdir(parents=True, exist_ok=True)
    test_dir.mkdir(parents=True, exist_ok=True)

    n_test = max(1, len(custom_samples) // 10)
    test_samples = custom_samples[:n_test]
    train_samples = custom_samples[n_test:]

    for i, sample in enumerate(test_samples):
        shutil.copy(sample, test_dir / f"custom_{i:04d}.wav")

    for i, sample in enumerate(train_samples):
        for dup in range(CUSTOM_SAMPLE_DUPLICATION):
            shutil.copy(sample, train_dir / f"custom_{i:04d}_dup{dup:02d}.wav")

    # Force feature recomputation: the augmentation stage skips work when
    # these files already exist, which would silently ignore the new clips.
    for name in ["positive_features_train.npy", "positive_features_test.npy"]:
        feature_file = output_dir / safe_name / name
        if feature_file.exists():
            feature_file.unlink()
            log.info("Deleted %s to force feature recomputation", feature_file)

    log.info("Installed custom samples: %d train recordings x%d copies, %d test recordings",
             len(train_samples), CUSTOM_SAMPLE_DUPLICATION, len(test_samples))


# ---------------------------------------------------------------------------
# Post-training evaluation
# ---------------------------------------------------------------------------

def evaluate_model(onnx_path: Path, features_dir: Path) -> None:
    """
    Score the trained model against its generated test features and print a
    recall / false-positive-rate table for a range of thresholds.

    Use this table to pick the detection threshold for the runtime config,
    and to compare models trained with different --max-negative-weight.

    Args:
        onnx_path:    Path to the trained wake word .onnx model.
        features_dir: Directory containing positive_features_test.npy and
                      negative_features_test.npy produced by the pipeline.
    """
    import numpy as np
    import onnxruntime as ort

    positive_path = features_dir / "positive_features_test.npy"
    negative_path = features_dir / "negative_features_test.npy"
    if not positive_path.exists() or not negative_path.exists():
        log.warning("Test feature files not found in %s, skipping evaluation", features_dir)
        return

    session = ort.InferenceSession(str(onnx_path))
    input_name = session.get_inputs()[0].name

    def score_features(path: Path) -> "np.ndarray":
        features = np.load(path)
        return np.array([
            session.run(None, {input_name: features[i].astype(np.float32)[None]})[0][0][0]
            for i in range(len(features))
        ])

    positive_scores = score_features(positive_path)
    negative_scores = score_features(negative_path)

    log.info("=" * 50)
    log.info("Model evaluation (%d positive / %d negative test clips)",
             len(positive_scores), len(negative_scores))
    log.info("=" * 50)
    log.info("positive scores: median=%.4f p90=%.4f max=%.4f",
             float(np.median(positive_scores)),
             float(np.percentile(positive_scores, 90)),
             float(positive_scores.max()))
    log.info("negative scores: median=%.4f p90=%.4f max=%.4f",
             float(np.median(negative_scores)),
             float(np.percentile(negative_scores, 90)),
             float(negative_scores.max()))
    log.info("%-12s %-8s %s", "threshold", "recall", "false-positive rate")
    for threshold in [0.01, 0.02, 0.03, 0.05, 0.1, 0.2, 0.3, 0.5]:
        recall = float((positive_scores >= threshold).mean())
        fpr = float((negative_scores >= threshold).mean())
        log.info("%-12.2f %-8.2f %.4f", threshold, recall, fpr)
    log.info("Pick the lowest threshold with an acceptable false-positive rate,")
    log.info("and set it in the Go activator config.")


# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

def parse_args() -> argparse.Namespace:  # argparse has no stricter return type
    parser = argparse.ArgumentParser(
        prog=os.path.basename(__file__),
        description="Train a custom wake word detection model using OpenWakeWord",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
EXAMPLES
    Train with defaults (50000 synthetic samples):
        %(prog)s --phrase "hey alfred" --output ./models

    Train with more samples for better accuracy:
        %(prog)s --phrase "hey alfred" --output ./models --samples 100000

    Train with your own voice recordings:
        %(prog)s --phrase "hey alfred" --output ./models --custom-samples ./my_recordings

    Retrain a calibration variant, reusing already generated clips/features
    (only the final training stage reruns; save the previous .onnx first):
        %(prog)s --phrase "hey alfred" --output ./models --max-negative-weight 500

SETUP
    Before running, clone the piper-sample-generator:
        git clone https://github.com/rhasspy/piper-sample-generator.git

    Install dependencies:
        pip install openwakeword torch torchinfo torchmetrics onnx piper-tts
        pip install audiomentations pyroomacoustics librosa datasets pyyaml

CUSTOM SAMPLES
    Record yourself saying the wake word 50-100 times.
    Save as 16kHz mono .wav files in a directory.
    Pass the directory path via --custom-samples.

    Record with sox (2 second clip):
        sox -d -r 16000 -c 1 recording_001.wav trim 0 2

NOTES
    First run downloads augmentation audio (~2GB) and negative feature data
    (~17GB) from HuggingFace. Everything is cached for subsequent runs.
    With default settings expect several hours total: sample generation on
    GPU is the longest stage, then feature extraction, then training.
    A recall/false-positive table is printed after training - use it to pick
    the detection threshold for the Go activator config.
    Output is a .onnx file ready to use with ONNX Runtime in your Go app.
        """,
    )

    parser.add_argument(
        "--phrase",
        type=str,
        required=True,
        metavar="PHRASE",
        help="Wake word phrase to train (e.g. 'hey alfred')",
    )

    parser.add_argument(
        "--output",
        type=Path,
        required=True,
        metavar="DIR",
        help="Output directory for the trained .onnx model",
    )

    parser.add_argument(
        "--samples",
        type=int,
        default=50000,
        metavar="N",
        help="Number of synthetic training samples (default: 50000, more = better accuracy)",
    )

    parser.add_argument(
        "--max-negative-weight",
        type=int,
        default=1000,
        metavar="W",
        help="Loss weight for negative examples at end of training. Lower values "
             "raise positive scores at the cost of more false positives (default: 1000)",
    )

    parser.add_argument(
        "--target-fp-per-hour",
        type=float,
        default=0.2,
        metavar="N",
        help="Target false positives per hour; training doubles the negative "
             "weight until this is met (default: 0.2)",
    )

    parser.add_argument(
        "--custom-samples",
        type=Path,
        default=None,
        metavar="DIR",
        help="Directory of your own .wav recordings to improve accuracy (optional)",
    )

    parser.add_argument(
        "--piper-gen",
        type=Path,
        default=Path("./piper-sample-generator"),
        metavar="DIR",
        help="Path to piper-sample-generator repo (default: ./piper-sample-generator)",
    )

    parser.add_argument(
        "--data-cache",
        type=Path,
        default=Path.home() / ".cache" / "openwakeword-training",
        metavar="DIR",
        help="Directory to cache downloaded augmentation/validation data",
    )

    parser.add_argument(
        "--keep-intermediate",
        action="store_true",
        default=False,
        help="Keep intermediate working files after training completes",
    )

    parser.add_argument(
        "--skip-augmentation",
        action="store_true",
        default=False,
        help="Skip audio augmentation step (faster but lower accuracy)",
    )

    return parser.parse_args()


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main() -> None:
    args = parse_args()

    phrase   = args.phrase.strip().lower()
    out_dir  = args.output.resolve()
    work_dir = out_dir / ".work"

    words = phrase.split()
    if len(words) > 4:
        log.warning("Phrase has %d words. 2-4 words recommended.", len(words))
    if len(words) < 2:
        log.warning("Single-word phrases have higher false-positive rates. Consider 2-3 words.")

    log.info("=" * 50)
    log.info("Wake Word Training")
    log.info("=" * 50)
    log.info("Phrase:          %s", phrase)
    log.info("Output:          %s", out_dir)
    log.info("Samples:         %d", args.samples)
    log.info("Custom samples:  %s", args.custom_samples or "none")
    log.info("Piper gen dir:   %s", args.piper_gen)
    log.info("=" * 50)

    # Validate
    check_dependencies(args.piper_gen)

    custom_samples: Optional[list[Path]] = None
    if args.custom_samples:
        wav_files = list(args.custom_samples.glob("*.wav"))
        if not wav_files:
            log.error("No .wav files found in: %s", args.custom_samples)
            sys.exit(1)
        custom_samples = wav_files
        log.info("Found %d custom voice samples", len(custom_samples))
        if len(custom_samples) < 10:
            log.warning("Only %d custom samples. 50-100 recommended.", len(custom_samples))

    # Locate openwakeword/train.py (installed as a package)
    import openwakeword
    oww_train_script = Path(openwakeword.__file__).parent / "train.py"
    if not oww_train_script.exists():
        log.error("openwakeword/train.py not found at: %s", oww_train_script)
        log.error("Try reinstalling: pip install --force-reinstall openwakeword")
        sys.exit(1)

    out_dir.mkdir(parents=True, exist_ok=True)
    work_dir.mkdir(parents=True, exist_ok=True)

    try:
        # Download augmentation data
        if not args.skip_augmentation:
            log.info("[1/5] Downloading augmentation data...")
            rir_dir, background_dir = download_augmentation_data(args.data_cache)
        else:
            log.info("[1/5] Skipping augmentation data download (--skip-augmentation set)")
            rir_dir = args.data_cache / "rirs"
            background_dir = args.data_cache / "background"
            rir_dir.mkdir(parents=True, exist_ok=True)
            background_dir.mkdir(parents=True, exist_ok=True)

        # Download false-positive validation data
        log.info("[2/5] Downloading false-positive validation data...")
        acav_features_path, fp_validation_path = download_fp_validation_data(args.data_cache)

        # Build YAML config
        config_path = build_training_config(
            phrase=phrase,
            output_dir=out_dir,
            work_dir=work_dir,
            piper_gen_dir=args.piper_gen.resolve(),
            rir_dir=rir_dir,
            background_dir=background_dir,
            acav_features_path=acav_features_path,
            fp_validation_path=fp_validation_path,
            n_samples=args.samples,
            max_negative_weight=args.max_negative_weight,
            target_fp_per_hour=args.target_fp_per_hour,
        )

        # Stage 1: generate synthetic clips
        log.info("[3/5] Generating synthetic samples...")
        run_training_stage(oww_train_script, config_path, "--generate_clips")

        # Add user-recorded samples to the positive clip sets
        if custom_samples:
            safe_name = phrase.replace(" ", "_").lower()
            install_custom_samples(custom_samples, out_dir, safe_name)

        # Stage 2: augment clips and extract features
        if not args.skip_augmentation:
            log.info("[4/5] Augmenting clips and extracting features...")
            run_training_stage(oww_train_script, config_path, "--augment_clips")
        else:
            log.info("[4/5] Skipping augmentation (--skip-augmentation set)")

        # Stage 3: train model
        log.info("[5/5] Training model...")
        run_training_stage(oww_train_script, config_path, "--train_model")

    finally:
        if not args.keep_intermediate and work_dir.exists():
            log.info("Cleaning up intermediate files...")
            shutil.rmtree(work_dir)

    safe_name = phrase.replace(" ", "_").lower()
    onnx_path = out_dir / f"{safe_name}.onnx"

    log.info("=" * 50)
    log.info("Training complete!")
    log.info("=" * 50)
    if onnx_path.exists():
        log.info("ONNX model: %s", onnx_path)
        evaluate_model(onnx_path, out_dir / safe_name)
    else:
        log.warning("Expected output not found at: %s", onnx_path)
        log.warning("Check %s for output files", out_dir)


if __name__ == "__main__":
    main()