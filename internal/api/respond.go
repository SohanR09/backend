package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// handlerFunc is a handler that returns a status and a JSON object body.
// handle() adds the "time_ns" field and writes the response.
type handlerFunc func(r *http.Request) (int, map[string]any)

// handle wraps fn so that every response carries the handler's execution time
// in nanoseconds, measured with the time package from start to end.
func (s *Server) handle(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		status, body := fn(r)
		body["time_ns"] = strconv.FormatInt(time.Since(start).Nanoseconds(), 10)
		writeJSON(w, status, body)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	payload, err := marshalJSON(body)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// fail builds an error response body.
func fail(status int, format string, args ...any) (int, map[string]any) {
	return status, map[string]any{"error": fmt.Sprintf(format, args...)}
}

// decodeJSON reads a single JSON value from the request body into dst and
// turns decoder errors into messages suitable for API clients.
func decodeJSON(r *http.Request, dst any) (int, error) {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var (
			syntaxErr *json.SyntaxError
			typeErr   *json.UnmarshalTypeError
			tooBig    *http.MaxBytesError
		)
		switch {
		case errors.Is(err, io.EOF):
			return http.StatusBadRequest, errors.New("request body is empty")
		case errors.As(err, &tooBig):
			return http.StatusRequestEntityTooLarge, errors.New("request body too large")
		case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
			return http.StatusBadRequest, errors.New("request body is not valid JSON")
		case errors.As(err, &typeErr):
			return http.StatusBadRequest, fmt.Errorf("field %q has the wrong type", typeErr.Field)
		default:
			return http.StatusBadRequest, errors.New("could not parse request body")
		}
	}
	if dec.More() {
		return http.StatusBadRequest, errors.New("request body must contain a single JSON object")
	}
	return 0, nil
}

// pagination reads optional limit/offset query parameters. When neither is
// present, paged is false and callers return everything.
type pagination struct {
	paged         bool
	limit, offset int
}

func parsePagination(q url.Values, maxLimit int) (pagination, error) {
	var p pagination
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, errors.New("limit must be a positive integer")
		}
		if n > maxLimit {
			n = maxLimit
		}
		p.limit, p.paged = n, true
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, errors.New("offset must be a non-negative integer")
		}
		p.offset, p.paged = n, true
		if p.limit == 0 {
			p.limit = maxLimit
		}
	}
	return p, nil
}

// paginate slices items according to p.
func paginate[T any](items []T, p pagination) []T {
	if !p.paged {
		return items
	}
	if p.offset >= len(items) {
		return []T{}
	}
	items = items[p.offset:]
	if p.limit < len(items) {
		items = items[:p.limit]
	}
	return items
}
