package jsonio

import (
	"strings"
	"testing"
)

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	var out sample
	err := DecodeBytes([]byte(`{"name":"a","count":1,"extra":true}`), "doc", &out)
	if err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Fatalf("error should name the field: %v", err)
	}
}

func TestDecodeStrictRejectsTrailingValues(t *testing.T) {
	var out sample
	err := DecodeBytes([]byte(`{"name":"a","count":1} {"name":"b"}`), "doc", &out)
	if err == nil {
		t.Fatal("expected trailing content rejection")
	}
	if !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("error should mention trailing content: %v", err)
	}
}

func TestDecodeStrictRejectsEmptyInput(t *testing.T) {
	var out sample
	if err := DecodeBytes(nil, "doc", &out); err == nil {
		t.Fatal("expected empty input rejection")
	}
}

func TestDecodeStrictReportsTypeErrors(t *testing.T) {
	var out sample
	err := DecodeBytes([]byte(`{"name":"a","count":"many"}`), "doc", &out)
	if err == nil {
		t.Fatal("expected a type error")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Fatalf("error should name the field: %v", err)
	}
}

func TestDecodeStrictAcceptsValidDocument(t *testing.T) {
	var out sample
	if err := DecodeBytes([]byte(`{"name":"a","count":3}`), "doc", &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Name != "a" || out.Count != 3 {
		t.Fatalf("decoded = %+v", out)
	}
}

func TestForEachLineSkipsBlanksAndReportsLineNumbers(t *testing.T) {
	input := "{\"name\":\"a\",\"count\":1}\r\n\n{\"name\":\"b\",\"count\":2}\n"
	var names []string
	var lines []int
	err := ForEachLineIn(strings.NewReader(input), "records", func(lineNo int, raw []byte) error {
		var rec sample
		if err := DecodeRecord(raw, &rec); err != nil {
			return err
		}
		names = append(names, rec.Name)
		lines = append(lines, lineNo)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("names = %v", names)
	}
	if lines[0] != 1 || lines[1] != 3 {
		t.Fatalf("line numbers = %v, want 1 and 3", lines)
	}
}

func TestForEachLineWrapsErrorsWithLocation(t *testing.T) {
	input := "{\"name\":\"a\",\"count\":1}\n{\"name\":\"b\",\"nope\":2}\n"
	err := ForEachLineIn(strings.NewReader(input), "records", func(_ int, raw []byte) error {
		var rec sample
		return DecodeRecord(raw, &rec)
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "records:2") {
		t.Fatalf("error should point at the line: %v", err)
	}
}

func TestMarshalLineIsSingleLine(t *testing.T) {
	line, err := MarshalLine(sample{Name: "a", Count: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(string(line), "\n") != 1 || !strings.HasSuffix(string(line), "\n") {
		t.Fatalf("line = %q", line)
	}
}

func TestEncodeIsIndentedAndStable(t *testing.T) {
	first, err := Marshal(sample{Name: "a", Count: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, _ := Marshal(sample{Name: "a", Count: 1})
	if string(first) != string(second) {
		t.Fatal("encoding must be stable")
	}
	if !strings.Contains(string(first), "\n  \"name\"") {
		t.Fatalf("expected indented output, got %s", first)
	}
}
