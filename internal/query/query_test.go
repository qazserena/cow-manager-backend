package query

import (
	"net/url"
	"testing"
)

var spec = &Spec{
	Table: "t", DefaultSort: "`id` DESC", BaseWhere: "deletedTime = 0",
	Columns: []Column{
		{Name: "id", Kind: Int, Filter: true, Sort: true},
		{Name: "title", Kind: String, Filter: true},
		{Name: "createTs", Kind: TsSec, Filter: true, Sort: true},
		{Name: "active", Kind: Bool, Filter: true},
		{Name: "secret", Kind: String}, // 不可过滤
	},
}

func TestWhere(t *testing.T) {
	q := url.Values{
		"id":            {"1", "2"},
		"title_like":    {"周年%"},
		"createTs_from": {"100"},
		"createTs_to":   {"200"},
		"active":        {"true"},
		"secret":        {"x"},
		"nope_like":     {"y"},
	}
	w := spec.Where(q)
	sql := w.SQL()
	for _, want := range []string{"deletedTime = 0", "`id` IN (?,?)", "`title` LIKE ?", "`createTs` >= ?", "`createTs` <= ?", "`active` = ?"} {
		if !containsStr(sql, want) {
			t.Fatalf("缺少片段 %q: %s", want, sql)
		}
	}
	if containsStr(sql, "secret") || containsStr(sql, "nope") {
		t.Fatalf("不可过滤列不应出现: %s", sql)
	}
	if len(w.Args) != 6 {
		t.Fatalf("参数个数 = %d, want 6: %v", len(w.Args), w.Args)
	}
	if w.Args[2] != "%周年\\%%" {
		t.Fatalf("LIKE 值应转义通配符: %v", w.Args[2])
	}
}

func TestOrderBy(t *testing.T) {
	if got := spec.OrderBy(url.Values{}); got != " ORDER BY `id` DESC" {
		t.Fatalf("默认排序 = %q", got)
	}
	got := spec.OrderBy(url.Values{"sort": {"createTs,asc", "title,desc", "id,descend"}})
	if got != " ORDER BY `createTs` ASC, `id` DESC" {
		t.Fatalf("排序 = %q", got)
	}
}

func TestConvert(t *testing.T) {
	c := &Column{Kind: Int}
	if v := c.convert([]byte("42")); v != int64(42) {
		t.Fatalf("Int convert = %#v", v)
	}
	b := &Column{Kind: Bool}
	if v := b.convert([]byte("1")); v != true {
		t.Fatalf("Bool convert = %#v", v)
	}
	j := &Column{Kind: JSON}
	if v := j.convert([]byte(`{"a":1}`)); v != `{"a":1}` {
		t.Fatalf("JSON 列应按原文字符串返回: %#v", v)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
