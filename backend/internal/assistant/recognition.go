package assistant

// Recognition is what a recognizer understood a transcript to mean: the
// intent to run and its slot values. Every recognizer (phrase templates,
// a local model, later the cloud) returns this, so the router and skills
// don't depend on how the intent was recognized.
type Recognition struct {
	Intent string            // intent name, as registered with Handle
	Slots  map[string]string // slot name -> value, for Fill; nil without slots
	Score  float64           // recognizer-specific confidence, 0..1
	Phrase string            // the phrase that matched, if the recognizer has one
	By     string            // which recognizer produced it, for logs ("templates", ...)
}
