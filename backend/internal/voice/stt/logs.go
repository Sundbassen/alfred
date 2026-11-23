package stt

/*
#include <whisper.h>

static void alfred_whisper_discard_log(enum ggml_log_level level, const char *text, void *user_data) {
	(void)level;
	(void)text;
	(void)user_data;
}

static void alfred_whisper_quiet(void) {
	whisper_log_set(alfred_whisper_discard_log, NULL);
}
*/
import "C"

// QuietLogs discards whisper.cpp's and ggml's log output (~40 lines of
// model loading details at startup). The Go bindings expose no log
// setting, so this calls whisper_log_set directly; it also covers ggml,
// which whisper_log_set forwards to. Process-wide; call before loading a
// model.
func QuietLogs() {
	C.alfred_whisper_quiet()
}
