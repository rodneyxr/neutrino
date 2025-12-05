package introspect

import (
	"reflect"
	"strings"
	"text/template/parse"
)

// walker holds the state of the AST traversal.
type walker struct {
	ChartName string
	File      string
	Content   string
	Results   []ValueUsage

	// scopes stack tracks the current dot context.
	// Each element represents a scope.
	// A scope is a list of variable bindings or resolved paths.
	// For simplicity, we'll track the current path of "."
	// nil means "." is unknown or root (depending on context, but usually root starts as empty string)
	dotPathStack []string

	// variables tracks variable bindings (e.g. $relname := .Chart.Name)
	// map of varName -> resolvedPath
	variables map[string]string
}

func newWalker(chartName, file, content string) *walker {
	return &walker{
		ChartName:    chartName,
		File:         file,
		Content:      content,
		Results:      []ValueUsage{},
		dotPathStack: []string{""}, // Start with root dot being empty prefix (effectively root)
		variables:    make(map[string]string),
	}
}

func (w *walker) walk(node parse.Node) {
	if node == nil {
		return
	}
	// Handle typed nil interfaces
	val := reflect.ValueOf(node)
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return
	}

	switch n := node.(type) {
	case *parse.ListNode:
		for _, node := range n.Nodes {
			w.walk(node)
		}
	case *parse.ActionNode:
		w.walk(n.Pipe)
	case *parse.PipeNode:
		for _, decl := range n.Decl {
			w.walk(decl)
		}
		for _, cmd := range n.Cmds {
			w.walk(cmd)
		}
		// Handle variable assignment if present
		if len(n.Decl) > 0 && len(n.Cmds) > 0 {
			w.handleAssignment(n)
		}
	case *parse.CommandNode:
		for _, arg := range n.Args {
			w.walk(arg)
		}
	case *parse.FieldNode:
		w.handleField(n)
	case *parse.VariableNode:
		// We encounter a variable usage
		// Check if it resolves to a Values path
		w.handleVariable(n)
	case *parse.IfNode:
		w.walk(n.List)
		w.walk(n.ElseList)
	case *parse.RangeNode:
		w.handleRange(n)
	case *parse.WithNode:
		w.handleWith(n)
	case *parse.TemplateNode:
		// Logic to follow named templates could go here,
		// but standard static analysis usually stays within file or assumes standard includes.
		// For now we skip recursing into named templates definitions unless we parse them all.
		// Zarf ADR says "basic version" first.
	}
}

func (w *walker) handleField(n *parse.FieldNode) {
	path := ""

	// Check if this field starts with .Values
	if len(n.Ident) > 0 && n.Ident[0] == "Values" {
		// It is a direct .Values usage
		parts := n.Ident[1:]
		if len(parts) > 0 {
			path = strings.Join(parts, ".")
			w.addUsage(path, n, "field", false)
		}
		return
	}

	// Check if it starts with . and we know what . is
	if len(n.Ident) > 0 {
		currentDot := w.currentDot()
		// If current dot is rooted at Values (e.g. inside a with .Values.image)
		if strings.HasPrefix(currentDot, "Values.") {
			suffix := strings.Join(n.Ident, ".")
			fullPath := strings.TrimPrefix(currentDot, "Values.") + "." + suffix
			w.addUsage(fullPath, n, "field", false)
			return
		}
		// Also strict check if currentDot IS "Values"
		if currentDot == "Values" {
			path = strings.Join(n.Ident, ".")
			w.addUsage(path, n, "field", false)
			return
		}
	}
}

func (w *walker) handleVariable(n *parse.VariableNode) {
	if len(n.Ident) == 0 {
		return
	}
	varName := n.Ident[0]
	// If it is regular variable $foo
	if val, ok := w.variables[varName]; ok {
		// Variable resolves to something we track
		if strings.HasPrefix(val, "Values.") {
			// Used the variable itself
			// e.g. {{ $v }} where $v is .Values.foo
			// We should record usage of .Values.foo
			w.addUsage(val, n, "variable", false)
		}
	}
}

