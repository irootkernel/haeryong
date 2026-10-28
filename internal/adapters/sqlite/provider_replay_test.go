//go:build haeryong_provider_replay

package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"os"
	"path/filepath"
)

// This reads an already retained native observation. It starts no native client,
// sends no request and cannot establish a fresh real execution of these bytes.
var _ = It("PA23 retained native acceptance IDs replay through current durable progress without another provider turn", Label("persistence"), func() {
	path := os.Getenv("HAERYONG_PROVIDER_REPLAY_RECEIPT")
	out := os.Getenv("HAERYONG_PROVIDER_REPLAY_EVIDENCE")
	Expect(filepath.IsAbs(path) && filepath.IsAbs(out)).To(BeTrue())
	data, e := os.ReadFile(path)
	Expect(e).NotTo(HaveOccurred())
	var receipt struct {
		Accepted struct {
			Correlation json.RawMessage `json:"request_id"`
			Thread      string          `json:"thread_id"`
			Turn        string          `json:"turn_id"`
		} `json:"accepted"`
	}
	Expect(json.Unmarshal(data, &receipt)).To(Succeed())
	Expect(receipt.Accepted.Thread != "" && receipt.Accepted.Turn != "").To(BeTrue())
	var correlation string
	_ = json.Unmarshal(receipt.Accepted.Correlation, &correlation)
	Expect(correlation != receipt.Accepted.Turn).To(BeTrue(), "RPC correlation must stay distinct from backend operation identity")
	s, a := providerFixture(false)
	profile := s.profile
	d := &providerFake{requestID: receipt.Accepted.Turn, sessionID: receipt.Accepted.Thread, noInputObservation: true, afterProgress: func() error {
		if e := s.Close(); e != nil {
			return e
		}
		return context.Canceled
	}}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	s = New(nil)
	_, closeDB, e := s.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		Expect(len(rows) == 1 && rows[0].RequestID == receipt.Accepted.Turn && rows[0].SessionID == receipt.Accepted.Thread && rows[0].State == "outcome_unknown").To(BeTrue())
		inputs, e := tx.ProviderInputs(rows[0].ID)
		if e != nil {
			return e
		}
		Expect(inputs[0].ObservedOrigin).To(BeEmpty())
		Expect(inputs[0].ObservedAt).To(BeNil())
		return nil
	})).To(Succeed())
	hash := sha256.Sum256(data)
	proof := jsonBytes(map[string]any{"kind": "replay_of_retained_native_acceptance", "receipt_sha256": fmt.Sprintf("%x", hash), "new_native_turns": 0, "provider_turn_and_thread_persisted_before_interruption": true, "new_input_observation_claimed": false})
	Expect(os.WriteFile(out, proof, 0600)).To(Succeed())
})
