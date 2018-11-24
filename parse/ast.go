// Copyright (c) 2018, The GoKi Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package parse does the parsing stage after lexing
package parse

import (
	"github.com/goki/ki"
	"github.com/goki/ki/kit"
	"github.com/goki/pi/lex"
)

// Ast is a node in the abstract syntax tree generated by the parsing step
// the name of the node (from ki.Node) is the type of the element
// (e.g., Expr, Stmt, etc)
// These nodes are generated by the parse.Rule's by matching tokens
type Ast struct {
	ki.Node
	Reg lex.Reg `desc:"region in source file corresponding to this Ast node"`
}

var KiT_Ast = kit.Types.AddType(&Ast{}, AstProps)

var AstProps = ki.Props{
	// "CallMethods": ki.PropSlice{
	// 	{"SaveAs", ki.Props{
	// 		"Args": ki.PropSlice{
	// 			{"File Name", ki.Props{
	// 				"default-field": "Filename",
	// 			}},
	// 		},
	// 	}},
	// },
}
