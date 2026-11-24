package assistant

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Slots are an intent's parameters: string fields tagged with the slot name
// they are filled from, matching the {name} placeholders in its phrases:
//
//	type Play struct{ Query string `slot:"query"` } // phrase "play {query}"
const slotTag = "slot"

var phraseSlot = regexp.MustCompile(`\{(\w+)\}`)

// Fill returns a copy of intent with its slot fields set from slots (slot
// name -> captured words), as produced by a recognizer. Every slot field
// must be filled and every slot must have a field; otherwise it returns a
// fault.ErrInvalidValue.
func Fill(intent Intent, slots map[string]string) (Intent, error) {
	v := reflect.ValueOf(intent)
	if v.Kind() != reflect.Struct {
		return nil, errors.WithMessagef(fault.ErrUnexpectedType, "intent %T is not a struct", intent)
	}
	fields, err := slotFields(v.Type())
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 && len(slots) == 0 {
		return intent, nil
	}

	filled := reflect.New(v.Type()).Elem()
	filled.Set(v)
	for name, index := range fields {
		value, ok := slots[name]
		if !ok {
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "intent %q: missing slot %q", intent.IntentName(), name)
		}
		filled.Field(index).SetString(value)
	}
	for name := range slots {
		if _, ok := fields[name]; !ok {
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "intent %q: no field for slot %q", intent.IntentName(), name)
		}
	}
	return filled.Interface().(Intent), nil
}

// slotFields maps slot names to field indexes of an intent struct type.
func slotFields(t reflect.Type) (map[string]int, error) {
	fields := make(map[string]int)
	for i := range t.NumField() {
		f := t.Field(i)
		name, ok := f.Tag.Lookup(slotTag)
		if !ok {
			continue
		}
		if f.Type.Kind() != reflect.String {
			return nil, errors.WithMessagef(fault.ErrUnexpectedType, "%s.%s: slot fields must be strings", t.Name(), f.Name)
		}
		fields[name] = i
	}
	return fields, nil
}

// checkSlots returns the intent type's slot names (sorted). It panics if a
// phrase uses a slot the type has no field for, or the type has slot fields
// that are not strings: programming errors found at registration, like a
// duplicate intent name.
func checkSlots(intent Intent, spec Spec) []string {
	fields, err := slotFields(reflect.TypeOf(intent))
	if err != nil {
		panic(fmt.Sprintf("assistant: intent %q: %v", intent.IntentName(), err))
	}
	for _, phrase := range spec.Phrases {
		for _, m := range phraseSlot.FindAllStringSubmatch(phrase, -1) {
			if _, ok := fields[m[1]]; !ok {
				panic(fmt.Sprintf("assistant: intent %q: phrase %q uses slot {%s}, but %T has no field tagged slot:%q",
					intent.IntentName(), phrase, m[1], intent, m[1]))
			}
		}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
