package web

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

const compressionThreshold = 1024

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponse(header http.Header) *bufferedResponse {
	return &bufferedResponse{header: header.Clone()}
}

func (w *bufferedResponse) Header() http.Header {
	return w.header
}

func (w *bufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func compressResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffered := newBufferedResponse(w.Header())
		next.ServeHTTP(buffered, r)
		status := buffered.status
		if status == 0 {
			status = http.StatusOK
		}
		body := buffered.body.Bytes()
		if buffered.header.Get("Content-Type") == "" && len(body) > 0 {
			buffered.header.Set("Content-Type", http.DetectContentType(body))
		}

		contentLength, _ := strconv.Atoi(buffered.header.Get("Content-Length"))
		largeEnough := len(body) >= compressionThreshold ||
			r.Method == http.MethodHead && contentLength >= compressionThreshold
		eligible := statusAllowsBody(status) &&
			status != http.StatusPartialContent &&
			r.Header.Get("Range") == "" &&
			largeEnough &&
			buffered.header.Get("Content-Encoding") == "" &&
			compressibleType(buffered.header.Get("Content-Type"))
		if eligible {
			appendVary(buffered.header, "Accept-Encoding")
		}
		compress := r.Method != http.MethodHead &&
			eligible &&
			prefersGzip(r.Header.Get("Accept-Encoding"))
		if compress {
			var encoded bytes.Buffer
			writer := gzip.NewWriter(&encoded)
			_, _ = writer.Write(body)
			_ = writer.Close()
			body = encoded.Bytes()
			buffered.header.Set("Content-Encoding", "gzip")
			appendVary(buffered.header, "Accept-Encoding")
		}

		if r.Method == http.MethodHead {
			if buffered.header.Get("Content-Length") == "" {
				buffered.header.Set("Content-Length", strconv.Itoa(len(body)))
			}
			body = nil
		} else if statusAllowsBody(status) {
			buffered.header.Set("Content-Length", strconv.Itoa(len(body)))
		} else {
			buffered.header.Del("Content-Length")
			body = nil
		}
		copyHeader(w.Header(), buffered.header)
		w.WriteHeader(status)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
	})
}

func deliveryHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			if r.URL.Path == "/assets/htmx-2.0.10.min.js" {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
		} else {
			w.Header().Set("Cache-Control", "private, no-store")
			appendVary(w.Header(), "HX-Request")
		}
		next.ServeHTTP(w, r)
	})
}

func statusAllowsBody(status int) bool {
	return status >= http.StatusOK &&
		status != http.StatusNoContent &&
		status != http.StatusNotModified
}

func compressibleType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(value, ";")[0])
	}
	switch mediaType {
	case "text/html", "text/css", "text/javascript", "application/javascript",
		"application/json", "image/svg+xml":
		return true
	default:
		return false
	}
}

func prefersGzip(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	quality := map[string]float64{}
	for _, part := range strings.Split(value, ",") {
		fields := strings.Split(part, ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		q := 1.0
		for _, parameter := range fields[1:] {
			key, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(key, "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				q = 0
			} else {
				q = max(0, min(1, parsed))
			}
		}
		quality[name] = q
	}
	gzipQuality, found := quality["gzip"]
	if !found {
		gzipQuality = quality["*"]
	}
	if gzipQuality <= 0 {
		return false
	}
	identityQuality := 1.0
	if explicit, ok := quality["identity"]; ok {
		identityQuality = explicit
	} else if wildcard, ok := quality["*"]; ok && wildcard == 0 {
		identityQuality = 0
	}
	return gzipQuality >= identityQuality
}

func appendVary(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for _, existing := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(existing), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func copyHeader(destination, source http.Header) {
	for key := range destination {
		destination.Del(key)
	}
	for key, values := range source {
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}
