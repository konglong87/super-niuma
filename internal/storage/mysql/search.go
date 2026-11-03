package mysql

import (
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// audit 和 telemetry 的列表查询原本把 Search 一律翻成 `%x%` 前导通配 LIKE，铺在
// 4 到 10 个列上 —— 前导通配让索引全废，每次都是全表扫(AUDIT-P1-26)。而调用方
// 往往手里就攥着精确的 trace_id / session_id。
//
// 这里给 Search 加一层 `field:value` 限定符：命中白名单的字段走等值（或
// `a,b,c` 的 IN），落到既有索引上；剩下的自由文本仍旧走 LIKE，老用法不变。
// 列名只可能来自下面的白名单，不会来自用户输入。
type searchColumn struct {
	column  string
	numeric bool
}

type searchSchema struct {
	// exact 是限定符名到列的映射。
	exact map[string]searchColumn
	// fuzzy 是自由文本要铺的列。
	fuzzy []string
}

var auditSearchSchema = searchSchema{
	exact: map[string]searchColumn{
		"trace_id":      {column: "trace_id"},
		"action":        {column: "action"},
		"resource_type": {column: "resource_type"},
		"resource_id":   {column: "resource_id"},
	},
	fuzzy: []string{"action", "resource_type", "resource_id", "trace_id"},
}

var telemetrySearchSchema = searchSchema{
	exact: map[string]searchColumn{
		"trace_id":   {column: "trace_id"},
		"session_id": {column: "session_id", numeric: true},
		"event_name": {column: "event_name"},
		"category":   {column: "category"},
		"status":     {column: "status"},
		"source":     {column: "source"},
		"model":      {column: "model"},
		"tool_name":  {column: "tool_name"},
	},
	fuzzy: []string{"event_name", "category", "source", "status", "trace_id", "CAST(session_id AS CHAR)", "resource_type", "resource_id", "model", "tool_name"},
}

type exactCondition struct {
	column string
	values []any
}

// applySearch 把 Search 拆成等值条件和自由文本，分别挂到 query 上。
func applySearch(query *gorm.DB, search string, schema searchSchema) *gorm.DB {
	exact, text := parseSearch(search, schema)
	for _, condition := range exact {
		if len(condition.values) == 1 {
			query = query.Where(condition.column+" = ?", condition.values[0])
			continue
		}
		query = query.Where(condition.column+" IN ?", condition.values)
	}
	if text == "" || len(schema.fuzzy) == 0 {
		return query
	}
	_, pattern := normalizeSearch(text)
	clauses := make([]string, 0, len(schema.fuzzy))
	args := make([]any, 0, len(schema.fuzzy))
	for _, column := range schema.fuzzy {
		clauses = append(clauses, column+" LIKE ?")
		args = append(args, pattern)
	}
	return query.Where(strings.Join(clauses, " OR "), args...)
}

// parseSearch 按空白切词：命中白名单的 `field:value` 变成等值条件，其余原样拼回
// 自由文本。解析不出有效值的限定符（比如 session_id:abc）退化成自由文本，免得
// 一个笔误静默返回空结果。
func parseSearch(search string, schema searchSchema) ([]exactCondition, string) {
	search = strings.TrimSpace(search)
	if search == "" {
		return nil, ""
	}
	var (
		conditions []exactCondition
		text       []string
	)
	for _, token := range strings.Fields(search) {
		name, raw, ok := strings.Cut(token, ":")
		if !ok {
			text = append(text, token)
			continue
		}
		column, known := schema.exact[strings.ToLower(strings.TrimSpace(name))]
		if !known {
			text = append(text, token)
			continue
		}
		values := exactValues(raw, column.numeric)
		if len(values) == 0 {
			text = append(text, token)
			continue
		}
		conditions = append(conditions, exactCondition{column: column.column, values: values})
	}
	return conditions, strings.Join(text, " ")
}

func exactValues(raw string, numeric bool) []any {
	values := make([]any, 0, 1)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !numeric {
			values = append(values, part)
			continue
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil
		}
		values = append(values, number)
	}
	return values
}

// SearchQualifier 拼一个限定符。带逗号、冒号或空白的值没法安全表达，直接丢掉 ——
// trace_id / resource_id 实际上都是十六进制或数字，不会撞上这条。
func SearchQualifier(name string, values ...string) string {
	safe := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, ",: \t\n") {
			continue
		}
		safe = append(safe, value)
	}
	if len(safe) == 0 {
		return ""
	}
	return name + ":" + strings.Join(safe, ",")
}
