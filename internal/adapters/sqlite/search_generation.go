package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"

	"haeryong.local/haeryong/internal/domain"
	driverSQLite "modernc.org/sqlite"
	sqliteLib "modernc.org/sqlite/lib"
)

// Hooks belong to this Store, including every replacement connection. They
// observe the base tables, never FTS internals, and copy only row-key prefixes
// (no chunk text). Rollback discards observations; completed commits advance
// Store-owned generations without adding database writes or a second connection.
type searchConnector struct {
	driver.Connector
	generations *searchGenerations
}

// Preserve the driver's context, cancellation and bad-connection contracts while
// releasing its callback registrations whenever a physical connection closes.
type searchDriverConnection interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}
type searchConnection struct {
	searchDriverConnection
	hooks driverSQLite.HookRegisterer
}

func (c *searchConnection) Close() error {
	c.hooks.RegisterPreUpdateHook(nil)
	c.hooks.RegisterCommitHook(nil)
	c.hooks.RegisterRollbackHook(nil)
	return c.searchDriverConnection.Close()
}

func (c *searchConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	hooks, ok := conn.(driverSQLite.HookRegisterer)
	base, supported := conn.(searchDriverConnection)
	if !ok || !supported {
		conn.Close()
		return nil, fmt.Errorf("SQLite search generation hooks unavailable")
	}
	hooks.RegisterPreUpdateHook(c.generations.observe)
	hooks.RegisterCommitHook(func() int32 {
		g := c.generations
		g.mu.Lock()
		defer g.mu.Unlock()
		g.committed = true
		return 0
	})
	hooks.RegisterRollbackHook(func() {
		g := c.generations
		g.mu.Lock()
		defer g.mu.Unlock()
		g.pending = nil
		g.committed = false
	})
	return &searchConnection{base, hooks}, nil
}

type searchMutation struct {
	table     string
	old, next []any
}
type searchGenerationKey struct{ version, project, lifecycle, versions string }
type searchGenerationVersion struct {
	document, contentSet string
	published            bool
}
type searchGenerationDocument struct{ project, lifecycle, latest string }
type searchGenerationPage struct{ version, path string }
type searchGenerationUnit struct {
	version, kind, page, state, body string
	invalid                          bool
}
type searchGenerations struct {
	mu                       sync.Mutex
	enabled, committed       bool
	clock, base              uint64
	pending                  []searchMutation
	versions                 map[string]searchGenerationVersion
	documents                map[string]searchGenerationDocument
	units                    map[int64]searchGenerationUnit
	pages                    map[searchGenerationPage]int64
	quarantined              map[string]bool
	byDocument, bySet        map[string]map[string]bool
	values                   map[searchGenerationKey]uint64
	cachedKey                searchGenerationKey
	cachedScope              string
	cachedClock, cachedStamp uint64
}

func (g *searchGenerations) observe(d driverSQLite.SQLitePreUpdateData) {
	n := 0
	switch d.TableName {
	case "documents":
		n = 5
	case "document_versions":
		n = 5
	case "search_units":
		n = 12
	case "search_chunks":
		n = 3
	case "page_anchors":
		n = 2
	case "search_indexes":
		n = 6
	case "version_pages", "version_entries":
		n = 1
	default:
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return
	}
	// A preceding autocommit statement can have completed outside Store.Write
	// (adapter diagnostics). Publish it before recording the next transaction.
	g.flushLocked()
	m := searchMutation{table: d.TableName}
	if d.Op != sqliteLib.SQLITE_INSERT {
		m.old = make([]any, n)
		if err := d.Old(m.old...); err != nil {
			panic(err)
		}
	}
	if d.Op != sqliteLib.SQLITE_DELETE {
		m.next = make([]any, n)
		if err := d.New(m.next...); err != nil {
			panic(err)
		}
	}
	g.pending = append(g.pending, m)
}
func generationText(v any) string { s, _ := v.(string); return s }
func generationVersion(r []any) searchGenerationVersion {
	return searchGenerationVersion{generationText(r[1]), generationText(r[4]), r[2] != nil}
}
func generationDocument(r []any) searchGenerationDocument {
	return searchGenerationDocument{generationText(r[1]), generationText(r[3]), generationText(r[4])}
}
func generationUnit(r []any) searchGenerationUnit {
	u := domain.SearchUnit{VersionID: generationText(r[1]), Kind: generationText(r[2]), PagePath: generationText(r[3]), RegionID: generationText(r[4]), SourceID: generationText(r[5]), State: generationText(r[8]), BodyState: generationText(r[9]), AnchorState: generationText(r[10])}
	return searchGenerationUnit{u.VersionID, u.Kind, u.PagePath, u.State, u.BodyState, validateSearchUnit(u) != nil}
}
func generationMember(m map[string]map[string]bool, key, id string, add bool) {
	if add {
		if m[key] == nil {
			m[key] = map[string]bool{}
		}
		m[key][id] = true
	} else {
		delete(m[key], id)
		if len(m[key]) == 0 {
			delete(m, key)
		}
	}
}
func (g *searchGenerations) flush() { g.mu.Lock(); defer g.mu.Unlock(); g.flushLocked() }
func (g *searchGenerations) flushLocked() {
	if !g.committed {
		return
	}
	g.committed = false
	if len(g.pending) == 0 {
		return
	}
	g.clock++
	dirty := map[string]searchGenerationSelections{}
	// Collect both the removed and added projection before advancing buckets.
	for _, m := range g.pending {
		g.changedProjection(m, dirty)
	}
	for id, selections := range dirty {
		g.mark(id, selections)
	}
	for _, m := range g.pending {
		for side, r := range [][]any{m.old, m.next} {
			if r == nil {
				continue
			}
			add := side == 1
			switch m.table {
			case "documents":
				id := generationText(r[0])
				if add {
					g.documents[id] = generationDocument(r)
				} else {
					delete(g.documents, id)
				}
			case "document_versions":
				id := generationText(r[0])
				v := generationVersion(r)
				generationMember(g.byDocument, v.document, id, add)
				generationMember(g.bySet, v.contentSet, id, add)
				if add {
					g.versions[id] = v
				} else {
					delete(g.versions, id)
				}
			case "search_indexes":
				id := generationText(r[0])
				if add {
					g.quarantined[id] = generationText(r[5]) == "pending"
				} else {
					delete(g.quarantined, id)
				}
			case "search_units":
				id := r[0].(int64)
				u := generationUnit(r)
				if u.kind == "page" {
					key := searchGenerationPage{u.version, u.page}
					if add {
						g.pages[key] = id
					} else {
						delete(g.pages, key)
					}
				}
				if add {
					g.units[id] = u
				} else {
					delete(g.units, id)
				}
			}
		}
	}
	// Resolve inserted units/versions and the new latest/lifecycle as well.
	for _, m := range g.pending {
		g.changedProjection(m, dirty)
	}
	for id, selections := range dirty {
		g.mark(id, selections)
	}
	g.pending = nil
}

