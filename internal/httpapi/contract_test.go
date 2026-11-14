package httpapi_test

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
)

const contractPath = "../../api/openapi.yaml"

// loadContract reads the contract the way a tool would: from the file, with
// every reference resolved.
func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(contractPath)
	if err != nil {
		t.Fatalf("load %s: %v", contractPath, err)
	}
	return doc
}

// operation is one method on one path of the contract.
type operation struct {
	method string
	path   string
	item   *openapi3.PathItem
	spec   *openapi3.Operation
}

// operations lists every operation of the contract in a stable order.
func operations(doc *openapi3.T) []operation {
	var all []operation
	for _, path := range slices.Sorted(maps.Keys(doc.Paths.Map())) {
		item := doc.Paths.Find(path)
		byMethod := item.Operations()
		for _, method := range slices.Sorted(maps.Keys(byMethod)) {
			all = append(all, operation{method: method, path: path, item: item, spec: byMethod[method]})
		}
	}
	return all
}

// parameters returns the parameters of an operation, including the ones
// declared on its path.
func (o operation) parameters() []*openapi3.Parameter {
	var all []*openapi3.Parameter
	for _, list := range []openapi3.Parameters{o.item.Parameters, o.spec.Parameters} {
		for _, ref := range list {
			all = append(all, ref.Value)
		}
	}
	return all
}

func TestContract_IsValidOpenAPI(t *testing.T) {
	doc := loadContract(t)
	if doc.OpenAPI != "3.0.3" {
		t.Errorf("openapi version = %q, want 3.0.3", doc.OpenAPI)
	}
	// Validation also checks every example against its schema, including
	// string formats such as uuid and date.
	if err := doc.Validate(t.Context(), openapi3.EnableSchemaFormatValidation()); err != nil {
		t.Fatalf("the contract is not valid:\n%v", err)
	}
	if got := doc.Servers[0].URL; got != "/api/v1" {
		t.Errorf("server url = %q, want /api/v1", got)
	}
}

// The operations of v1, by tag. Adding or removing an operation is a
// contract change and must be made here on purpose.
func plannedOperations() map[string][]string {
	return map[string][]string{
		"auth":    {"getMe", "logout"},
		"uploads": {"uploadReceipts"},
		"receipts": {
			"createLineItem", "deleteLineItem", "deleteReceipt", "getReceipt", "getReceiptImage",
			"listReceipts", "reprocessReceipt", "updateLineItem", "updateReceipt",
		},
		"review": {"listReviewFlags", "resolveReviewFlag"},
		"products": {
			"getProduct", "getProductPriceHistory", "listProducts", "mergeProducts", "splitProduct", "updateProduct",
		},
		"stores":     {"listStores", "updateStore"},
		"categories": {"createCategory", "deleteCategory", "listCategories", "updateCategory"},
		"reports":    {"getMonthBreakdown", "getMonthlySpending"},
		"settings":   {"getSettings", "getUsage", "updateSettings"},
		"storage":    {"completeDrive", "connectDrive", "disconnectStorage", "getStorageStatus"},
	}
}

func TestContract_DefinesExactlyThePlannedOperations(t *testing.T) {
	doc := loadContract(t)
	got := map[string][]string{}
	for _, op := range operations(doc) {
		if len(op.spec.Tags) != 1 {
			t.Errorf("%s %s: has tags %v, want exactly one", op.method, op.path, op.spec.Tags)
			continue
		}
		got[op.spec.Tags[0]] = append(got[op.spec.Tags[0]], op.spec.OperationID)
	}
	for _, ids := range got {
		slices.Sort(ids)
	}
	if diff := cmp.Diff(plannedOperations(), got); diff != "" {
		t.Errorf("operations by tag mismatch (-want +got):\n%s", diff)
	}

	var declared []string
	for _, tag := range doc.Tags {
		declared = append(declared, tag.Name)
		if strings.TrimSpace(tag.Description) == "" {
			t.Errorf("tag %s has no description", tag.Name)
		}
	}
	slices.Sort(declared)
	if diff := cmp.Diff(slices.Sorted(maps.Keys(plannedOperations())), declared); diff != "" {
		t.Errorf("declared tags mismatch (-want +got):\n%s", diff)
	}
}

