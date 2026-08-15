// Package jsonio centralizes strict JSON and JSONL decoding plus canonical
// encoding. Strictness means unknown fields are rejected, trailing content
// after the top-level value is rejected, and byte offsets are reported so that
// operators can find the offending input quickly.
package jsonio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// MaxLineBytes bounds a single JSONL record to keep memory predictable.
const MaxLineBytes = 1 << 20

// DecodeStrict decodes exactly one JSON value from r into v.
func DecodeStrict(r io.Reader, name string, v any) error {
	dec := json.NewDecoder(r)
	if err := decodeInto(dec, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := ensureEOF(dec); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// decodeInto captures the first top-level value verbatim and then decodes it
// strictly. Splitting the two steps lets the caller check for trailing content
// even when the document itself decodes cleanly.
func decodeInto(dec *json.Decoder, v any) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("input is empty, expected a JSON document")
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	inner := json.NewDecoder(bytes.NewReader(raw))
	inner.DisallowUnknownFields()
	if err := inner.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", describe(err))
	}
	return nil
}

// ensureEOF rejects any additional token following the top-level value.
func ensureEOF(dec *json.Decoder) error {
	var extra json.RawMessage
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unexpected trailing content: %w", err)
	}
	trimmed := strings.TrimSpace(string(extra))
	if len(trimmed) > 32 {
		trimmed = trimmed[:32] + "..."
	}
	return fmt.Errorf("unexpected trailing JSON value %q after the document", trimmed)
}

// DecodeFile reads and strictly decodes a JSON document from disk.
func DecodeFile(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return DecodeStrict(f, path, v)
}

// DecodeBytes strictly decodes an in-memory JSON document.
func DecodeBytes(data []byte, name string, v any) error {
	return DecodeStrict(bytes.NewReader(data), name, v)
}

// LineHandler consumes one non-empty JSONL record. The line number is 1-based
// and counts every physical line including blanks.
type LineHandler func(lineNo int, raw []byte) error

// ForEachLine walks a JSONL file, skipping blank lines and passing every other
// line to fn. Lines longer than MaxLineBytes are rejected.
func ForEachLine(path string, fn LineHandler) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return ForEachLineIn(f, path, fn)
}

// ForEachLineIn is ForEachLine over an arbitrary reader.
func ForEachLineIn(r io.Reader, name string, fn LineHandler) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	lines := splitLines(data)
	for i, line := range lines {
		lineNo := i + 1
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if len(trimmed) > MaxLineBytes {
			return fmt.Errorf("%s:%d: record exceeds %d bytes", name, lineNo, MaxLineBytes)
		}
		if err := fn(lineNo, trimmed); err != nil {
			return fmt.Errorf("%s:%d: %w", name, lineNo, err)
		}
	}
	return nil
}

// splitLines splits on \n and strips a trailing \r so that CRLF files decode
// identically to LF files.
func splitLines(data []byte) [][]byte {
	raw := bytes.Split(data, []byte("\n"))
	out := make([][]byte, 0, len(raw))
	for _, line := range raw {
		out = append(out, bytes.TrimSuffix(line, []byte("\r")))
	}
	return out
}

// DecodeRecord strictly decodes a single JSONL record.
func DecodeRecord(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON record: %w", describe(err))
	}
	return ensureEOF(dec)
}

// Encode writes v as indented JSON with a trailing newline and no HTML
// escaping, which keeps documents diff friendly.
func Encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Marshal renders v the same way Encode does and returns the bytes.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := Encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalLine renders v as a single-line JSON record terminated by \n, the
// format used for every append-only ledger in the store.
func MarshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// describe unwraps json type errors into operator-friendly messages.
func describe(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "(top level)"
		}
		return fmt.Errorf("field %s expects %s but received %s", field, typeErr.Type, typeErr.Value)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("syntax error at byte offset %d: %s", syntaxErr.Offset, syntaxErr.Error())
	}
	return err
}
