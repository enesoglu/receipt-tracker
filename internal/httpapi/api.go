package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// handlers implements the generated StrictServerInterface by combining one
// small type per API tag. The methods of a tag live in the file named after
// it (receipts.go, products.go, ...), so the package that builds a tag
// changes that file and adds its dependencies to that type only.
type handlers struct {
	authHandlers
	uploadsHandlers
	receiptsHandlers
	reviewHandlers
	productsHandlers
	storesHandlers
	categoriesHandlers
	reportsHandlers
	settingsHandlers
	storageHandlers
}

// notImplemented is what an operation returns until its handler is built.
// The contract already describes the operation, so clients can be developed
// against it; the server answers 501 until the behaviour exists.
func notImplemented() error {
	return &statusError{status: http.StatusNotImplemented, detail: "this operation is not implemented yet"}
}

// mountAPI registers every operation of the contract on router. A handler
// only returns a response object or an error. All three ways a request can
// fail end in the problem writer: a parameter that cannot be bound, a body
// that cannot be decoded, and an error from the handler.
func mountAPI(router chi.Router, problems problemWriter) {
	strict := NewStrictHandlerWithOptions(handlers{}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problems.write(w, r, apperr.Wrap(apperr.Invalid, detailBodyShape, err))
		},
		ResponseErrorHandlerFunc: problems.write,
	})
	HandlerWithOptions(strict, ChiServerOptions{BaseRouter: router, ErrorHandlerFunc: problems.write})
}
