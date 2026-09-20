package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type responseTooLargeError struct {
	limit int64
}

func (e responseTooLargeError) Error() string {
	return fmt.Sprintf("registry response exceeds %d-byte limit", e.limit)
}

func decodeBoundedJSON(body io.Reader, contentLength int64, limit int64, target any) error {
	raw, err := readBoundedBody(body, contentLength, limit)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("registry response contains multiple JSON values")
		}
		return fmt.Errorf("registry response contains invalid trailing data: %w", err)
	}
	return nil
}

func readBoundedBody(body io.Reader, contentLength int64, limit int64) ([]byte, error) {
	if contentLength > limit {
		return nil, responseTooLargeError{limit: limit}
	}
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, responseTooLargeError{limit: limit}
	}
	return raw, nil
}
