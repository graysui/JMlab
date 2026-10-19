package userdata

import (
	"bytes"
	"encoding/json"
)

// JSON null clears a rating; an omitted field preserves it. Go callers can
// continue using Rating pointers without changing unrelated state mutations.
func (input *StateInput) UnmarshalJSON(data []byte) error {
	type fields StateInput
	var value fields
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}
	*input = StateInput(value)
	_, input.ratingPresent = present["rating"]
	return nil
}
