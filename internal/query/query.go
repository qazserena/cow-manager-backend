// Package query 是只读列表接口的小助手:按代码里声明的列白名单,
// 把 URL 过滤参数翻译成 SQL,并提供分页与 CSV 导出。
//
// 过滤参数约定(只对声明了 Filter 的列生效,其余忽略):
//
//	col=v            等于;重复出现(col=1&col=2)视为 IN
//	col_like=v       LIKE %v%
//	col_from=v       >=
//	col_to=v         <=
//	col_gt=v / col_lt=v / col_ne=v
//	sort=col,asc     排序(可重复),只对声明了 Sort 的列生效
//	page / size      分页,page 从 0 起
package query

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/httpx"
)

// Kind 列的值类型,决定过滤参数解析与导出格式。
type Kind int

const (
	String Kind = iota
	Int
	Bool
	TsSec // unix 秒
	TsMs  // unix 毫秒
	Date  // 'YYYY-MM-DD'
	JSON  // JSON 文本
)

// Column 一列的声明。
type Column struct {
	Name   string
	Label  string
	Kind   Kind
	Filter bool
	Sort   bool
	// Enum 导出时用于把整数翻译成枚举文案的 meta 枚举代码(可空)
	Enum string
	// Expr 可选的 SQL 表达式(可带表别名或子查询),用于 SELECT / WHERE / ORDER BY;
	// 为空则直接用 `Name`。配合 Spec.From 做多表联查时使用
	Expr string
}

// Spec 一张表(或视图)的只读查询声明。
type Spec struct {
	Table       string
	Columns     []Column
	DefaultSort string // 如 "`id` DESC"
	BaseWhere   string // 固定附加条件,如 "deletedTime = 0"
	// From 可选的自定义 FROM 子句(可含 JOIN / 派生表);为空则用 `Table`。
	// 设了 From 之后 Table 只用于权限码与导出文件名
	From string
}

// expr 列在 SQL 里的写法。
func (c *Column) expr() string {
	if c.Expr != "" {
		return c.Expr
	}
	return "`" + c.Name + "`"
}

// from FROM 子句。
func (s *Spec) from() string {
	if s.From != "" {
		return s.From
	}
	return "`" + s.Table + "`"
}

// EnumLabeler 导出时的枚举翻译回调。
type EnumLabeler func(enumCode string, value int64) (string, bool)

// Column 按名字找列。
func (s *Spec) Column(name string) *Column {
	for i := range s.Columns {
		if s.Columns[i].Name == name {
			return &s.Columns[i]
		}
	}
	return nil
}

func (s *Spec) selectExpr() string {
	parts := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		if c.Expr != "" {
			parts[i] = c.Expr + " AS `" + c.Name + "`"
		} else {
			parts[i] = "`" + c.Name + "`"
		}
	}
	return strings.Join(parts, ", ")
}

// Where 累积的 WHERE 片段。
type Where struct {
	Conds []string
	Args  []any
}

// Add 追加一个条件。
func (w *Where) Add(cond string, args ...any) {
	w.Conds = append(w.Conds, cond)
	w.Args = append(w.Args, args...)
}

// SQL 生成 " WHERE ..."(无条件时为空串)。
func (w *Where) SQL() string {
	if len(w.Conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.Conds, " AND ")
}

var suffixOps = []struct{ suffix, op string }{
	{"_like", "LIKE"}, {"_from", ">="}, {"_to", "<="}, {"_gt", ">"}, {"_lt", "<"}, {"_ne", "<>"},
}

func (c *Column) parse(v string) (any, error) {
	v = strings.TrimSpace(v)
	switch c.Kind {
	case Int, TsSec, TsMs:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, err
		}
		return int64(f), nil
	case Bool:
		lv := strings.ToLower(v)
		if lv == "true" || lv == "1" {
			return 1, nil
		}
		return 0, nil
	default:
		return v, nil
	}
}

