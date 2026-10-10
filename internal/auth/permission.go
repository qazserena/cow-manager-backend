package auth

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Tree 角色权限树(role.permissionTree 的 JSON 形态)。
//
// 校验规则与 Java PermissionTree.check 一致:按 "/" 逐段下钻,途中任一节点
// wildcard=true 即放行;整条路径都能匹配到节点也放行(非严格模式)。
type Tree struct {
	Code     string           `json:"code"`
	Wildcard bool             `json:"wildcard"`
	Children map[string]*Tree `json:"children,omitempty"`
}

// ParseTree 解析 JSON;空串返回空树。
func ParseTree(raw string) (*Tree, error) {
	t := &Tree{}
	if strings.TrimSpace(raw) == "" {
		return t, nil
	}
	if err := json.Unmarshal([]byte(raw), t); err != nil {
		return nil, err
	}
	return t, nil
}

// Clone 深拷贝。
func (t *Tree) Clone() *Tree {
	if t == nil {
		return nil
	}
	c := &Tree{Code: t.Code, Wildcard: t.Wildcard}
	if len(t.Children) > 0 {
		c.Children = make(map[string]*Tree, len(t.Children))
		for k, v := range t.Children {
			c.Children[k] = v.Clone()
		}
	}
	return c
}

// Merge 把另一棵树并入(多角色取并集)。
func (t *Tree) Merge(o *Tree) {
	if o == nil {
		return
	}
	if o.Wildcard {
		t.Wildcard = true
	}
	for k, child := range o.Children {
		if t.Children == nil {
			t.Children = map[string]*Tree{}
		}
		if mine, ok := t.Children[k]; ok {
			mine.Merge(child)
		} else {
			t.Children[k] = child.Clone()
		}
	}
}

// Check 校验权限码,如 "feature/mail/edit"。
func (t *Tree) Check(code string) bool {
	if t == nil {
		return false
	}
	node := t
	for _, part := range strings.Split(strings.Trim(code, "/"), "/") {
		if node.Wildcard {
			return true
		}
		if part == "" {
			continue
		}
		child, ok := node.Children[part]
		if !ok {
			return false
		}
		node = child
	}
	return true
}

// IsSuperAdmin 根通配:拥有一切。
func (t *Tree) IsSuperAdmin() bool { return t != nil && t.Wildcard }

// hasWildcardOn 从根沿 path 下钻,途中(含终点)遇到通配即 true;用于判断「能否授予 path 下的全部」。
func (t *Tree) hasWildcardOn(path []string) bool {
	if t == nil {
		return false
	}
	node := t
	if node.Wildcard {
		return true
	}
	for _, part := range path {
		child, ok := node.Children[part]
		if !ok {
			return false
		}
		node = child
		if node.Wildcard {
			return true
		}
	}
	return false
}

// Covers 本树是否覆盖 other 的全部权限 —— 授予 / 管理角色时的边界:不能给出自己没有的东西。
// other 里的通配节点要求本树在同一路径或其上游也有通配;叶子节点要求本树 Check 通过。
func (t *Tree) Covers(other *Tree) bool {
	if other == nil {
		return true
	}
	if t != nil && t.Wildcard {
		return true
	}
	return coversNode(t, other, nil)
}

func coversNode(t *Tree, node *Tree, path []string) bool {
	if node.Wildcard {
		return t.hasWildcardOn(path)
	}
	if len(node.Children) == 0 {
		if len(path) == 0 {
			return true // 空树
		}
		return t.Check(strings.Join(path, "/"))
	}
	for key, child := range node.Children {
		p := make([]string, len(path)+1)
		copy(p, path)
		p[len(path)] = key
		if !coversNode(t, child, p) {
			return false
		}
	}
	return true
}

// MergeTrees 合并多棵树为新树。
func MergeTrees(trees ...*Tree) *Tree {
	root := &Tree{}
	for _, t := range trees {
		root.Merge(t)
	}
	return root
}

// Node 权限定义树(GET /permission/tree 的返回形态,供角色编辑器展示全集)。
type Node struct {
	Code     string           `json:"code"`
	Name     string           `json:"name"`
	Children map[string]*Node `json:"children"`
}

func newNode(code, name string) *Node {
	return &Node{Code: code, Name: name, Children: map[string]*Node{}}
}

// 顶层命名空间的显示名。
var namespaceNames = map[string]string{
	"feature": "功能",
	"game":    "区域",
}

// Registry 启动时由各模块注册权限码与名称,构成权限定义树。
type Registry struct {
	mu   sync.RWMutex
	root *Node
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry { return &Registry{root: newNode("", "")} }

// Register 注册权限码(如 "feature/mail/edit"),并为末段命名;
// 中间段若尚未命名则用给定的 names 依次填充。返回原权限码,便于内联使用。
func (r *Registry) Register(code, name string, intermediateNames ...string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	parts := strings.Split(strings.Trim(code, "/"), "/")
	node := r.root
	for i, part := range parts {
		child, ok := node.Children[part]
		if !ok {
			child = newNode(part, "")
			node.Children[part] = child
		}
		if i == len(parts)-1 {
			if name != "" {
				child.Name = name
			}
		} else if child.Name == "" {
			if i == 0 {
				child.Name = namespaceNames[part]
			} else if i-1 < len(intermediateNames) {
				child.Name = intermediateNames[i-1]
			}
		}
		node = child
	}
	return code
}

// Tree 返回定义树的深拷贝。
func (r *Registry) Tree() *Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneNode(r.root)
}

func cloneNode(n *Node) *Node {
	c := newNode(n.Code, n.Name)
	keys := make([]string, 0, len(n.Children))
	for k := range n.Children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.Children[k] = cloneNode(n.Children[k])
	}
	return c
}
