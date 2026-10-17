package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestCanonicalInventoryPreservesLiteralEscapesAndUnicodeSeparators", Label("unit"), func() {

	path := "literal\\u2028\\u2029-\u2028\u2029\t\n.txt"
	link := "link\\u2028\\u2029-\u2028\u2029\b\f\r"
	line, err := InventoryLine(domain.Entry{Path: path, Kind: "symlink", LinkText: link})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	want := []byte("[\"literal\\\\u2028\\\\u2029-\u2028\u2029\\t\\n.txt\",\"symlink\",\"link\\\\u2028\\\\u2029-\u2028\u2029\\b\\f\\r\"]\n")
	Expect(!bytes.Equal(line, want) || !json.Valid(line)).To(BeFalse(), func() string { return fmt.Sprintf("canonical bytes changed or invalid JSON: %q", line) })

	var values []string
	Expect(json.Unmarshal(line, &values) != nil || values[0] != path || values[2] != link).To(BeFalse(), func() string { return fmt.Sprintf("identity roundtrip changed: %q", values) })

})
