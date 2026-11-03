package loaders

import (
	"bufio"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"os"
)

// ScanJSONL streams one file, calling fn for each non-empty line.
func ScanJSONL(path string, fn func(line []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// lines hold full tool messages; allow generous buffer
	sc.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return sc.Err()
}

// DecodeJSONLStream decodes a stream of JSON values of type T from r using Go 1.27
// jsontext.Decoder and encoding/json/v2, calling fn for each decoded value.
func DecodeJSONLStream[T any](r io.Reader, fn func(val T) error) error {
	dec := jsontext.NewDecoder(r)
	for {
		var val T
		if err := jsonv2.UnmarshalDecode(dec, &val); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if err := fn(val); err != nil {
			return err
		}
	}
	return nil
}

// ScanJSONLStream opens path and streams JSON values using Go 1.27 encoding/json/v2.
// Malformed lines are skipped; lines exceeding the 32 MiB buffer limit surface as scan errors.
func ScanJSONLStream[T any](path string, fn func(val T) error) error {
	return ScanJSONL(path, func(line []byte) error {
		var val T
		if err := jsonv2.Unmarshal(line, &val); err != nil {
			return nil
		}
		return fn(val)
	})
}
