package sqlite

import (
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestQueryLifecycleFilteringAndCursorRuntimeBinding", Label("persistence"), func() {
	t := GinkgoT()

	s, auth, _, _ := authFixture(t)
	_, owner := pairOwner(t, auth, "query owner")
	project, _ := service.NewID()
	other, _ := service.NewID()
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			for _, p := range []string{project, other} {
				if err := tx.CreateProject(domain.Project{ID: p, Name: p, Lifecycle: "archived"}); err != nil {
					return err
				}
			}
			for i := 0; i < 12; i++ {
				state := []string{"trash", "active", "purged"}[i%3]
				p := project
				if i%2 == 0 {
					p = other
				}
				if err := tx.CreateDocument(domain.Document{ID: fmt.Sprintf("document-%02d", i), ProjectID: p, Title: state + " private title", Lifecycle: state}); err != nil {
					return err
				}
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	core := query.New(s, auth, nil)
	seen := map[string]bool{}
	o := query.Options{Limit: 1}
	firstCursor := ""
	for {
		list, err := core.Documents(ctx, owner.Token, o)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		for _, d := range list.Items {
			Expect(d.Lifecycle != "active" || seen[d.ID]).To(BeFalse(), func() string { return fmt.Sprint("hidden/duplicate row leaked before cursor", d.ID) })

			seen[d.ID] = true
		}
		if list.NextCursor == nil {
			break
		}
		o.Cursor = *list.NextCursor
		if firstCursor == "" {
			firstCursor = o.Cursor
		}
	}
	Expect(len(seen) != 4).To(BeFalse(), func() string { return fmt.Sprintf("lifecycle filtering caused pagination holes %d/4", len(seen)) })
	{

		// This fixture changes the persisted generation marker only. It proves cursor
		// binding, not the independently owned complete restore/session invalidation.
		_, err := s.db.Exec("UPDATE libraries SET runtime_generation=?", "runtime-query-fixture-next")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	_, err := core.Documents(ctx, owner.Token, query.Options{Limit: 1, Cursor: firstCursor})
	var invalid *query.Error
	Expect(!errors.As(err, &invalid) || invalid.Reason != "cursor_mismatch").To(BeFalse(), func() string { return fmt.Sprint("old-generation cursor remained usable", err) })
	{

		_, err := s.db.Exec("UPDATE documents SET lifecycle='trash',revision=revision+1 WHERE document_id='document-01'")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := core.Document(ctx, owner.Token, "document-01")
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("current lifecycle did not guard item delivery", err) })
	}
	{

		err := s.Write(ctx, func(tx app.WriteTx) error {
			scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"project.read"}, ProjectIDs: []string{project}}
			rows, err := tx.QueryDocuments(scope, "", "", "", "", 1)
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0].ProjectID != project || rows[0].Lifecycle != "active" {
				Fail(fmt.Sprint("SQL scope/lifecycle filtering followed LIMIT"))
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})