func escapeLike(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(v)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Where 把 URL 参数翻译成 WHERE。
func (s *Spec) Where(q url.Values) *Where {
	w := &Where{}
	if s.BaseWhere != "" {
		w.Add(s.BaseWhere)
	}
	for i := range s.Columns {
		c := &s.Columns[i]
		if !c.Filter {
			continue
		}
		col := c.expr()
		if vals, ok := q[c.Name]; ok {
			var args []any
			for _, v := range vals {
				if strings.TrimSpace(v) == "" {
					continue
				}
				if a, err := c.parse(v); err == nil {
					args = append(args, a)
				}
			}
			switch {
			case len(args) == 1:
				w.Add(col+" = ?", args[0])
			case len(args) > 1:
				w.Add(col+" IN ("+placeholders(len(args))+")", args...)
			}
		}
		for _, so := range suffixOps {
			v := q.Get(c.Name + so.suffix)
			if strings.TrimSpace(v) == "" {
				continue
			}
			if so.op == "LIKE" {
				w.Add(col+" LIKE ?", "%"+escapeLike(strings.TrimSpace(v))+"%")
				continue
			}
			a, err := c.parse(v)
			if err != nil {
				continue
			}
			w.Add(col+" "+so.op+" ?", a)
		}
	}
	return w
}

// OrderBy 把 sort 参数翻译成 ORDER BY。
func (s *Spec) OrderBy(q url.Values) string {
	var parts []string
	for _, sv := range q["sort"] {
		name, dir, _ := strings.Cut(sv, ",")
		c := s.Column(strings.TrimSpace(name))
		if c == nil || !c.Sort {
			continue
		}
		d := "ASC"
		dir = strings.ToLower(strings.TrimSpace(dir))
		if dir == "desc" || dir == "descend" {
			d = "DESC"
		}
		parts = append(parts, c.expr()+" "+d)
	}
	if len(parts) == 0 {
		if s.DefaultSort == "" {
			return ""
		}
		return " ORDER BY " + s.DefaultSort
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

// Row 一行结果,键为列名。
type Row = map[string]any

func (c *Column) convert(v any) any {
	if v == nil {
		return nil
	}
	var s string
	switch x := v.(type) {
	case []byte:
		s = string(x)
	case string:
		s = x
	case int64:
		if c.Kind == Bool {
			return x != 0
		}
		return x
	case float64:
		return x
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(x)
	}
	switch c.Kind {
	case Int, TsSec, TsMs:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		return s
	case Bool:
		return s == "1" || strings.EqualFold(s, "true")
	default:
		// JSON 列也按原文字符串返回,与原 Java 版(实体字段为 String)一致,前端自行解析
		return s
	}
}

func (s *Spec) scan(rows *sqlx.Rows) ([]Row, error) {
	out := make([]Row, 0)
	for rows.Next() {
		vals, err := rows.SliceScan()
		if err != nil {
			return nil, err
		}
		row := make(Row, len(s.Columns))
		for i := range s.Columns {
			if i < len(vals) {
				row[s.Columns[i].Name] = s.Columns[i].convert(vals[i])
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Count 统计行数。
func (s *Spec) Count(ctx context.Context, db *sqlx.DB, w *Where) (int64, error) {
	var total int64
	err := db.GetContext(ctx, &total, "SELECT COUNT(*) FROM "+s.from()+w.SQL(), w.Args...)
	return total, err
}

// Select 查询行(limit<=0 表示不限)。
func (s *Spec) Select(ctx context.Context, db *sqlx.DB, w *Where, order string, limit, offset int) ([]Row, error) {
	sql := "SELECT " + s.selectExpr() + " FROM " + s.from() + w.SQL() + order
	args := append([]any{}, w.Args...)
	if limit > 0 {
		sql += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}
	rows, err := db.QueryxContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scan(rows)
}

// Page 分页查询。
func (s *Spec) Page(ctx context.Context, db *sqlx.DB, q url.Values, page, size int) (*httpx.Page, error) {
	w := s.Where(q)
	total, err := s.Count(ctx, db, w)
	if err != nil {
		return nil, err
	}
	rows, err := s.Select(ctx, db, w, s.OrderBy(q), size, page*size)
	if err != nil {
		return nil, err
	}
	return &httpx.Page{Content: rows, TotalElements: total}, nil
}

// List 按过滤条件查询,最多 limit 行。
func (s *Spec) List(ctx context.Context, db *sqlx.DB, q url.Values, limit int) ([]Row, error) {
	return s.Select(ctx, db, s.Where(q), s.OrderBy(q), limit, 0)
}

// One 按某列取一行,不存在返回 nil。
func (s *Spec) One(ctx context.Context, db *sqlx.DB, col string, val any) (Row, error) {
	w := &Where{}
	if s.BaseWhere != "" {
		w.Add(s.BaseWhere)
	}
	if c := s.Column(col); c != nil {
		w.Add(c.expr()+" = ?", val)
	} else {
		w.Add("`"+col+"` = ?", val)
	}
	rows, err := s.Select(ctx, db, w, "", 1, 0)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// ExportCSV 按过滤条件导出全部行为 UTF-8(带 BOM)CSV。
func (s *Spec) ExportCSV(ctx context.Context, db *sqlx.DB, q url.Values, out io.Writer, loc *time.Location, labeler EnumLabeler) error {
	w := s.Where(q)
	sql := "SELECT " + s.selectExpr() + " FROM " + s.from() + w.SQL() + s.OrderBy(q)
	rows, err := db.QueryxContext(ctx, sql, w.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	if _, err := out.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(out)
	header := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		if c.Label != "" {
			header[i] = c.Label
		} else {
			header[i] = c.Name
		}
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	record := make([]string, len(s.Columns))
	n := 0
	for rows.Next() {
		vals, err := rows.SliceScan()
		if err != nil {
			return err
		}
		for i := range s.Columns {
			var v any
			if i < len(vals) {
				v = vals[i]
			}
			record[i] = s.Columns[i].format(v, loc, labeler)
		}
		if err := cw.Write(record); err != nil {
			return err
		}
		n++
		if n%500 == 0 {
			cw.Flush()
			if f, ok := out.(interface{ Flush() }); ok {
				f.Flush()
			}
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return rows.Err()
}

func (c *Column) format(v any, loc *time.Location, labeler EnumLabeler) string {
	cv := c.convert(v)
	if cv == nil {
		return ""
	}
	switch c.Kind {
	case TsSec, TsMs:
		n, ok := cv.(int64)
		if !ok || n == 0 {
			return ""
		}
		t := time.Unix(n, 0)
		if c.Kind == TsMs {
			t = time.UnixMilli(n)
		}
		return t.In(loc).Format("2006-01-02 15:04:05")
	case Bool:
		if b, ok := cv.(bool); ok && b {
			return "是"
		}
		return "否"
	case Int:
		if n, ok := cv.(int64); ok && c.Enum != "" && labeler != nil {
			if label, found := labeler(c.Enum, n); found {
				return label
			}
		}
		return fmt.Sprint(cv)
	default:
		return fmt.Sprint(cv)
	}
}
