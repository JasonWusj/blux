package httpapi

import (
	"blux/internal/platform"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativePickerContract(t *testing.T) {
	path := t.TempDir()
	encoded, _ := json.Marshal(path)
	for _, c := range []struct {
		path string
		err  error
		want string
	}{{path, nil, `"path":` + string(encoded)}, {"", platform.ErrCanceled, `"canceled":true`}} {
		api := &Server{DirectoryPicker: func(context.Context) (string, error) { return c.path, c.err }}
		rr := httptest.NewRecorder()
		api.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/v1/projects/select", nil))
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), c.want) {
			t.Fatal(rr.Code, rr.Body.String())
		}
	}
}
