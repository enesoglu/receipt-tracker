package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/products"
	"github.com/enesoglu/receipt-tracker/internal/receipts"
	"github.com/enesoglu/receipt-tracker/internal/reports"
)

// handlers implements the generated StrictServerInterface by combining one
// small type per API tag. The methods of a tag live in the file named after
// it (receipts.go, products.go, ...), so the package that builds a tag
// changes that file and adds its dependencies to that type only.
type handlers struct {
	authHandlers
	uploadsHandlers
	pipelineHandlers
	receiptsHandlers
	reviewHandlers
	suggestionsHandlers
	productsHandlers
	storesHandlers
	categoriesHandlers
	reportsHandlers
	settingsHandlers
	usageHandlers
	storageHandlers
}

// serviceUnavailable classifies a required service missing from the wiring.
func serviceUnavailable() error {
	return apperr.New(apperr.Unavailable, "service is unavailable")
}

// mountAPI registers every operation of the contract on router. A handler
// only returns a response object or an error. All three ways a request can
// fail end in the problem writer: a parameter that cannot be bound, a body
// that cannot be decoded, and an error from the handler.
func mountAPI(router chi.Router, problems problemWriter, authentication authHandlers, storage storageHandlers, ingestion Ingestion, reportService *reports.Service, productService *products.Service, receiptService *receipts.Service) {
	all := handlers{
		authHandlers: authentication, storageHandlers: storage,
		pipelineHandlers:    pipelineHandlers{receipts: receiptService},
		receiptsHandlers:    receiptsHandlers{receipts: receiptService},
		reviewHandlers:      reviewHandlers{receipts: receiptService},
		suggestionsHandlers: suggestionsHandlers{receipts: receiptService},
		storesHandlers:      storesHandlers{receipts: receiptService},
		categoriesHandlers:  categoriesHandlers{receipts: receiptService},
		settingsHandlers:    settingsHandlers{receipts: receiptService},
		reportsHandlers:     reportsHandlers{reports: reportService},
		productsHandlers:    productsHandlers{products: productService},
		uploadsHandlers:     uploadsHandlers{uploads: ingestion.Uploads}, usageHandlers: usageHandlers{usage: ingestion.Usage},
	}
	strict := NewStrictHandlerWithOptions(all, []StrictMiddlewareFunc{uploadDeadlines}, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problems.write(w, r, apperr.Wrap(apperr.Invalid, detailBodyShape, err))
		},
		ResponseErrorHandlerFunc: problems.write,
	})
	HandlerWithOptions(strict, ChiServerOptions{BaseRouter: router, ErrorHandlerFunc: problems.write})
}
