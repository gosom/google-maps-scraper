package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func UnmarshalJSON[T any](r io.Reader, v *T) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return errors.New("input is empty or not valid JSON")
	}

	if err := json.Unmarshal(data, v); err != nil {
		return errors.New("failed to unmarshal JSON: " + err.Error())
	}

	return nil
}
func ResetReaderPosition(r io.Reader) error {
	// Assert that r is a Seeker
	sr, ok := r.(io.Seeker)
	if !ok {
		return fmt.Errorf("reader does not support seeking")
	}

	// Reset the position to the beginning
	if _, err := sr.Seek(0, io.SeekStart); err != nil {
		return err
	}

	return nil
}
