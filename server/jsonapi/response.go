package jsonapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type errResponse struct {
	Errors []*JSONError `json:"errors"`
}

type JSONError struct {
	ID     string `json:"id,omitempty"`
	Href   string `json:"href,omitempty"`
	Status string `json:"status,omitempty"`
	Code   string `json:"code,omitempty"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (e *JSONError) Error() string {
	return e.Detail
}

func newErrResponse(errors []error) *errResponse {
	r := &errResponse{Errors: []*JSONError{}}
	for _, err := range errors {
		switch err := err.(type) {
		case *JSONError:
			r.Errors = append(r.Errors, err)
		default:
			r.Errors = append(r.Errors, &JSONError{Detail: err.Error()})
		}
	}

	return r
}

func setContentType(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/vnd.api+json")
}

// Error takes a singular error and responds appropriately
func Error(w http.ResponseWriter, err error, status int) {
	Errors(w, []error{err}, status)
}

// Errors takes an array of errors and responds appropriately
func Errors(w http.ResponseWriter, errors []error, status int) {
	setContentType(w)

	b, err := json.MarshalIndent(newErrResponse(errors), "", "  ")
	if err != nil {
		http.Error(w, `{"errors":[{"status":"500","detail":"unable to marshal error message"}]}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
	fmt.Fprintf(w, string(b)+"\n")
}
