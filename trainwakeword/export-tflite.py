#!/usr/bin/env python3
"""Export an openWakeWord model from ONNX to TensorFlow Lite.

openWakeWord's training writes ONNX, which Alfred's own wake word code runs.
Satellites built on Home Assistant's pyopen-wakeword (linux-voice-assistant)
only load TFLite. openWakeWord's own converter needs onnx-tf, which no longer
works with current onnx, so this script rebuilds the (small) classifier in
TensorFlow from the ONNX weights and checks that both give the same scores.

The classifier is openWakeWord's DNN: flatten the [16, 96] feature window,
then layer1 → layer norm → ReLU, N blocks of linear → layer norm → ReLU, and
last_layer → sigmoid.

    python export-tflite.py ../backend/models/alfred/hey_alfred.onnx hey_alfred.tflite
"""

import argparse
import re
import sys

import numpy as np
import onnx
import onnxruntime as ort
import tensorflow as tf
from onnx import numpy_helper


def load_weights(path):
    model = onnx.load(path)  # loads external data (.onnx.data) next to it
    weights = {i.name: numpy_helper.to_array(i) for i in model.graph.initializer}
    eps = 1e-5
    for node in model.graph.node:
        if node.op_type == "LayerNormalization":
            for a in node.attribute:
                if a.name == "epsilon":
                    eps = a.f
    shape = [d.dim_value for d in model.graph.input[0].type.tensor_type.shape.dim]
    return weights, eps, shape


def build(weights, eps, shape):
    """A tf.Module computing the classifier with the ONNX weights."""
    blocks = sorted({int(m.group(1)) for k in weights if (m := re.match(r"blocks\.(\d+)\.", k))})
    layers = [("layer1.weight", "layer1.bias", "layernorm1.weight", "layernorm1.bias")]
    for b in blocks:
        p = f"blocks.{b}."
        layers.append((p + "fcn_layer.weight", p + "fcn_layer.bias", p + "layer_norm.weight", p + "layer_norm.bias"))
    const = lambda k: tf.constant(weights[k], dtype=tf.float32)

    class Classifier(tf.Module):
        @tf.function(input_signature=[tf.TensorSpec(shape, tf.float32, name="x")])
        def __call__(self, x):
            h = tf.reshape(x, [shape[0], -1])
            for w, b, g, beta in layers:
                h = tf.matmul(h, const(w), transpose_b=True) + const(b)
                mean, var = tf.nn.moments(h, axes=[-1], keepdims=True)
                h = (h - mean) / tf.sqrt(var + eps) * const(g) + const(beta)
                h = tf.nn.relu(h)
            h = tf.matmul(h, const("last_layer.weight"), transpose_b=True) + const("last_layer.bias")
            return tf.sigmoid(h)

    return Classifier()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("onnx_model")
    parser.add_argument("tflite_model")
    args = parser.parse_args()

    weights, eps, shape = load_weights(args.onnx_model)
    module = build(weights, eps, shape)
    converter = tf.lite.TFLiteConverter.from_concrete_functions(
        [module.__call__.get_concrete_function()], module)
    tflite = converter.convert()
    with open(args.tflite_model, "wb") as f:
        f.write(tflite)

    # Both models must agree, on random features and on all-zero input.
    onnx_session = ort.InferenceSession(args.onnx_model)
    interpreter = tf.lite.Interpreter(model_content=tflite)
    interpreter.allocate_tensors()
    inp, out = interpreter.get_input_details()[0], interpreter.get_output_details()[0]
    rng = np.random.default_rng(0)
    worst = 0.0
    for i in range(200):
        x = np.zeros(shape, np.float32) if i == 0 else rng.normal(0, 3, shape).astype(np.float32)
        want = onnx_session.run(None, {onnx_session.get_inputs()[0].name: x})[0]
        interpreter.set_tensor(inp["index"], x)
        interpreter.invoke()
        got = interpreter.get_tensor(out["index"])
        worst = max(worst, float(np.max(np.abs(want - got))))
    print(f"wrote {args.tflite_model} ({len(tflite)} bytes); input {shape}; "
          f"largest score difference vs ONNX over 200 inputs: {worst:.2e}")
    if worst > 1e-4:
        sys.exit("TFLite and ONNX scores differ too much")


if __name__ == "__main__":
    main()
