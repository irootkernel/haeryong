package source_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("SCAP42 real Runner protected stop and committed controlled restart settle cooperative accepted capture before writer release", Label("persistence"), func() {
	for _, mode := range []string{"protected_stop", "controlled_restart"} {
		selection := service.Selection{Profile: "lifetime", DataDir: filepath.Join(GinkgoT().TempDir(), "profile")}
		profile, e := service.Initialize(selection)
		Expect(e).NotTo(HaveOccurred())
		store := foundation.New(nil)
		auth := appauth.New(store)
		core := source.New(store, auth, store, &fakeReader{})
		life, cancel := context.WithCancel(context.Background())
		core.Lifetime = life
		entered := make(chan struct{})
		var calls atomic.Int32
		body := "staged cooperative lifetime bytes"
		core.Files = inspectedCaptureFiles{open: func(ctx context.Context, _, _ string) (io.ReadCloser, error) {
			if calls.Add(1) == 1 {
				return io.NopCloser(strings.NewReader(body)), nil
			}
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		var token, project string
		key := sid()
		sourceID := sid()
		var result domain.Response
		endpoint := &localcontrol.HTTP{Auth: auth, BeforeDrain: cancel, Product: func(origin string) (http.Handler, error) {
			pair, e := auth.CreatePairing(context.Background(), origin, "fixture", "fixture")
			if e != nil {
				return nil, e
			}
			_, e = auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: sid(), Action: "approve"})
			if e != nil {
				return nil, e
			}
			session, e := auth.Poll(context.Background(), pair.ID, pair.PollingSecret, pair.Origin)
			if e != nil {
				return nil, e
			}
			token = session.Token
			health, e := store.Health(context.Background())
			if e != nil {
				return nil, e
			}
			project = health.Library.UnfiledProjectID
			e = store.Write(context.Background(), func(tx app.WriteTx) error {
				cfg, e := tx.Configuration()
				if e != nil {
					return e
				}
				var v map[string]any
				if e = json.Unmarshal(cfg.Value, &v); e != nil {
					return e
				}
				v["projects"] = map[string]any{project: map[string]any{"sources": []map[string]any{{"source_id": sourceID, "kind": "files", "root_path": GinkgoT().TempDir(), "repo_id": nil, "retention_mode": "reference_only", "provider_ids": []string{}}}}}
				cfg.Value, _ = json.Marshal(v)
				return tx.SetConfiguration(cfg, cfg.Revision)
			})
			if e != nil {
				return nil, e
			}
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response, e := core.Capture(r.Context(), token, key, project, managedRequest(sourceID, "one", "two", "three"))
				if e != nil {
					w.WriteHeader(500)
					return
				}
				result = response
				w.WriteHeader(response.Status)
				_, _ = w.Write(response.Body)
			}), nil
		}}
		listener, e := net.Listen("tcp4", "127.0.0.1:0")
		Expect(e).NotTo(HaveOccurred())
		port := listener.Addr().(*net.TCPAddr).Port
		Expect(listener.Close()).To(Succeed())
		runner := service.Runner{Selection: selection, Port: port, Storage: store, Endpoint: endpoint}
		ready := make(chan service.Identity, 1)
		done := make(chan error, 1)
		go func() { defer GinkgoRecover(); done <- runner.Run(life, func(id service.Identity) { ready <- id }) }()
		stopped := false
		defer func() {
			cancel()
			if !stopped {
				Eventually(done, 10*time.Second).Should(Receive())
			}
		}()
		var identity service.Identity
		Eventually(ready, 20*time.Second).Should(Receive(&identity))
		received := make(chan int, 1)
		go func() {
			response, e := http.Get(identity.Endpoint + "/capture")
			if e != nil {
				received <- 0
				return
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			received <- response.StatusCode
		}()
		Eventually(entered, 10*time.Second).Should(BeClosed())
		if mode == "protected_stop" {
			Expect(localcontrol.Stop(context.Background(), profile, identity)).To(Succeed())
		} else {
			configuration := config.New(store, auth, configpath.Resolver{})
			configuration.Restart = cancel
			current, e := store.Configuration(context.Background())
			Expect(e).NotTo(HaveOccurred())
			choices, e := config.Decode(strings.NewReader(string(current.Value)), "json")
			Expect(e).NotTo(HaveOccurred())
			network := choices["network"].(map[string]any)
			network["listener"].(map[string]any)["port"] = json.Number("9272")
			network["allowed_origins"] = []any{"http://127.0.0.1:9272"}
			planned, e := configuration.Plan(context.Background(), token, sid(), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: current.Revision, Changes: choices})
			Expect(e).NotTo(HaveOccurred())
			var plan struct {
				ID string `json:"plan_id"`
			}
			Expect(json.Unmarshal(planned.Body, &plan)).To(Succeed())
			_, e = configuration.Commit(context.Background(), token, plan.ID, "", config.CommitRequest{ExpectedRevision: current.Revision, Confirmed: true})
			Expect(e).NotTo(HaveOccurred())
		}
		Eventually(received, 10*time.Second).Should(Receive(Equal(503)))
		Eventually(done, 10*time.Second).Should(Receive(BeNil()))
		stopped = true
		Expect(calls.Load()).To(Equal(int32(2)), "no third selected source read after lifetime cancellation")
		var envelope map[string]any
		Expect(json.Unmarshal(result.Body, &envelope)).To(Succeed())
		stamp, ok := envelope["request_id"].(string)
		Expect(ok).To(BeTrue())
		Expect(stamp).NotTo(BeEmpty())
		Expect(envelope).To(Equal(map[string]any{"request_id": stamp, "error": map[string]any{"code": "SERVICE_RECOVERING", "message": "source check failed: interrupted_before_commit", "retryable": false, "details": map[string]any{"reason": "interrupted_before_commit"}}}))
		// Reopening proves the real Runner released its writer only after settlement.
		writer, e := service.OpenWriter(selection)
		Expect(e).NotTo(HaveOccurred())
		_, closeDB, e := store.Prepare(context.Background(), writer.Profile)
		Expect(e).NotTo(HaveOccurred())
		func() {
			defer writer.Close()
			defer closeDB()
			actor, e := auth.Authenticate(context.Background(), token)
			Expect(e).NotTo(HaveOccurred())
			receipt, e := store.Receipt(context.Background(), domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key})
			Expect(e).NotTo(HaveOccurred())
			Expect(*receipt.Response).To(Equal(result))
			var ids map[string]string
			Expect(json.Unmarshal(receipt.Normalization, &ids)).To(Succeed())
			view, e := core.CaptureView(context.Background(), token, ids["capture_id"], "")
			Expect(e).NotTo(HaveOccurred())
			Expect(view["complete"]).To(BeFalse())
			Expect(view["target_set_id"]).To(BeNil())
			Expect(view["items"]).To(HaveLen(3))
			replay, e := core.Capture(context.Background(), token, key, project, managedRequest(sourceID, "one", "two", "three"))
			Expect(e).NotTo(HaveOccurred())
			Expect(replay).To(Equal(result))
			Expect(calls.Load()).To(Equal(int32(2)))
			Expect(store.Collect(context.Background(), domain.Object{Hash: vsha([]byte(body)), Size: int64(len(body))})).To(Succeed(), "settled capture leaves no execution-only pin")
		}()
	}
})
