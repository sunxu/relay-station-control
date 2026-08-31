package api

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestAccountInventoryProductAPIRemainsLimitedToAuditedQuery(t *testing.T) {
	router := chi.NewRouter()
	HandlerWithOptions(NewServer("test"), ChiServerOptions{BaseRouter: router})
	var routes []string
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/account-inventory") {
			routes = append(routes, method+" "+route)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(routes)
	want := []string{http.MethodPost + " /api/account-inventory/query"}
	if !slices.Equal(routes, want) {
		t.Fatalf("account inventory product routes = %v, want %v", routes, want)
	}
}