type searchGenerationSelections uint8

const (
	searchAllVersions searchGenerationSelections = 1 << iota
	searchLatestVersions
	searchEverySelection = searchAllVersions | searchLatestVersions
)

func (g *searchGenerations) changedProjection(m searchMutation, dirty map[string]searchGenerationSelections) {
	if m.table == "documents" {
		if m.old != nil && m.next != nil {
			old, next := generationDocument(m.old), generationDocument(m.next)
			if old == next {
				return
			} // Current document title/revision is not a version projection.
			if old.project == next.project && old.lifecycle == next.lifecycle {
				// A publication changes latest membership, not an unchanged older
				// version's all-version projection, even when the new version is hidden.
				dirty[old.latest] |= searchLatestVersions
				dirty[next.latest] |= searchLatestVersions
				return
			}
		}
		for _, r := range [][]any{m.old, m.next} {
			if r != nil {
				for id := range g.byDocument[generationText(r[0])] {
					dirty[id] |= searchEverySelection
				}
			}
		}
		return
	}
	for _, r := range [][]any{m.old, m.next} {
		if r == nil {
			continue
		}
		switch m.table {
		case "document_versions", "search_indexes", "version_pages":
			dirty[generationText(r[0])] |= searchEverySelection
		case "version_entries":
			for id := range g.bySet[generationText(r[0])] {
				dirty[id] |= searchEverySelection
			}
		case "page_anchors":
			u := g.units[g.pages[searchGenerationPage{generationText(r[0]), generationText(r[1])}]]
			if !g.quarantined[u.version] && u.state == "ready" && u.body == "complete" {
				dirty[u.version] |= searchEverySelection
			}
		case "search_units":
			u := generationUnit(r)
			if !g.quarantined[u.version] && (g.unitVisible(u) || u.invalid || u.state == "failed" || u.body == "failed") {
				dirty[u.version] |= searchEverySelection
			}
		case "search_chunks":
			u := g.units[r[1].(int64)]
			part := generationText(r[2])
			// Derived pending regions have no visible facts or metadata hash until
			// their parent page's O(1) EOF activation. Supplied ready regions retain
			// their independent metadata visibility. Unknown parts still invalidate.
			visible := part == "metadata" && g.unitVisible(u) || part == "body" && g.bodyVisible(u) || part != "metadata" && part != "body"
			if !g.quarantined[u.version] && visible {
				dirty[u.version] |= searchEverySelection
			}
		}
	}
}
func (g *searchGenerations) parentComplete(u searchGenerationUnit) bool {
	if u.kind != "region" {
		return false
	}
	parent := g.units[g.pages[searchGenerationPage{u.version, u.page}]]
	return parent.state == "ready" && parent.body == "complete"
}
func (g *searchGenerations) unitVisible(u searchGenerationUnit) bool {
	return u.state == "ready" || g.parentComplete(u)
}
func (g *searchGenerations) bodyVisible(u searchGenerationUnit) bool {
	return u.state == "ready" && u.body == "complete" || g.parentComplete(u)
}
func (g *searchGenerations) mark(id string, selections searchGenerationSelections) {
	v, ok := g.versions[id]
	if !ok || !v.published {
		return
	}
	d, ok := g.documents[v.document]
	if !ok || d.lifecycle == "trash" || d.lifecycle == "purged" {
		return
	}
	versions := []string{}
	if selections&searchAllVersions != 0 {
		versions = append(versions, "all")
	}
	if selections&searchLatestVersions != 0 && d.latest == id {
		versions = append(versions, "latest")
	}
	for _, version := range []string{"", id} {
		for _, project := range []string{"", d.project} {
			for _, lifecycle := range []string{"all", d.lifecycle} {
				for _, selection := range versions {
					g.values[searchGenerationKey{version, project, lifecycle, selection}] = g.clock
				}
			}
		}
	}
}
func (g *searchGenerations) stamp(o domain.SearchSelection) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.flushLocked()
	key := searchGenerationKey{project: o.Project, lifecycle: o.Lifecycle, versions: o.Versions}
	if g.cachedClock == g.clock && g.cachedKey == key && g.cachedScope == o.FenceKey {
		return g.cachedStamp
	}
	stamp := g.base
	if o.Scope.Kind == "owner" {
		if n := g.values[key]; n > stamp {
			stamp = n
		}
	} else if admits("version.read", o.Scope.Actions...) {
		projects := o.Scope.ProjectIDs
		if o.Project != "" {
			projects = nil
			if admits(o.Project, o.Scope.ProjectIDs...) {
				projects = []string{o.Project}
			}
		}
		for _, id := range o.Scope.VersionIDs {
			key.version = id
			for _, project := range projects {
				key.project = project
				if n := g.values[key]; n > stamp {
					stamp = n
				}
			}
		}
	}
	key.project = o.Project
	key.version = ""
	g.cachedKey = key
	g.cachedScope = o.FenceKey
	g.cachedClock = g.clock
	g.cachedStamp = stamp
	return stamp
}

