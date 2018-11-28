// Copyright (c) 2018, The GoKi Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parse

import (
	"fmt"

	"github.com/goki/pi/lex"
	"github.com/goki/pi/token"
)

// parse.State is the state maintained for parsing
type State struct {
	Src        *lex.File     `desc:"source and lexed version of source we're parsing"`
	Ast        *Ast          `desc:"root of the Ast abstract syntax tree we're updating"`
	EosPos     []lex.Pos     `desc:"positions *in token coordinates* of the EOS markers generated"`
	EosIdx     int           `desc:"index in list of Eos tokens that we're currently on"`
	Pos        lex.Pos       `desc:"the current lex token position"`
	ScopeStack []lex.Reg     `desc:"scope stack  *in token coordinates* of regions for looking for tokens"`
	State      []string      `desc:"state stack"`
	Errs       lex.ErrorList `desc:"any error messages accumulated during parsing specifically"`
}

// Init initializes the state at start of parsing
func (ps *State) Init(src *lex.File, ast *Ast, eospos []lex.Pos) {
	ps.Src = src
	ps.Ast = ast
	ps.Ast.DeleteChildren(true)
	ps.EosPos = eospos
	ps.State = nil
	ps.Pos, _ = ps.Src.ValidTokenPos(lex.PosZero)
	ps.EosIdx = 0
	ps.Errs.Reset()
}

// Error adds a parsing error at given lex token position
func (ps *State) Error(pos lex.Pos, msg string) {
	if pos != lex.PosZero {
		pos = ps.Src.TokenSrcPos(pos).St
	}
	ps.Errs.Add(pos, ps.Src.Filename, "Parser: "+msg)
	fmt.Println("ERROR: " + ps.Errs[len(ps.Errs)-1].Error())
}

// AtEof returns true if current position is at end of file
func (ps *State) AtEof() bool {
	return ps.Pos.Ln >= ps.Src.NLines()
}

// MatchLex is our optimized matcher method
func (ps *State) MatchLex(lx *lex.Lex, tkey token.KeyToken, isCat, isSubCat bool, stdepth int, cp lex.Pos) bool {
	if lx.Depth != stdepth {
		return false
	}
	if !(lx.Tok == tkey.Tok || (isCat && lx.Tok.Cat() == tkey.Tok) || (isSubCat && lx.Tok.SubCat() == tkey.Tok)) {
		return false
	}
	if tkey.Key == "" {
		return true
	}
	return tkey.Key == string(ps.Src.TokenSrc(cp))
}

// FindToken looks for token in given region, returns position where found, false if not.
// Only matches when depth is same as at reg.St start at the start of the search.
// All positions in token indexes.
func (ps *State) FindToken(tkey token.KeyToken, reg lex.Reg) (lex.Pos, bool) {
	cp, ok := ps.Src.ValidTokenPos(reg.St)
	if !ok {
		return cp, false
	}
	tok := tkey.Tok
	isCat := tok.Cat() == tok
	isSubCat := tok.SubCat() == tok
	stlx := ps.Src.LexAt(cp)
	for cp.IsLess(reg.Ed) {
		lx := ps.Src.LexAt(cp)
		if ps.MatchLex(lx, tkey, isCat, isSubCat, stlx.Depth, cp) {
			return cp, true
		}
		cp, ok = ps.Src.NextTokenPos(cp)
		if !ok {
			return cp, false
		}
	}
	return cp, false
}

// MatchToken returns true if token matches at given position -- must be
// a valid position!
func (ps *State) MatchToken(tkey token.KeyToken, pos lex.Pos) bool {
	tok := tkey.Tok
	isCat := tok.Cat() == tok
	isSubCat := tok.SubCat() == tok
	lx := ps.Src.LexAt(pos)
	return ps.MatchLex(lx, tkey, isCat, isSubCat, lx.Depth, pos)
}

// FindTokenReverse looks *backwards* for token in given region, with same depth as reg.Ed-1 end
// where the search starts. Returns position where found, false if not.
// Automatically deals with possible confusion with unary operators -- if there are two
// ambiguous operators in a row, automatically gets the first one.  This is mainly / only used for
// binary operator expressions (mathematical binary operators).
// All positions are in token indexes.
func (ps *State) FindTokenReverse(tkey token.KeyToken, reg lex.Reg) (lex.Pos, bool) {
	cp, ok := ps.Src.PrevTokenPos(reg.Ed)
	if !ok {
		return cp, false
	}
	tok := tkey.Tok
	isCat := tok.Cat() == tok
	isSubCat := tok.SubCat() == tok
	isAmbigUnary := tok.IsAmbigUnaryOp()
	stlx := ps.Src.LexAt(cp)
	for reg.St.IsLess(cp) {
		lx := ps.Src.LexAt(cp)
		if ps.MatchLex(lx, tkey, isCat, isSubCat, stlx.Depth, cp) {
			if isAmbigUnary { // make sure immed prior is not also!
				pp, ok := ps.Src.PrevTokenPos(cp)
				if ok {
					pt := ps.Src.Token(pp)
					if !pt.IsAmbigUnaryOp() {
						return cp, true
					}
					// otherwise we don't match -- cannot match second opr
				} else {
					return cp, true
				}
			} else {
				return cp, true
			}
		}
		ok := false
		cp, ok = ps.Src.PrevTokenPos(cp)
		if !ok {
			return cp, false
		}
	}
	return cp, false
}

// AddAst adds a child Ast node to given parent Ast node
func (ps *State) AddAst(parAst *Ast, rule string, reg lex.Reg) *Ast {
	chAst := parAst.AddNewChild(KiT_Ast, rule).(*Ast)
	chAst.SetTokReg(reg, ps.Src)
	return chAst
}

func (ps *State) PushState(st string) {
	ps.State = append(ps.State, st)
}

func (ps *State) CurState() string {
	sz := len(ps.State)
	if sz == 0 {
		return ""
	}
	return ps.State[sz-1]
}

func (ps *State) PopState() string {
	sz := len(ps.State)
	if sz == 0 {
		return ""
	}
	st := ps.CurState()
	ps.State = ps.State[:sz-1]
	return st
}

func (ps *State) PushScope(reg lex.Reg) {
	ps.ScopeStack = append(ps.ScopeStack, reg)
}

func (ps *State) CurScope() (lex.Reg, bool) {
	sz := len(ps.ScopeStack)
	if sz == 0 {
		return lex.Reg{}, false
	}
	return ps.ScopeStack[sz-1], true
}

func (ps *State) PopScope() (lex.Reg, bool) {
	sz := len(ps.ScopeStack)
	if sz == 0 {
		return lex.Reg{}, false
	}
	st, _ := ps.CurScope()
	ps.ScopeStack = ps.ScopeStack[:sz-1]
	return st, true
}