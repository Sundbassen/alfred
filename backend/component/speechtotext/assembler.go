package speechtotext

// flushReason describes why the assembler emitted an utterance.
type flushReason int

const (
	flushNone flushReason = iota
	// flushSilence: a silence gap followed enough buffered speech.
	flushSilence
	// flushMaxDuration: the buffer hit its size cap without a silence gap
	// (e.g. the silence threshold sits below the ambient noise level).
	flushMaxDuration
)

// assemblerConfig holds the sizing rules for utterance assembly. All byte
// counts refer to PCM16 audio (2 bytes per sample).
type assemblerConfig struct {
	preRollChunks     int // chunks of leading audio kept from before speech starts
	silenceGapChunks  int // consecutive silent chunks that end an utterance
	minUtteranceBytes int // minimum buffered bytes before a silence gap may flush
	maxUtteranceBytes int // forced flush size cap
}

// utteranceAssembler turns a stream of audio chunks (each classified as
// silent or not) into silence-delimited utterance buffers.
//
// While idle it keeps a rolling pre-roll window of the most recent silent
// chunks; when speech starts, the pre-roll is prepended to the utterance so
// quiet onsets (like the "h" of "hey") are not clipped. Silence inside an
// utterance is kept so the audio stays contiguous; only silence before any
// speech is excluded (beyond the pre-roll).
//
// Not safe for concurrent use.
type utteranceAssembler struct {
	config assemblerConfig

	preRoll        [][]byte
	buffer         []byte
	silenceCounter int
}

func newUtteranceAssembler(config assemblerConfig) *utteranceAssembler {
	return &utteranceAssembler{config: config}
}

// Add feeds one chunk and its silence classification. When a complete
// utterance is ready it is returned along with the reason; otherwise the
// returned buffer is nil and the reason is flushNone.
func (a *utteranceAssembler) Add(chunk []byte, isSilent bool) ([]byte, flushReason) {
	if isSilent && len(a.buffer) == 0 {
		// Idle: remember recent audio as pre-roll for the next utterance
		a.preRoll = append(a.preRoll, chunk)
		if len(a.preRoll) > a.config.preRollChunks {
			a.preRoll = a.preRoll[1:]
		}
		return nil, flushNone
	}

	if len(a.buffer) == 0 {
		// Speech started: seed the utterance with the pre-roll
		for _, pre := range a.preRoll {
			a.buffer = append(a.buffer, pre...)
		}
		a.preRoll = nil
	}

	a.buffer = append(a.buffer, chunk...)

	if isSilent {
		a.silenceCounter++
		if a.silenceCounter >= a.config.silenceGapChunks && len(a.buffer) >= a.config.minUtteranceBytes {
			return a.flush(), flushSilence
		}
	} else {
		a.silenceCounter = 0
	}

	if len(a.buffer) >= a.config.maxUtteranceBytes {
		return a.flush(), flushMaxDuration
	}

	return nil, flushNone
}

// Pending returns the incomplete utterance accumulated so far, for draining
// at shutdown.
func (a *utteranceAssembler) Pending() []byte {
	return a.buffer
}

func (a *utteranceAssembler) flush() []byte {
	utterance := a.buffer
	a.buffer = nil
	a.silenceCounter = 0
	a.preRoll = nil
	return utterance
}
