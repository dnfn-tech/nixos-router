package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

var errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Preserve endpoint-specific metadata and legacy aliases while adding the stable
// HTTP error contract. Successful job payloads are not HTTP errors.
func normalizeAPIError(status int, value interface{}) interface{} {
	body := map[string]interface{}{}
	switch v := value.(type) {
	case map[string]interface{}:
		for k, x := range v {
			body[k] = x
		}
	case map[string]string:
		for k, x := range v {
			body[k] = x
		}
	}
	code, _ := body["code"].(string)
	legacy, _ := body["error"].(string)
	if code == "" {
		code = strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
		if errorCodePattern.MatchString(legacy) {
			code = legacy
		}
	}
	message, _ := body["message"].(string)
	if message == "" {
		message = legacy
	}
	if message == "" {
		message = http.StatusText(status)
	}
	body["code"], body["message"] = code, message
	if body["fieldErrors"] == nil {
		body["fieldErrors"] = map[string]string{}
	}
	return body
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	writeJSON(w, status, map[string]interface{}{"code": code, "message": message, "fieldErrors": fields, "error": message, "errors": []string{message}})
}

// Callers supply fixed public messages, never raw system errors.
func apiHTTPError(w http.ResponseWriter, message string, status int) {
	code := strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
	writeAPIError(w, status, code, message, nil)
}

func writeConfigError(w http.ResponseWriter, err error) {
	fields := map[string]string{}
	var issue *config.ValidationError
	message := "配置无效，请检查输入。"
	if errors.As(err, &issue) {
		fields[issue.Path] = issue.Message
		message = issue.Error()
	}
	writeJSON(w, http.StatusBadRequest, map[string]interface{}{
		"code": "invalid_config", "error": "invalid_config", "message": message,
		"fieldErrors": fields, "errors": []string{message},
	})
}

// Bound request allocation before decoding, including chunked JSON/multipart.
// Authentication and CSRF checks run before this middleware.
func (s *Server) limitAPIBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		const limit = 16 << 20
		if r.ContentLength > limit {
			apiHTTPError(w, "request body exceeds 16 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			apiHTTPError(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		if len(data) > limit {
			apiHTTPError(w, "request body exceeds 16 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		next.ServeHTTP(w, r)
	})
}
