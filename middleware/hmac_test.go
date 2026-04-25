package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// computeSignature builds an HMAC-SHA256 hex-encoded signature locally for tests.
// Mirrors the production logic so tests assert end-to-end correctness, not
// just that the function "agrees with itself".
func computeSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func init() {
	gin.SetMode(gin.TestMode)
}

// newHMACTestRouter wires HMACSignatureMiddleware onto a test endpoint that
// echoes the body back, so tests can assert the body is restored downstream.
func newHMACTestRouter(t *testing.T, secretEnvKey string, downstreamCalled *bool) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.POST("/echo", HMACSignatureMiddleware(secretEnvKey), func(c *gin.Context) {
		if downstreamCalled != nil {
			*downstreamCalled = true
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "downstream read failed"})
			return
		}
		c.Data(http.StatusOK, "application/octet-stream", body)
	})
	return r
}

func TestHMACSignatureMiddleware_ValidSignature(t *testing.T) {
	const key = "TEST_HMAC_SECRET_VALID"
	t.Setenv(key, "supersecret")
	body := []byte(`{"event":"ok"}`)
	sig := computeSignature([]byte("supersecret"), body)

	called := false
	router := newHMACTestRouter(t, key, &called)

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("downstream handler should be called on valid signature")
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("body restored mismatch: got %q want %q", rec.Body.String(), body)
	}
}

func TestHMACSignatureMiddleware_InvalidSignature(t *testing.T) {
	const key = "TEST_HMAC_SECRET_INVALID"
	t.Setenv(key, "supersecret")
	body := []byte(`{"event":"ok"}`)
	wrongSig := computeSignature([]byte("WRONG_SECRET"), body)

	called := false
	router := newHMACTestRouter(t, key, &called)

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, wrongSig)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"invalid signature"`) {
		t.Fatalf("expected invalid signature JSON, got %s", rec.Body.String())
	}
	if called {
		t.Fatal("downstream handler must NOT be called on invalid signature")
	}
}

func TestHMACSignatureMiddleware_MissingHeader(t *testing.T) {
	const key = "TEST_HMAC_SECRET_MISSING"
	t.Setenv(key, "supersecret")
	body := []byte(`{}`)

	called := false
	router := newHMACTestRouter(t, key, &called)

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	// no signature header
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("downstream must not be called when header is missing")
	}
}

func TestHMACSignatureMiddleware_MalformedHex(t *testing.T) {
	const key = "TEST_HMAC_SECRET_MALFORMED"
	t.Setenv(key, "supersecret")
	body := []byte(`{}`)

	router := newHMACTestRouter(t, key, nil)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, "ZZZ-not-hex-XYZ!!!")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for malformed hex, got %d", rec.Code)
	}
}

func TestHMACSignatureMiddleware_EmptyBody(t *testing.T) {
	const key = "TEST_HMAC_SECRET_EMPTY"
	t.Setenv(key, "supersecret")
	body := []byte("")
	sig := computeSignature([]byte("supersecret"), body)

	called := false
	router := newHMACTestRouter(t, key, &called)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty body with valid sig, got %d", rec.Code)
	}
	if !called {
		t.Fatal("downstream must be called for empty body with valid sig")
	}
}

func TestHMACSignatureMiddleware_BodyRestored(t *testing.T) {
	const key = "TEST_HMAC_SECRET_RESTORE"
	t.Setenv(key, "supersecret")

	type payload struct {
		Foo string `json:"foo"`
		Bar int    `json:"bar"`
	}

	body := []byte(`{"foo":"hello","bar":42}`)
	sig := computeSignature([]byte("supersecret"), body)

	r := gin.New()
	r.POST("/json", HMACSignatureMiddleware(key), func(c *gin.Context) {
		var p payload
		if err := c.ShouldBindJSON(&p); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, p)
	})

	req := httptest.NewRequest(http.MethodPost, "/json", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"foo":"hello"`) || !strings.Contains(rec.Body.String(), `"bar":42`) {
		t.Fatalf("downstream did not see restored body: %s", rec.Body.String())
	}
}

