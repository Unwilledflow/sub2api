package repository

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestClearErrorDoesNotOverrideSchedulable(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "account_repo.go", nil, 0)
	if err != nil {
		t.Fatalf("parse account_repo.go: %v", err)
	}

	var clearError *ast.FuncDecl
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if ok && method.Recv != nil && method.Name.Name == "ClearError" {
			clearError = method
			break
		}
	}
	if clearError == nil {
		t.Fatal("ClearError method not found")
	}

	var setsStatus, clearsMessage, setsSchedulable bool
	ast.Inspect(clearError.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "SetStatus":
			setsStatus = true
		case "SetErrorMessage":
			clearsMessage = true
		case "SetSchedulable":
			setsSchedulable = true
		}
		return true
	})

	if !setsStatus || !clearsMessage {
		t.Fatal("ClearError must still restore status and clear the error message")
	}
	if setsSchedulable {
		t.Fatal("ClearError must not call SetSchedulable")
	}
}
