package auth

import "testing"

func mustTree(t *testing.T, raw string) *Tree {
	tr, err := ParseTree(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// Covers 是「不能授予自己没有的权限」的判定依据。
func TestCovers(t *testing.T) {
	root := mustTree(t, `{"code":"","wildcard":true}`)
	allFeatures := mustTree(t, `{"code":"","children":{"feature":{"code":"feature","wildcard":true},"game":{"code":"game","wildcard":true}}}`)
	mailEditor := mustTree(t, `{"code":"","children":{"feature":{"code":"feature","children":{"mail":{"code":"mail","children":{"view":{"code":"view"},"edit":{"code":"edit"}}}}},"game":{"code":"game","children":{"ranch":{"code":"ranch","children":{"dev":{"code":"dev","wildcard":true}}}}}}}`)
	mailViewer := mustTree(t, `{"code":"","children":{"feature":{"code":"feature","children":{"mail":{"code":"mail","children":{"view":{"code":"view"}}}}},"game":{"code":"game","children":{"ranch":{"code":"ranch","children":{"dev":{"code":"dev","wildcard":true}}}}}}}`)
	mailWildcard := mustTree(t, `{"code":"","children":{"feature":{"code":"feature","children":{"mail":{"code":"mail","wildcard":true}}}}}`)
	prodViewer := mustTree(t, `{"code":"","children":{"feature":{"code":"feature","children":{"mail":{"code":"mail","children":{"view":{"code":"view"}}}}},"game":{"code":"game","children":{"ranch":{"code":"ranch","children":{"prod":{"code":"prod","wildcard":true}}}}}}}`)
	empty := mustTree(t, `{"code":"","children":{}}`)

	cases := []struct {
		name  string
		a, b  *Tree
		cover bool
	}{
		{"root covers everything", root, allFeatures, true},
		{"root covers root", root, root, true},
		{"all-features does not cover root wildcard", allFeatures, root, false},
		{"all-features covers mail editor", allFeatures, mailEditor, true},
		{"mail editor covers mail viewer", mailEditor, mailViewer, true},
		{"mail viewer does not cover mail editor", mailViewer, mailEditor, false},
		{"mail editor (leaves) does not cover mail wildcard", mailEditor, mailWildcard, false},
		{"mail wildcard covers mail editor features", mailWildcard, mustTree(t, `{"code":"","children":{"feature":{"code":"feature","children":{"mail":{"code":"mail","children":{"edit":{"code":"edit"}}}}}}}`), true},
		{"dev-only does not cover prod region", mailEditor, prodViewer, false},
		{"anyone covers empty", mailViewer, empty, true},
		{"empty covers nothing but empty", empty, mailViewer, false},
		{"nil operator tree covers only empty", nil, empty, true},
		{"nil operator tree does not cover viewer", nil, mailViewer, false},
	}
	for _, c := range cases {
		if got := c.a.Covers(c.b); got != c.cover {
			t.Errorf("%s: Covers = %v, want %v", c.name, got, c.cover)
		}
	}
}

func TestIsSuperAdmin(t *testing.T) {
	if !mustTree(t, `{"code":"","wildcard":true}`).IsSuperAdmin() {
		t.Fatal("root wildcard should be super admin")
	}
	if mustTree(t, `{"code":"","children":{"feature":{"code":"feature","wildcard":true}}}`).IsSuperAdmin() {
		t.Fatal("feature wildcard is not super admin")
	}
	var nilTree *Tree
	if nilTree.IsSuperAdmin() {
		t.Fatal("nil tree")
	}
}
