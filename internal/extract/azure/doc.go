// Package azure reads receipts with Azure Document Intelligence, REST API
// version 2024-11-30, model prebuilt-receipt, through the standard library
// HTTP client.
//
// # Protocol (verified against the 2024-11-30 REST reference and OpenAPI spec)
//
// Submit: POST {endpoint}/documentintelligence/documentModels/prebuilt-receipt:analyze?api-version=2024-11-30
// with the image as the body ("Analyze Document From Stream"; image/jpeg and
// image/png are accepted media types) and the key in the
// Ocp-Apim-Subscription-Key header. The answer is 202 Accepted with an
// Operation-Location header naming the result and a Retry-After header in
// seconds. Any other status carries {"error": {"code", "message",
// "innererror": {"code", "message"}}}.
//
// Poll: GET the Operation-Location URL with the same key. 200 returns an
// AnalyzeOperation whose status is notStarted, running, succeeded, failed,
// canceled or skipped; a failed operation carries "error", a succeeded one
// "analyzeResult".
//
// Free tier (F0) limits from the service limits page: 1 analyze request per
// second, 1 get request per second, 4 MB per document, 2 pages analyzed; the
// pricing page grants 500 free pages per month. The limits page recommends
// polling no more than once every 2 seconds and honouring Retry-After, and
// answers 429 when a rate is exceeded. The client therefore spaces
// submissions at least 1 s apart, polls at most every 2 s or as Retry-After
// asks, retries 429 and 5xx with exponential backoff, and treats every other
// 4xx as permanent, keeping the service's error code.
//
// # Field values (receipt schema 2024-11-30 GA and the DocumentField definition)
//
// Each field of analyzeResult.documents[0].fields is a DocumentField with
// "type", "content", "confidence" and one value property:
//
//   - currency: "valueCurrency": {"amount": <number>, "currencySymbol":
//     "<string>", "currencyCode": "<ISO 4217>"}; only amount is required.
//     Used for Total, Subtotal, TotalTax, Items.*.Price, Items.*.TotalPrice.
//   - number: "valueNumber": <JSON number> (a double). Items.*.Quantity.
//   - date: "valueDate": "YYYY-MM-DD". TransactionDate.
//   - time: "valueTime": "hh:mm:ss". TransactionTime.
//   - string: "valueString": "<text>". MerchantName, Items.*.Description,
//     Items.*.ProductCode, Items.*.QuantityUnit.
//   - array: "valueArray": [DocumentField, ...]. Items, whose elements are
//     type "object" with "valueObject": {"Description": DocumentField, ...}.
//
// Numbers are read from their JSON text into decimals, never through
// float64, so amounts stay exact. A field that is absent, or whose value
// property is missing or malformed, is reported as not found.
package azure
