package server

// swaggerFreeModels godoc
// @Summary List the built-in free model providers
// @Description Read-only snapshot embedded at build time (no online update). Every catalog field is returned unchanged; `unverified` is derived: true when status is not ok, openai_compatible is not supported, the recommended model is null, or its tool_calling is not supported. The catalog never contains API keys.
// @Tags Runtime
// @Security ApiKeyAuth
// @Produce json
// @Success 200 {object} FreeModelsResponse
// @Failure 401 {object} SwaggerError
// @Failure 500 {object} SwaggerError
// @Router /runtime/free-models [get]
func swaggerFreeModels() {}
