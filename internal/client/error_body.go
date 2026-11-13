// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"encoding/json"
	"net/http"
)

// redactedValidationKeys are the members of a validation error entry that
// echo the request back: input carries the submitted value, which can be a
// secret, and ctx the validator's context. Neither belongs in a diagnostic.
var redactedValidationKeys = []string{"input", "ctx"}

// redactErrorBody returns a 4xx response body for an error message with the
// request echo removed from every entry of a detail list. Any other body is
// returned as it came.
func redactErrorBody(statusCode int, body []byte) string {
	if statusCode < http.StatusBadRequest || statusCode >= http.StatusInternalServerError {
		return string(body)
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return string(body)
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(envelope["detail"], &entries) != nil {
		return string(body)
	}
	for _, entry := range entries {
		for _, key := range redactedValidationKeys {
			delete(entry, key)
		}
	}
	redactedDetail, encodeError := json.Marshal(entries)
	if encodeError != nil {
		return string(body)
	}
	envelope["detail"] = redactedDetail
	redacted, encodeError := json.Marshal(envelope)
	if encodeError != nil {
		return string(body)
	}
	return string(redacted)
}
