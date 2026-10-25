package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// APIError is a non-2xx response from the routeur.ai management API, decoded
// from its error envelope `{message, code, errors?}`.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Code is the stable machine code (validation_failed, not_found,
	// project_not_empty, …). Unknown codes must be treated by Status.
	Code string
	// Message is the human-readable explanation from the API.
	Message string
	// Fields holds per-field messages for validation_failed and
	// immutable_field, keyed by request field (dotted for nested fields).
	Fields map[string][]string
	// Feature and RequiredPlan are set on 403 feature_unavailable.
	Feature      string
	RequiredPlan string
	// RequestID is the X-Request-Id the client sent, for support requests.
	RequestID string
}

type errorEnvelope struct {
	Message      string              `json:"message"`
	Code         string              `json:"code"`
	Errors       map[string][]string `json:"errors"`
	Feature      string              `json:"feature"`
	RequiredPlan string              `json:"required_plan"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString(e.Message)
	if e.Code != "" {
		fmt.Fprintf(&b, " (HTTP %d, code %s)", e.Status, e.Code)
	} else {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.RequiredPlan != "" {
		fmt.Fprintf(&b, "; the %q feature needs the %q plan", e.Feature, e.RequiredPlan)
	}
	for _, k := range e.FieldNames() {
		fmt.Fprintf(&b, "\n  %s: %s", k, strings.Join(e.Fields[k], " "))
	}
	return b.String()
}

// FieldNames returns the keys of Fields, sorted, so output is stable.
func (e *APIError) FieldNames() []string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// newAPIError decodes the error envelope; a body that is not the envelope
// (a proxy's HTML page, say) still yields a useful error.
func newAPIError(status int, body []byte, requestID string) *APIError {
	e := &APIError{Status: status, RequestID: requestID}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && (env.Message != "" || env.Code != "") {
		e.Code, e.Message, e.Fields = env.Code, env.Message, env.Errors
		e.Feature, e.RequiredPlan = env.Feature, env.RequiredPlan
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
		if snippet := strings.TrimSpace(string(body)); snippet != "" {
			if len(snippet) > 200 {
				snippet = snippet[:200] + "…"
			}
			e.Message += ": " + snippet
		}
	}
	return e
}

// AsAPIError unwraps err to an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	ok := errors.As(err, &apiErr)
	return apiErr, ok
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.Status == http.StatusNotFound
}
