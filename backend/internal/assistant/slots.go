package assistant

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Slots are an intent's parameters: string fields tagged with the slot name
// they are filled from, matching the {name} placeholders in its phrases:
//
//	type Play struct{ Query string `slot:"query"` } // phrase "play {query}"
//
// A slot tagged `slot:"name,optional"` may be left out: phrases without
// {name} leave the field empty, e.g. a reminder's message:
//
//	type SetTimer struct {
//		When    string `slot:"when"`
//		Message string `slot:"message,optional"`
//	}
const slotTag = "slot"

// optionalSlot is the tag option for a slot that may be left out.
const optionalSlot = "optional"

var phraseSlot = regexp.MustCompile(`\{(\w+)\}`)

// Slot describes one slot of an intent type, for recognizers.
type Slot struct {
	Name     string
	Optional bool
}

// slotField is a slot's field in the intent struct.
type slotField struct {
	index    int
	optional bool
}

// Fill returns a copy of intent with its slot fields set from slots (slot
// name -> captured words), as produced by a recognizer. Every required slot
// field must be filled (optional ones are left empty when missing) and
// every slot must have a field; otherwise it returns a
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
	for name, field := range fields {
		value, ok := slots[name]
		if !ok {
			if field.optional {
				continue
			}
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "intent %q: missing slot %q", intent.IntentName(), name)
		}
		filled.Field(field.index).SetString(value)
	}
	for name := range slots {
		if _, ok := fields[name]; !ok {
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "intent %q: no field for slot %q", intent.IntentName(), name)
		}
	}
	return filled.Interface().(Intent), nil
}

// slotFields maps slot names to the fields of an intent struct type.
func slotFields(t reflect.Type) (map[string]slotField, error) {
	fields := make(map[string]slotField)
	for i := range t.NumField() {
		f := t.Field(i)
		tag, ok := f.Tag.Lookup(slotTag)
		if !ok {
			continue
		}
		if f.Type.Kind() != reflect.String {
			return nil, errors.WithMessagef(fault.ErrUnexpectedType, "%s.%s: slot fields must be strings", t.Name(), f.Name)
		}
		name, option, _ := strings.Cut(tag, ",")
		if name == "" || (option != "" && option != optionalSlot) {
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "%s.%s: bad slot tag %q (want name or name,optional)",
				t.Name(), f.Name, tag)
		}
		fields[name] = slotField{index: i, optional: option == optionalSlot}
	}
	return fields, nil
}

// checkSlots returns the intent type's slots (sorted by name). It panics if
// a phrase uses a slot the type has no field for, or the type has slot
// fields that are not strings or have a malformed tag: programming errors
// found at registration, like a duplicate intent name.
func checkSlots(intent Intent, spec Spec) []Slot {
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
	slots := make([]Slot, 0, len(fields))
	for name, field := range fields {
		slots = append(slots, Slot{Name: name, Optional: field.optional})
	}
	slices.SortFunc(slots, func(a, b Slot) int { return strings.Compare(a.Name, b.Name) })
	return slots
}
