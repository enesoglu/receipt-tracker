package httpapi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestIsolationAudit_CoversEveryOperationAndNamesExistingTests(t *testing.T) {
	data, err := os.ReadFile("../../docs/ISOLATION-AUDIT.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`(?m)^\|\s+(\w+)\s+\|\s+(Test\w+)\s+\|`).FindAllStringSubmatch(string(data), -1)
	audit := map[string]string{}
	for _, row := range rows {
		if _, exists := audit[row[1]]; exists {
			t.Errorf("duplicate audit entry: %s", row[1])
		}
		audit[row[1]] = row[2]
	}
	tests := map[string]bool{}
	err = filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !regexp.MustCompile(`_test\.go$`).MatchString(path) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			if fn, ok := declaration.(*ast.FuncDecl); ok {
				tests[fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range operations(loadContract(t)) {
		name := audit[op.spec.OperationID]
		if name == "" || !tests[name] {
			t.Errorf("%s needs an isolation audit entry naming an existing test", op.spec.OperationID)
		}
		delete(audit, op.spec.OperationID)
	}
	protocol, err := openapi3.NewLoader().LoadFromFile("../oauthserver/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range operations(protocol) {
		name := audit[op.spec.OperationID]
		if name == "" || !tests[name] {
			t.Errorf("%s needs an OAuth boundary audit entry naming an existing test", op.spec.OperationID)
		}
		delete(audit, op.spec.OperationID)
	}
	for _, name := range []string{"list_receipts", "get_receipt", "search_products", "get_price_history", "get_spending_summary", "list_categories", "list_stores"} {
		if audit[name] != "TestMCP_EveryToolIsolatesUsers" || !tests[audit[name]] {
			t.Errorf("%s needs its MCP isolation test", name)
		}
		delete(audit, name)
	}
	if len(audit) != 0 {
		t.Errorf("audit contains unknown operations: %v", audit)
	}
}
