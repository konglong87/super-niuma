package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/konglong87/super-niuma/internal/super/freemodels"
)

// FreeModelProvider is one entry of the built-in free model catalog. All
// catalog fields are kept as-is; Unverified is derived so the UI can label the
// provider "未核实" and use the raw fields to say which item is unconfirmed.
type FreeModelProvider struct {
	freemodels.Provider
	Unverified bool `json:"unverified"`
}

// FreeModelsResponse is the payload returned by GET /runtime/free-models.
type FreeModelsResponse struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   string              `json:"generated_at"`
	Providers     []FreeModelProvider `json:"providers"`
}

func registerFreeModelRoutes(router *gin.Engine, opts Options) {
	router.GET("/runtime/free-models", freeModelsGin(opts))
}

func freeModelsGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		catalog, err := freemodels.Load()
		if err != nil {
			http.Error(c.Writer, "free model catalog unavailable", http.StatusInternalServerError)
			return
		}
		resp := FreeModelsResponse{
			SchemaVersion: catalog.SchemaVersion,
			GeneratedAt:   catalog.GeneratedAt,
			Providers:     make([]FreeModelProvider, 0, len(catalog.Providers)),
		}
		for _, p := range catalog.Providers {
			resp.Providers = append(resp.Providers, FreeModelProvider{Provider: p, Unverified: p.Unverified()})
		}
		writeJSON(c.Writer, resp)
	}
}