func TestContract_OperationsFollowTheConventions(t *testing.T) {
	doc := loadContract(t)
	camelCase := regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

	for _, op := range operations(doc) {
		t.Run(op.spec.OperationID, func(t *testing.T) {
			if !camelCase.MatchString(op.spec.OperationID) {
				t.Errorf("operationId %q is not camelCase", op.spec.OperationID)
			}
			if strings.TrimSpace(op.spec.Summary) == "" {
				t.Error("has no summary")
			}

			responses := op.spec.Responses.Map()
			success := 0
			for status, ref := range responses {
				if !strings.HasPrefix(status, "2") && status != "302" && status != "303" {
					// Every error is the shared problem document.
					media := ref.Value.Content.Get("application/problem+json")
					if media == nil || media.Schema.Ref != "#/components/schemas/Problem" {
						t.Errorf("response %s is not the shared Problem", status)
					}
					continue
				}
				success++
				if media := ref.Value.Content.Get("application/json"); media != nil && len(media.Examples) == 0 {
					t.Errorf("response %s has no example", status)
				}
			}
			if success != 1 {
				t.Errorf("has %d success responses, want exactly one", success)
			}
			for _, status := range []string{"401", "default"} {
				if _, ok := responses[status]; !ok {
					t.Errorf("does not declare the %s response", status)
				}
			}

			hasInput := op.spec.RequestBody != nil
			for _, parameter := range op.parameters() {
				hasInput = true
				if parameter.Example == nil && len(parameter.Examples) == 0 {
					t.Errorf("parameter %s has no example", parameter.Name)
				}
				if strings.TrimSpace(parameter.Description) == "" {
					t.Errorf("parameter %s has no description", parameter.Name)
				}
			}
			if _, ok := responses["400"]; hasInput && !ok {
				t.Error("takes input but does not declare the 400 response")
			}
			if op.spec.RequestBody != nil {
				if media := op.spec.RequestBody.Value.Content.Get("application/json"); media != nil && len(media.Examples) == 0 {
					t.Error("request body has no example")
				}
			}

			if strings.HasPrefix(op.spec.OperationID, "list") {
				names := map[string]bool{}
				for _, parameter := range op.parameters() {
					names[parameter.Name] = true
				}
				if !names["limit"] || !names["cursor"] {
					t.Error("a list operation must take limit and cursor")
				}
				page := responses["200"].Value.Content.Get("application/json").Schema.Value
				for _, property := range []string{"items", "nextCursor"} {
					if _, ok := page.Properties[property]; !ok || !slices.Contains(page.Required, property) {
						t.Errorf("a list response must always carry %s", property)
					}
				}
			}
		})
	}
}

// Money, quantities and shares travel as decimal strings. A numeric type
// for any of them would let a client round an amount.
func TestContract_MoneyIsNeverANumber(t *testing.T) {
	doc := loadContract(t)
	for _, name := range []string{"Money", "Quantity", "Ratio"} {
		schema := doc.Components.Schemas[name].Value
		if !schema.Type.Is("string") || schema.Pattern == "" {
			t.Errorf("%s must be a string with a pattern", name)
		}
	}
	amount := regexp.MustCompile(`(?i)(total|amount|price|subtotal|min|max|latest)$`)
	for name, ref := range doc.Components.Schemas {
		for property, propertyRef := range allProperties(ref.Value) {
			schema := propertyRef.Value
			if amount.MatchString(property) && (schema.Type.Is("number") || schema.Type.Is("integer")) {
				t.Errorf("%s.%s looks like an amount but is a number", name, property)
			}
		}
	}
}

// allProperties returns the properties of a schema, including the ones its
// allOf parts contribute.
func allProperties(schema *openapi3.Schema) map[string]*openapi3.SchemaRef {
	properties := map[string]*openapi3.SchemaRef{}
	maps.Copy(properties, schema.Properties)
	for _, part := range schema.AllOf {
		maps.Copy(properties, allProperties(part.Value))
	}
	return properties
}
