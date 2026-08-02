package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestCompressionNegotiatesQualityAndPreservesBody(t *testing.T) {
	body := []byte(strings.Repeat("<main>Process Lab workbench</main>", 200))
	handler := compressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Vary", "HX-Request")
		_, _ = w.Write(body)
	}))
	tests := []struct {
		name           string
		acceptEncoding string
		compressed     bool
	}{
		{name: "not advertised"},
		{name: "gzip", acceptEncoding: "gzip", compressed: true},
		{name: "gzip disabled", acceptEncoding: "br, gzip;q=0"},
		{name: "identity preferred", acceptEncoding: "gzip;q=0.4, identity;q=0.8"},
		{name: "gzip preferred", acceptEncoding: "identity;q=0, gzip;q=0.8", compressed: true},
		{name: "wildcard tie", acceptEncoding: "*;q=1", compressed: true},
		{name: "identity beats wildcard", acceptEncoding: "*;q=0.5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept-Encoding", test.acceptEncoding)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			got := response.Body.Bytes()
			if test.compressed {
				if encoding := response.Header().Get("Content-Encoding"); encoding != "gzip" {
					t.Fatalf("Content-Encoding = %q", encoding)
				}
				got = decompressForTest(t, got)
			} else if encoding := response.Header().Get("Content-Encoding"); encoding != "" {
				t.Fatalf("Content-Encoding = %q", encoding)
			}
			if !bytes.Equal(got, body) {
				t.Fatal("response body changed during negotiation")
			}
			if length := response.Header().Get("Content-Length"); length != strconv.Itoa(response.Body.Len()) {
				t.Fatalf("Content-Length = %q, body = %d", length, response.Body.Len())
			}
			assertVary(t, response.Header(), "HX-Request", "Accept-Encoding")
		})
	}
}

func TestCompressionPreservesSpecialResponses(t *testing.T) {
	large := []byte(strings.Repeat("compressible response ", 200))
	tests := []struct {
		name        string
		method      string
		status      int
		contentType string
		encoding    string
		body        []byte
		wantBody    bool
	}{
		{
			name: "HEAD", method: http.MethodHead, status: http.StatusOK,
			contentType: "text/html", body: large,
		},
		{
			name: "no content", method: http.MethodGet, status: http.StatusNoContent,
			contentType: "text/html", body: large,
		},
		{
			name: "already encoded", method: http.MethodGet, status: http.StatusOK,
			contentType: "text/html", encoding: "br", body: large, wantBody: true,
		},
		{
			name: "partial content", method: http.MethodGet, status: http.StatusPartialContent,
			contentType: "text/html", body: large, wantBody: true,
		},
		{
			name: "small", method: http.MethodGet, status: http.StatusOK,
			contentType: "text/html", body: []byte("small"), wantBody: true,
		},
		{
			name: "incompressible", method: http.MethodGet, status: http.StatusOK,
			contentType: "image/png", body: large, wantBody: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := compressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.encoding != "" {
					w.Header().Set("Content-Encoding", test.encoding)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write(test.body)
			}))
			request := httptest.NewRequest(test.method, "/", nil)
			request.Header.Set("Accept-Encoding", "gzip")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d", response.Code)
			}
			if test.wantBody && !bytes.Equal(response.Body.Bytes(), test.body) {
				t.Fatal("body changed")
			}
			if !test.wantBody && response.Body.Len() != 0 {
				t.Fatalf("body length = %d", response.Body.Len())
			}
			if test.encoding != "" && response.Header().Get("Content-Encoding") != test.encoding {
				t.Fatalf("Content-Encoding = %q", response.Header().Get("Content-Encoding"))
			}
			if test.method == http.MethodHead &&
				response.Header().Get("Content-Length") != strconv.Itoa(len(test.body)) {
				t.Fatalf("HEAD Content-Length = %q", response.Header().Get("Content-Length"))
			}
		})
	}
}

func TestApplicationDeliveryHeadersAndRepresentations(t *testing.T) {
	server, _ := openTestServer(t)

	identityRequest := httptest.NewRequest(http.MethodGet, "/flows/1/workbench", nil)
	identity := httptest.NewRecorder()
	server.Handler().ServeHTTP(identity, identityRequest)

	gzipRequest := httptest.NewRequest(http.MethodGet, "/flows/1/workbench", nil)
	gzipRequest.Header.Set("HX-Request", "true")
	gzipRequest.Header.Set("Accept-Encoding", "gzip")
	compressed := httptest.NewRecorder()
	server.Handler().ServeHTTP(compressed, gzipRequest)

	if compressed.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", compressed.Header().Get("Content-Encoding"))
	}
	if got := decompressForTest(t, compressed.Body.Bytes()); !bytes.Equal(got, identity.Body.Bytes()) {
		t.Fatal("compressed workbench differs from identity representation")
	}
	assertVary(t, compressed.Header(), "HX-Request", "Accept-Encoding")
	if cache := compressed.Header().Get("Cache-Control"); cache != "private, no-store" {
		t.Fatalf("dynamic Cache-Control = %q", cache)
	}

	htmx := request(t, server, http.MethodGet, "/assets/htmx-2.0.10.min.js", nil)
	if cache := htmx.Header().Get("Cache-Control"); cache != "public, max-age=31536000, immutable" {
		t.Fatalf("HTMX Cache-Control = %q", cache)
	}
	app := request(t, server, http.MethodGet, "/assets/app.css", nil)
	if cache := app.Header().Get("Cache-Control"); cache != "no-cache" {
		t.Fatalf("mutable asset Cache-Control = %q", cache)
	}
}

func decompressForTest(t *testing.T, body []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func assertVary(t *testing.T, header http.Header, values ...string) {
	t.Helper()
	got := strings.ToLower(strings.Join(header.Values("Vary"), ","))
	for _, value := range values {
		if !strings.Contains(got, strings.ToLower(value)) {
			t.Errorf("Vary %q does not contain %q", got, value)
		}
	}
}
