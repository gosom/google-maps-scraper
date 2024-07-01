package utils

import (
	"io"
)

func IsJson(reader io.Reader) (bool, error) {
	buf := make([]byte, 1024)
	n, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}

	if n == 0 || buf[0] != '{' && buf[0] != '[' {
		return false, nil // Indicate not JSON or empty
	}

	return true, nil
}
