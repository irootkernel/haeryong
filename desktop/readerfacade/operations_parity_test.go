package readerfacade

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The closed native operation set is a cross-language contract: every named
// case dispatched by (*ReaderService).invoke must appear in the TypeScript
// NATIVE_OPERATIONS public list or the NativeWireOperation wire-only union,
// and vice versa. No fixed count is asserted. The gate fails on any
// duplicate, any missing entry in either direction, any overlap between the
// public and wire lists, and any non-literal the extractors cannot see.

const (
	nativeOperationsSource = "../../frontend/src/client/native.ts"
	nativeWireSource       = "../../frontend/src/client/native-wire.ts"
)

// operationSwitchVisitor collects the string literals of every switch whose
// tag is exactly the `operation` identifier, without descending into switch
// bodies (nested value selectors such as entry-kind or scope-kind switches
// are not operation dispatch) or closures (unrelated inner functions).
type operationSwitchVisitor struct {
	ops      []string
	problems []string
}

func (v *operationSwitchVisitor) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.FuncLit:
		return nil
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		if sw, ok := n.(*ast.SwitchStmt); ok && sw.Body != nil {
			if ident, ok := sw.Tag.(*ast.Ident); ok && ident.Name == "operation" {
				for _, stmt := range sw.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range clause.List {
						lit, ok := expr.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							v.problems = append(v.problems, fmt.Sprintf("non-literal operation case at %s", "fixture"))
							continue
						}
						name, err := strconv.Unquote(lit.Value)
						if err != nil {
							v.problems = append(v.problems, fmt.Sprintf("operation case literal %q: %v", lit.Value, err))
							continue
						}
						v.ops = append(v.ops, name)
					}
				}
			}
		}
		return nil
	}
	return v
}

// extractInvokeOperations parses Go source and returns every operation name
// dispatched by (*ReaderService).invoke's top-level operation switches.
func extractInvokeOperations(source string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "invoke.go", source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse Go source: %w", err)
	}
	var invoke *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "invoke" || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
			continue
		}
		if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "ReaderService" {
				invoke = fn
				break
			}
		}
	}
	if invoke == nil {
		return nil, fmt.Errorf("(*ReaderService).invoke not found")
	}
	visitor := &operationSwitchVisitor{}
	ast.Walk(visitor, invoke.Body)
	if len(visitor.problems) > 0 {
		return nil, fmt.Errorf("operation switch problems: %s", strings.Join(visitor.problems, "; "))
	}
	return visitor.ops, nil
}

var (
	publicOperationsPattern = regexp.MustCompile(`(?s)export const NATIVE_OPERATIONS = \[(.*?)\] as const;`)
	wireOperationsPattern   = regexp.MustCompile(`(?s)export type NativeWireOperation = (.*?);`)
	stringLiteralPattern    = regexp.MustCompile(`"([^"\n]*)"`)
)

func extractStringLiterals(block string) (names []string, residual string) {
	matches := stringLiteralPattern.FindAllStringSubmatchIndex(block, -1)
	var remaining []string
	last := 0
	for _, at := range matches {
		remaining = append(remaining, block[last:at[0]])
		names = append(names, block[at[2]:at[3]])
		last = at[1]
	}
	remaining = append(remaining, block[last:])
	return names, strings.Join(remaining, "")
}

// extractDeclaredOperations parses the two TypeScript declarations that own
// the closed operation list: the public NATIVE_OPERATIONS array (literal
// members only) and the NativeWireOperation union (literal members beside the
// `Operation` identifier). After literal extraction the array body may hold
// only comma/whitespace separators (the anchor already consumed the outer
// brackets, so any bracket is a nested member and must fail), and the union
// body may hold only `|`/whitespace separators with the exact standalone
// `Operation` identifier at most once — cutset-style trimming would silently
// accept lookalike identifiers such as OperationOperation.
func extractDeclaredOperations(nativeSrc, wireSrc string) (public, wire []string, err error) {
	publicBlock := publicOperationsPattern.FindStringSubmatch(nativeSrc)
	if publicBlock == nil {
		return nil, nil, fmt.Errorf("NATIVE_OPERATIONS declaration not found")
	}
	public, residual := extractStringLiterals(publicBlock[1])
	if rest := strings.Trim(residual, " \t\r\n,"); rest != "" {
		return nil, nil, fmt.Errorf("NATIVE_OPERATIONS carries a non-literal entry: %q", rest)
	}
	wireBlock := wireOperationsPattern.FindStringSubmatch(wireSrc)
	if wireBlock == nil {
		return nil, nil, fmt.Errorf("NativeWireOperation declaration not found")
	}
	wire, residual = extractStringLiterals(wireBlock[1])
	seenOperation := false
	for _, segment := range strings.Split(residual, "|") {
		switch token := strings.TrimSpace(segment); token {
		case "":
			continue
		case "Operation":
			if seenOperation {
				return nil, nil, fmt.Errorf("NativeWireOperation repeats the Operation identifier")
			}
			seenOperation = true
		default:
			return nil, nil, fmt.Errorf("NativeWireOperation carries a non-literal member: %q", token)
		}
	}
	return public, wire, nil
}

