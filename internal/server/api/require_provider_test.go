//go:build enterprise

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// requireProvider — гейт управления тенантами: только провайдер (актор в Default-тенанте,
// нескоуплен) проходит; скоупленный не-Default it_admin отбивается 403 (иначе он мог бы
// POST /tenants/{Default}/assign со своим id и самоповыситься до провайдера).
func TestRequireProviderGuard(t *testing.T) {
	// Скоуплен на не-Default тенант → 403, guard возвращает false.
	scopedCtx := storage.WithTenantScope(context.Background(), "11111111-1111-1111-1111-111111111111")
	req := httptest.NewRequest(http.MethodGet, "/tenants", nil).WithContext(scopedCtx)
	w := httptest.NewRecorder()
	if requireProvider(w, req) {
		t.Fatal("скоупленный актор НЕ должен проходить гейт управления тенантами")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}

	// Провайдер (Default → WithTenantScope не ставит scope) → проходит.
	provCtx := storage.WithTenantScope(context.Background(), storage.DefaultTenantID)
	reqP := httptest.NewRequest(http.MethodGet, "/tenants", nil).WithContext(provCtx)
	wP := httptest.NewRecorder()
	if !requireProvider(wP, reqP) {
		t.Fatal("провайдер (Default-тенант) должен проходить гейт")
	}

	// Нескоупленный ctx (нет актора) — тоже провайдер (фон/тест) → проходит.
	reqU := httptest.NewRequest(http.MethodGet, "/tenants", nil)
	wU := httptest.NewRecorder()
	if !requireProvider(wU, reqU) {
		t.Fatal("нескоупленный ctx должен трактоваться как провайдер")
	}
}
