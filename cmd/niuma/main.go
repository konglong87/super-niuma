package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/konglong87/super-niuma/internal/cli"
	"github.com/konglong87/super-niuma/internal/server"
)

// @title niuma API
// @version 0.1.0
// @description niuma local server APIs, including internal query APIs, OpenAI-compatible endpoints, tenant persistence APIs, and mobile chat SSE APIs.
// @BasePath /
// @schemes http https
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name Authorization
// @description Internal server auth token. Use "Bearer <token>".
// @securityDefinitions.apikey MobileJWT
// @in header
// @name Authorization
// @description Mobile chat JWT. Use "Bearer <jwt>".
func main() {
	// 必须带 SIGTERM：systemd / k8s / `docker stop` 默认发的是 SIGTERM，
	// 只捕 SIGINT 时整套优雅退出逻辑在容器里一行都不会执行。
	ctx, stop := signal.NotifyContext(context.Background(), server.ShutdownSignals()...)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