// Reconstruct only relationship keys at profile open, in physical 64-row pages.
// This is not a query snapshot and retains no source/chunk content. The Store
// clock never resets, including Close/Prepare on the same Store instance.
func (g *searchGenerations) load(ctx context.Context, db *sql.DB) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = false
	g.versions = map[string]searchGenerationVersion{}
	g.documents = map[string]searchGenerationDocument{}
	g.units = map[int64]searchGenerationUnit{}
	g.pages = map[searchGenerationPage]int64{}
	g.quarantined = map[string]bool{}
	g.byDocument = map[string]map[string]bool{}
	g.bySet = map[string]map[string]bool{}
	g.values = map[searchGenerationKey]uint64{}
	for _, table := range []string{"documents", "document_versions", "search_indexes", "search_units"} {
		after := int64(0)
		for {
			columns := "document_id,project_id,title,lifecycle,latest_version_id"
			if table == "document_versions" {
				columns = "version_id,document_id,sequence,base_version_id,content_set_id"
			}
			if table == "search_indexes" {
				columns = "version_id,job_id,extractor_revision,revision,discovery_complete,state"
			}
			if table == "search_units" {
				columns = "unit_id,version_id,kind,page_path,region_id,source_id,title,input_hash,state,body_state,anchor_state,reason"
			}
			rows, err := db.QueryContext(ctx, "SELECT rowid,"+columns+" FROM "+table+" WHERE rowid>? ORDER BY rowid LIMIT ?", after, domain.SearchSliceRows)
			if err != nil {
				return err
			}
			count := 0
			for rows.Next() {
				n := 5
				if table == "search_indexes" {
					n = 6
				}
				if table == "search_units" {
					n = 12
				}
				r := make([]any, n)
				dest := make([]any, n+1)
				dest[0] = &after
				for i := range r {
					dest[i+1] = &r[i]
				}
				if err = rows.Scan(dest...); err != nil {
					rows.Close()
					return err
				}
				switch table {
				case "documents":
					g.documents[generationText(r[0])] = generationDocument(r)
				case "document_versions":
					id := generationText(r[0])
					v := generationVersion(r)
					g.versions[id] = v
					generationMember(g.byDocument, v.document, id, true)
					generationMember(g.bySet, v.contentSet, id, true)
				case "search_indexes":
					g.quarantined[generationText(r[0])] = generationText(r[5]) == "pending"
				case "search_units":
					id := r[0].(int64)
					u := generationUnit(r)
					g.units[id] = u
					if u.kind == "page" {
						g.pages[searchGenerationPage{u.version, u.page}] = id
					}
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if count == 0 {
				break
			}
		}
	}
	g.clock++
	g.base = g.clock
	g.pending = nil
	g.committed = false
	g.cachedClock = 0
	g.enabled = true
	return nil
}
