package server

import "net/http"

// tenantEndpoint 统一 tenant 系 handler 的前置检查：鉴权 → 存储可用 → 角色。
// 检查顺序与既有 handler 内联写法保持一致，不改变任何响应码或错误文案。
func tenantEndpoint(opts Options, roles []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if opts.TenantService == nil {
			writeTenantError(w, http.StatusServiceUnavailable, errMsgTenantStorageNotConfig)
			return
		}
		if len(roles) > 0 && !requireTenantRole(w, r, opts, roles...) {
			return
		}
		next(w, r)
	}
}
