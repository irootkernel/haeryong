package importer

import (
	"bytes"
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"testing"
)

func TestCanonicalInventoryPreservesLiteralEscapesAndUnicodeSeparators(t *testing.T) {
	path := "literal\\u2028\\u2029-\u2028\u2029\t\n.txt"
	link := "link\\u2028\\u2029-\u2028\u2029\b\f\r"
	line, err := InventoryLine(domain.Entry{Path: path, Kind: "symlink", LinkText: link})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("[\"literal\\\\u2028\\\\u2029-\u2028\u2029\\t\\n.txt\",\"symlink\",\"link\\\\u2028\\\\u2029-\u2028\u2029\\b\\f\\r\"]\n")
	if !bytes.Equal(line, want) || !json.Valid(line) {
		t.Fatalf("canonical bytes changed or invalid JSON: %q", line)
	}
	var values []string
	if json.Unmarshal(line, &values) != nil || values[0] != path || values[2] != link {
		t.Fatalf("identity roundtrip changed: %q", values)
	}
}