func duplicateNames(names []string) []string {
	seen := map[string]int{}
	for _, name := range names {
		seen[name]++
	}
	var duplicates []string
	for name, count := range seen {
		if count > 1 {
			duplicates = append(duplicates, fmt.Sprintf("%s×%d", name, count))
		}
	}
	sort.Strings(duplicates)
	return duplicates
}

// parityDifferences reports directional unknowns between the Go dispatch set
// and the declared public+wire set. missing lists declared names the facade
// never dispatches (labelled public or wire); extra lists Go cases with no
// declared TypeScript name.
func parityDifferences(goNames, public, wire []string) (missing, extra []string) {
	origin := map[string]string{}
	for _, name := range public {
		origin[name] = "public"
	}
	for _, name := range wire {
		if previous, ok := origin[name]; ok {
			origin[name] = previous + "+wire"
		} else {
			origin[name] = "wire"
		}
	}
	dispatched := map[string]bool{}
	for _, name := range goNames {
		dispatched[name] = true
	}
	for name, label := range origin {
		if !dispatched[name] {
			missing = append(missing, name+"("+label+")")
		}
	}
	for name := range dispatched {
		if _, ok := origin[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func TestReaderNativeOperationsParity(t *testing.T) {
	goSource, err := os.ReadFile("reader.go")
	if err != nil {
		t.Fatalf("read facade source: %v", err)
	}
	nativeSource, err := os.ReadFile(nativeOperationsSource)
	if err != nil {
		t.Fatalf("read NATIVE_OPERATIONS source: %v", err)
	}
	wireSource, err := os.ReadFile(nativeWireSource)
	if err != nil {
		t.Fatalf("read NativeWireOperation source: %v", err)
	}
	goNames, err := extractInvokeOperations(string(goSource))
	if err != nil {
		t.Fatalf("extract invoke operations: %v", err)
	}
	if duplicates := duplicateNames(goNames); len(duplicates) > 0 {
		t.Fatalf("duplicate Go operation cases: %s", strings.Join(duplicates, ", "))
	}
	public, wire, err := extractDeclaredOperations(string(nativeSource), string(wireSource))
	if err != nil {
		t.Fatalf("extract declared operations: %v", err)
	}
	if duplicates := duplicateNames(public); len(duplicates) > 0 {
		t.Fatalf("duplicate NATIVE_OPERATIONS entries: %s", strings.Join(duplicates, ", "))
	}
	if duplicates := duplicateNames(wire); len(duplicates) > 0 {
		t.Fatalf("duplicate NativeWireOperation members: %s", strings.Join(duplicates, ", "))
	}
	for _, name := range public {
		for _, wireName := range wire {
			if name == wireName {
				t.Fatalf("operation %q is declared both public and wire-only", name)
			}
		}
	}
	missing, extra := parityDifferences(goNames, public, wire)
	if len(missing) > 0 {
		t.Fatalf("declared operations the facade never dispatches: %s", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Fatalf("Go operation cases missing from the TypeScript declarations: %s", strings.Join(extra, ", "))
	}
	// The source/configuration family must stay exposed in all transports.
	for _, required := range []string{"versionBacklinks", "validateVersionIndexes", "rebuildVersionIndexes", "versionReferences", "resolveReference", "search", "configurationView", "configurationPlan", "configurationPlanCommit", "projectRegister", "projectSources", "retainedSourceReads", "verifySourceEvidence", "runSourceObservation"} {
		found := false
		for _, name := range public {
			if name == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("source operation missing: %s", required)
		}
	}
	// Descriptive only: the current sizes are facts, never asserted invariants.
	t.Logf("operation parity: go=%d public=%d wire=%d", len(goNames), len(public), len(wire))
}

func TestInvokeOperationSelectionExcludesNestedClosuresAndDetectsDuplicates(t *testing.T) {
	source := `package readerfacade

type ReaderService struct{}

func (s *ReaderService) invoke(operation string, payload []byte) {
	switch operation {
	case "alpha", "beta":
		switch operation {
		case "beta":
		}
	case "gamma":
		closure := func() {
			switch operation {
			case "hidden":
			}
		}
		_ = closure
	}
	switch in.Kind {
	case "file":
	}
	switch operation {
	case "alpha":
	}
}
`
	names, err := extractInvokeOperations(source)
	if err != nil {
		t.Fatalf("fixture extraction failed: %v", err)
	}
	if strings.Join(names, ",") != "alpha,beta,gamma,alpha" {
		t.Fatalf("unexpected selection %v: nested/closure/wrong-tag cases must be excluded", names)
	}
	if duplicates := duplicateNames(names); strings.Join(duplicates, ",") != "alpha×2" {
		t.Fatalf("duplicate detection failed: %v", duplicates)
	}
}

func TestDeclaredOperationExtractionAndDirectionalDiffs(t *testing.T) {
	public, wire, err := extractDeclaredOperations(
		"export const NATIVE_OPERATIONS = [\n  \"pair\",\n  \"pair\",\n] as const;\n",
		"export type NativeWireOperation = Operation | \"streamRead\";\n",
	)
	if err != nil {
		t.Fatalf("literal-only duplicate entries must still extract: %v", err)
	}
	if strings.Join(public, ",") != "pair,pair" || strings.Join(wire, ",") != "streamRead" {
		t.Fatalf("unexpected extraction public=%v wire=%v", public, wire)
	}
	if duplicates := duplicateNames(public); strings.Join(duplicates, ",") != "pair×2" {
		t.Fatalf("duplicate public entry detection failed: %v", duplicates)
	}
	if _, _, err := extractDeclaredOperations(
		"export const NATIVE_OPERATIONS = [\n  SOME_CONSTANT,\n] as const;\n",
		"export type NativeWireOperation = Operation | \"streamRead\";\n",
	); err == nil || !strings.Contains(err.Error(), "non-literal entry") {
		t.Fatalf("identifier member must fail as non-literal, got %v", err)
	}
	if _, _, err := extractDeclaredOperations(
		"export const NATIVE_OPERATIONS = [\n  \"pair\",\n] as const;\n",
		"export type NativeWireOperation = Operation | OtherOperation | \"streamRead\";\n",
	); err == nil || !strings.Contains(err.Error(), "non-literal member") {
		t.Fatalf("second union identifier must fail as non-literal, got %v", err)
	}
	// Cutset-lookalike identifier: every character of OperationOperation is a
	// member of the naive |Operation cutset, so only exact-token validation
	// can reject it.
	if _, _, err := extractDeclaredOperations(
		"export const NATIVE_OPERATIONS = [\n  \"pair\",\n] as const;\n",
		"export type NativeWireOperation = Operation | OperationOperation | \"streamRead\";\n",
	); err == nil || !strings.Contains(err.Error(), "OperationOperation") {
		t.Fatalf("cutset-lookalike union identifier must be named and rejected, got %v", err)
	}
	if _, _, err := extractDeclaredOperations(
		"export const NATIVE_OPERATIONS = [\n  \"pair\",\n] as const;\n",
		"export type NativeWireOperation = Operation | Operation | \"streamRead\";\n",
	); err == nil || !strings.Contains(err.Error(), "repeats the Operation identifier") {
		t.Fatalf("a repeated exact Operation token must fail, got %v", err)
	}
	// The anchor consumes only the outer brackets; a nested array member
	// leaves brackets in the residual and must fail as non-literal.
	if _, _, err := extractDeclaredOperations(
		"export const NATIVE_OPERATIONS = [\n  [\"inner\"],\n] as const;\n",
		"export type NativeWireOperation = Operation | \"streamRead\";\n",
	); err == nil || !strings.Contains(err.Error(), "non-literal entry") {
		t.Fatalf("nested array member must fail as non-literal, got %v", err)
	}

	missing, extra := parityDifferences([]string{"alpha", "beta"}, []string{"alpha"}, []string{"beta", "wireOnly"})
	if strings.Join(missing, ",") != "wireOnly(wire)" || len(extra) != 0 {
		t.Fatalf("declared-but-never-dispatched detection failed: missing=%v extra=%v", missing, extra)
	}
	missing, extra = parityDifferences([]string{"alpha", "beta", "undocumented"}, []string{"alpha"}, []string{"beta"})
	if len(missing) != 0 || strings.Join(extra, ",") != "undocumented" {
		t.Fatalf("dispatched-but-undeclared detection failed: missing=%v extra=%v", missing, extra)
	}
	missing, extra = parityDifferences([]string{"alpha", "beta"}, []string{"alpha", "shared"}, []string{"beta", "shared"})
	if strings.Join(missing, ",") != "shared(public+wire)" || len(extra) != 0 {
		t.Fatalf("overlap labelling failed: missing=%v extra=%v", missing, extra)
	}
}