func TestHMACSignatureMiddleware_HexCase(t *testing.T) {
	const key = "TEST_HMAC_SECRET_HEXCASE"
	t.Setenv(key, "supersecret")
	body := []byte(`payload-bytes`)
	sigLower := computeSignature([]byte("supersecret"), body)
	sigUpper := strings.ToUpper(sigLower)

	for _, sig := range []string{sigLower, sigUpper} {
		called := false
		router := newHMACTestRouter(t, key, &called)
		req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
		req.Header.Set(SignatureHeader, sig)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for sig=%s, got %d", sig, rec.Code)
		}
		if !called {
			t.Fatalf("downstream not called for sig=%s", sig)
		}
	}
}

func TestHMACSignatureMiddleware_EmptySecretEnv_Panics(t *testing.T) {
	const key = "TEST_HMAC_SECRET_EMPTY_PANIC"
	t.Setenv(key, "")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when env var is empty")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected panic string, got %T: %v", r, r)
		}
		if !strings.Contains(msg, key) {
			t.Fatalf("panic message should reference env key %q, got %q", key, msg)
		}
	}()

	_ = HMACSignatureMiddleware(key)
}

func TestHMACSignatureMiddleware_EmptyEnvKeyName_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty secretEnvKey argument")
		}
	}()
	_ = HMACSignatureMiddleware("")
}

func TestHMACSignatureMiddleware_OversizedBody(t *testing.T) {
	const key = "TEST_HMAC_SECRET_OVERSIZED"
	t.Setenv(key, "supersecret")
	body := bytes.Repeat([]byte("x"), maxBodySize+1)
	// supply a CORRECT signature so failure must be due to size, not signature.
	sig := computeSignature([]byte("supersecret"), body)

	called := false
	router := newHMACTestRouter(t, key, &called)

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"payload too large"`) {
		t.Fatalf("expected payload too large error, got %s", rec.Body.String())
	}
	if called {
		t.Fatal("downstream must not be called for oversized body")
	}
}

func TestHMACSignatureMiddleware_BodyAtCap(t *testing.T) {
	// Boundary: exactly maxBodySize bytes should pass.
	const key = "TEST_HMAC_SECRET_AT_CAP"
	t.Setenv(key, "supersecret")
	body := bytes.Repeat([]byte("x"), maxBodySize)
	sig := computeSignature([]byte("supersecret"), body)

	called := false
	router := newHMACTestRouter(t, key, &called)

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 at exact cap, got %d", rec.Code)
	}
	if !called {
		t.Fatal("downstream should be called for body at exactly the cap")
	}
}

func TestHMACSignatureMiddleware_MultipartBody(t *testing.T) {
	const key = "TEST_HMAC_SECRET_MULTIPART"
	t.Setenv(key, "supersecret")

	// Build a multipart body manually so we know exact bytes.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("name", "alice"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	part, err := w.CreateFormFile("file", "data.bin")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("binary-content-bytes")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	rawBody := buf.Bytes()
	sig := computeSignature([]byte("supersecret"), rawBody)

	called := false
	router := newHMACTestRouter(t, key, &called)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(rawBody))
	req.Header.Set(SignatureHeader, sig)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for multipart raw-body validation, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("downstream must be called for multipart with valid signature")
	}
	if !bytes.Equal(rec.Body.Bytes(), rawBody) {
		t.Fatalf("body restored mismatch on multipart")
	}
}

// failingReader returns an error on the first Read call.
type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read fail")
}

func TestHMACSignatureMiddleware_BodyReadError(t *testing.T) {
	const key = "TEST_HMAC_SECRET_READFAIL"
	t.Setenv(key, "supersecret")

	r := gin.New()
	r.POST("/echo", func(c *gin.Context) {
		// Inject a body reader that fails BEFORE the middleware runs is tricky
		// because Gin assigns Body via the http server. Instead, we install a
		// wrapper handler that swaps Body and re-invokes the middleware.
		c.Request.Body = io.NopCloser(failingReader{})
		HMACSignatureMiddleware(key)(c)
	})

	body := []byte(`{}`)
	sig := computeSignature([]byte("supersecret"), body)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, sig)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on body read error, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"cannot read body"`) {
		t.Fatalf("expected cannot read body, got %s", rec.Body.String())
	}
}