func (w *walker) handleAssignment(n *parse.PipeNode) {
	if len(n.Decl) == 0 || len(n.Cmds) == 0 {
		return
	}
	// Simple case: $var := .Values.foo
	// We need to resolve the RHS.
	// Only handling single variable assignment for now.

	destVar := n.Decl[0] // *parse.VariableNode
	if len(destVar.Ident) == 0 {
		return
	}
	varName := destVar.Ident[0]

	rhs := n.Cmds[0] // *parse.CommandNode
	// Resolve RHS to a path string if possible
	path := w.resolveCommandToPath(rhs)

	if path != "" {
		w.variables[varName] = path
	}
}

func (w *walker) resolveCommandToPath(cmd *parse.CommandNode) string {
	if len(cmd.Args) == 0 {
		return ""
	}
	// Check first arg
	switch arg := cmd.Args[0].(type) {
	case *parse.FieldNode:
		// .Values.foo
		if len(arg.Ident) > 0 && arg.Ident[0] == "Values" {
			parts := arg.Ident[1:]
			return "Values." + strings.Join(parts, ".")
		}
		// .foo where . is Values...
		currentDot := w.currentDot()
		if currentDot != "" {
			if strings.HasPrefix(currentDot, "Values.") || currentDot == "Values" {
				suffix := strings.Join(arg.Ident, ".")
				if currentDot == "Values" {
					return "Values." + suffix
				}
				return currentDot + "." + suffix
			}
		}
	case *parse.VariableNode:
		if len(arg.Ident) > 0 {
			if val, ok := w.variables[arg.Ident[0]]; ok {
				return val
			}
		}
	}
	return ""
}

func (w *walker) handleRange(n *parse.RangeNode) {
	w.pushScope(n.Pipe)
	w.walk(n.List)
	w.popScope()
	w.walk(n.ElseList)
}

func (w *walker) handleWith(n *parse.WithNode) {
	w.pushScope(n.Pipe)
	w.walk(n.List)
	w.popScope()
	w.walk(n.ElseList)
}

func (w *walker) pushScope(pipe *parse.PipeNode) {
	// Determine the new dot
	// If pipe is empty or creates a var but not dot?
	// range $k, $v := .List -> dot is unchanged? No, output of pipeline is dot in With, but Range?
	// In range, T is the pipeline value. key, val are variables. Dot is set to the element.

	// Resolve pipeline
	if len(pipe.Cmds) > 0 {
		// Simplification: only take the first command result as the new context
		path := w.resolveCommandToPath(pipe.Cmds[0])
		if path != "" {
			// If we are iterating over a values path
			// e.g. range .Values.items -> path is Values.items
			// Inside, dot is Values.items[*] (wildcard)
			// We'll mark it as dynamic
			if strings.HasPrefix(path, "Values.") {
				// Use hash/wildcard
				w.dotPathStack = append(w.dotPathStack, path+".*")
				return
			}
		}
	}

	// If we can't resolve, we push nil/unknown
	w.dotPathStack = append(w.dotPathStack, "")
}

func (w *walker) popScope() {
	if len(w.dotPathStack) > 0 {
		w.dotPathStack = w.dotPathStack[:len(w.dotPathStack)-1]
	}
}

func (w *walker) currentDot() string {
	if len(w.dotPathStack) > 0 {
		return w.dotPathStack[len(w.dotPathStack)-1]
	}
	return ""
}

func (w *walker) addUsage(path string, node parse.Node, kind string, wildcard bool) {
	// Clean path?
	// e.g. remove trailing dots?
	// Validate it looks like a graph path

	loc := ValueUsageLocation{
		File: w.File,
		Kind: kind,
		Line: w.lineFromPos(node.Position()),
	}

	// Check if already exists
	for i, u := range w.Results {
		if u.Path == path && u.Chart == w.ChartName {
			w.Results[i].Locations = append(w.Results[i].Locations, loc)
			return
		}
	}

	w.Results = append(w.Results, ValueUsage{
		Path:      path,
		Chart:     w.ChartName,
		Wildcard:  wildcard || strings.Contains(path, "*"),
		Locations: []ValueUsageLocation{loc},
	})
}

func (w *walker) lineFromPos(pos parse.Pos) int {
	if int(pos) > len(w.Content) {
		return 0
	}
	return strings.Count(w.Content[:int(pos)], "\n") + 1
}
