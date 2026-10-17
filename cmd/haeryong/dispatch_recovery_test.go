package main

import (
	"encoding/json"

	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	"haeryong.local/haeryong/internal/application/transfer"

	// The proxy forwards real authenticated requests to the production daemon and
	// closes one socket after its response has committed. It supplies no authority.
	"fmt"
	. "github.com/onsi/gomega"
)

func readCallerJournal(t GinkgoTInterface, root, id string) transfer.Journal {
	GinkgoHelper()
	b, e := os.ReadFile(filepath.Join(root, "journals", id+".json"))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var j transfer.Journal
	{
		e = json.Unmarshal(b, &j)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	return j
}
