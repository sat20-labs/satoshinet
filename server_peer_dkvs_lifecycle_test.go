package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

func TestServerPeerDisconnectClosesDKVS(t *testing.T) {
	sp := &serverPeer{}
	start, err := sp.dkvsState.StartPathSync("/svc/lifecycle.btc", time.Now())
	if err != nil || start.Request == nil {
		t.Fatalf("start test session: request=%v err=%v", start.Request, err)
	}
	sp.WaitForDisconnect()
	if !sp.dkvsState.Closed() {
		t.Fatal("server connection did not close its DKVS work")
	}
	if _, active := sp.dkvsState.ActivePathSync(); active {
		t.Fatal("closed connection retained an active synchronization")
	}
	start, err = sp.dkvsState.StartPathSync("/svc/lifecycle.btc", time.Now())
	if err != nil || start.Request != nil {
		t.Fatalf("closed connection restarted DKVS: request=%v err=%v", start.Request, err)
	}
	sp.WaitForDisconnect() // all disconnect paths may request cleanup again
}

// Keep the production call site on serverPeer's lifecycle wrapper. Calling
// sp.Peer.WaitForDisconnect directly here would bypass the DKVS cleanup above.
func TestServerPeerDoneUsesConnectionLifecycle(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "peerDoneHandler" {
			continue
		}
		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "WaitForDisconnect" {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "sp" {
				t.Fatal("peerDoneHandler bypasses serverPeer.WaitForDisconnect")
			}
			found = true
			return true
		})
		if !found {
			t.Fatal("peerDoneHandler has no connection lifecycle wait")
		}
		return
	}
	t.Fatal("production peerDoneHandler not found")
}
