package sdk

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// LoadJSON reads a JSON file into v. A missing file is not an error.
func LoadJSON(path string, v interface{}) error {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// SaveJSON writes v atomically: a unique temporary file in the same directory
// is synced and renamed over path, so neither a power cut nor two concurrent
// saves can leave a truncated or mixed file behind.
func SaveJSON(path string, v interface{}) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(raw, '\n'), 0644)
}

// WriteFileAtomic writes data to path via a temporary file and rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	_ = f.Sync()
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteJSON sends v as a JSON response. It encodes before writing the status
// line, so an unencodable value yields a 500 instead of an empty 200.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	raw, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		raw, _ = json.Marshal(map[string]string{"error": "encoding response: " + err.Error(), "code": "internal"})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

// APIError is an error with a stable machine-readable code (for clients and
// translations) and the HTTP status to answer with. Responses have the shape
// {"error": "<English message>", "code": "<code>"}.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

// NewError creates an APIError.
func NewError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// WriteError answers with err: an *APIError keeps its status and code, any
// other error becomes 500/"internal".
func WriteError(w http.ResponseWriter, err error) {
	var ae *APIError
	if errors.As(err, &ae) {
		WriteJSON(w, ae.Status, map[string]string{"error": ae.Message, "code": ae.Code})
		return
	}
	WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error(), "code": "internal"})
}

// Error answers with a plain message and status (code "error").
func Error(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg, "code": "error"})
}
