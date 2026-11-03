package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/prompttemplate"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func TestPromptTemplateHTTPPersistenceAndOwnership(t *testing.T) {
	const token = "prompt-template-test"
	const tenantKey = "prompt-template-tenant"
	const owner = "owner"
	repo, err := mysqlstore.OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "templates.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if _, err := repo.UpsertTenant(context.Background(), mysqlstore.TenantInput{TenantKey: tenantKey, Name: "Templates"}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Options{
		AuthToken: token, TenantService: tenantservice.NewService(repo, nil),
		PromptTemplateService: prompttemplate.NewService(repo),
	}, nil)
	request := func(method, path, user, auth string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("X-Tenant-Key", tenantKey)
		req.Header.Set("X-User-Id", user)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s user=%s: status=%d body=%s, want %d", method, path, user, response.Code, response.Body.String(), want)
		}
		if want >= http.StatusBadRequest && !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("error content type = %q, want text/plain", response.Header().Get("Content-Type"))
		}
		return response
	}
	const path = "/tenant/prompt-templates"
	original := promptTemplateRequest{Title: "Original", Content: "before", Pinned: true, SortOrder: 4}
	request(http.MethodPost, path, owner, "", original, http.StatusUnauthorized)
	response := request(http.MethodPost, path, owner, token, original, http.StatusOK)
	var created prompttemplate.PromptTemplate
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	update := promptTemplateRequest{ID: created.ID, Title: "Renamed", Content: "after"}
	request(http.MethodPatch, path, "foreign-user", token, update, http.StatusNotFound)
	response = request(http.MethodGet, path, "foreign-user", token, nil, http.StatusOK)
	if response.Body.String() != "[]\n" {
		t.Fatalf("foreign user has records: %s", response.Body.String())
	}
	request(http.MethodPatch, path, owner, token, original, http.StatusBadRequest)
	request(http.MethodPatch, path, owner, token, update, http.StatusOK)
	request(http.MethodPatch, path, owner, token, update, http.StatusOK)
	response = request(http.MethodGet, path, owner, token, nil, http.StatusOK)
	var items []prompttemplate.PromptTemplate
	if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != created.ID || items[0].Content != "after" || items[0].Pinned || items[0].SortOrder != 0 {
		t.Fatalf("update readback = %+v", items)
	}
	request(http.MethodPost, path, owner, token, original, http.StatusOK)
	conflict := update
	conflict.Title = original.Title
	response = request(http.MethodPatch, path, owner, token, conflict, http.StatusConflict)
	if response.Body.String() != prompttemplate.ErrConflict.Error()+"\n" {
		t.Fatalf("conflict body = %q", response.Body.String())
	}
	missing := update
	missing.ID += 1000
	request(http.MethodPatch, path, owner, token, missing, http.StatusNotFound)
	deletePath := fmt.Sprintf("%s/%d", path, created.ID)
	request(http.MethodDelete, deletePath, "foreign-user", token, nil, http.StatusNotFound)
	request(http.MethodDelete, deletePath, owner, token, nil, http.StatusNoContent)
	request(http.MethodDelete, deletePath, owner, token, nil, http.StatusNotFound)
}
