module github.com/konglong87/super-niuma/mobile

go 1.26.0

// The phone core lives in its own module so the gomobile tooling and the
// golang.org/x versions it needs never change the desktop and server build.
// It still compiles the repository's own packages from the parent directory.
replace github.com/konglong87/super-niuma => ../

require (
	github.com/konglong87/super-niuma v0.0.0-00010101000000-000000000000
	github.com/mattn/go-sqlite3 v1.14.22
)

require (
	filippo.io/edwards25519 v1.1.1 // indirect
	github.com/anthropics/anthropic-sdk-go v1.46.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
	github.com/golang-migrate/migrate/v4 v4.19.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/openai/openai-go/v3 v3.32.0 // indirect
	github.com/redis/go-redis/v9 v9.18.0 // indirect
	github.com/sashabaranov/go-openai v1.41.2 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/mobile v0.0.0-20261011034528-197770444867 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gorm.io/driver/mysql v1.6.0 // indirect
	gorm.io/driver/sqlite v1.6.0 // indirect
	gorm.io/gorm v1.31.1 // indirect
	mvdan.cc/sh/v3 v3.12.0 // indirect
)

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)
