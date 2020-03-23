// Copyright (c) 2018, The GoKi Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/goki/ki/dirs"
	"github.com/goki/pi/filecat"
	"github.com/goki/pi/pi"
	"github.com/goki/pi/syms"
	"github.com/goki/pi/token"
	"golang.org/x/tools/go/packages"
)

// ParseDirLock provides a lock protecting parsing of a package directory
type ParseDirLock struct {
	Path string
	Mu   sync.Mutex `json:"-" xml:"-" desc:"mutex protecting processing of this path"`
}

// ParseDirLocks manages locking for parsing package directories
type ParseDirLocks struct {
	Dirs map[string]*ParseDirLock `desc:"map of paths with processing status"`
	Mu   sync.Mutex               `json:"-" xml:"-" desc:"mutex protecting access to Dirs"`
}

// TheParseDirs is the parse dirs locking manager
var TheParseDirs ParseDirLocks

// ParseDir is how you call ParseDir on a given path in a secure way that is
// managed for multiple accesses.  If dir is currently being parsed, then
// the mutex is locked and caller will wait until that is done -- at which point
// the next one should be able to load parsed symbols instead of parsing fresh.
// Once the symbols are returned, then the local FileState SymsMu lock must be
// used when integrating any external symbols back into another filestate.
// As long as all the symbol resolution etc is all happening outside of the
// external syms linking, then it does not need to be protected.
func (pd *ParseDirLocks) ParseDir(gl *GoLang, path string, opts pi.LangDirOpts) *syms.Symbol {
	pfld := strings.Fields(path)
	if len(pfld) > 1 { // remove first alias
		path = pfld[1]
	}
	pd.Mu.Lock()
	if pd.Dirs == nil {
		pd.Dirs = make(map[string]*ParseDirLock)
	}
	ds, has := pd.Dirs[path]
	if !has {
		ds = &ParseDirLock{Path: path}
		pd.Dirs[path] = ds
	}
	pd.Mu.Unlock()
	ds.Mu.Lock()
	rsym := gl.ParseDirImpl(path, opts)
	ds.Mu.Unlock()
	return rsym
}

// ParseDirExcludes are files to exclude in processing directories
// because they take a long time and aren't very useful (data files).
// Any file that contains one of these strings is excluded.
var ParseDirExcludes = []string{
	"/image/font/gofont/",
	"zerrors_",
	"unicode/tables.go",
	"filecat/mimetype.go",
	"/html/entity.go",
	"/draw/impl.go",
	"/truetype/hint.go",
	"/runtime/proc.go",
}

// ParseDir is the interface call for parsing a directory
func (gl *GoLang) ParseDir(path string, opts pi.LangDirOpts) *syms.Symbol {
	if path == "" || path == "C" || path[0] == '_' {
		return nil
	}
	return TheParseDirs.ParseDir(gl, path, opts)
}

// ParseDirImpl does the actual work of parsing a directory.
// Path is assumed to be a package import path or a local file name
func (gl *GoLang) ParseDirImpl(path string, opts pi.LangDirOpts) *syms.Symbol {
	var files []string
	var pkgPathAbs string
	gm := os.Getenv("GO111MODULE")
	if filepath.IsAbs(path) {
		pkgPathAbs = path
		files = dirs.ExtFileNames(pkgPathAbs, []string{".go"})
		if len(files) == 0 {
			// fmt.Printf("No go files, bailing\n")
			return nil
		}
		for i, pt := range files {
			files[i] = filepath.Join(pkgPathAbs, pt)
		}
	} else if gm == "off" { // note: using GOPATH manual mechanism as packages.Load is somehow very slow
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			path, err = dirs.GoSrcDir(path)
			if err != nil {
				if TraceTypes {
					log.Println(err)
				}
				return nil
			}
		} else if err != nil {
			log.Println(err.Error())
			return nil
		}
		pkgPathAbs, _ = filepath.Abs(path)
		// fmt.Printf("Parsing, loading path: %v\n", path)

		files = dirs.ExtFileNames(path, []string{".go"})
		if len(files) == 0 {
			// fmt.Printf("No go files, bailing\n")
			return nil
		}
		for i, pt := range files {
			files[i] = filepath.Join(pkgPathAbs, pt)
		}
	} else {
		// packages automatically deals with GOPATH vs. modules, etc.
		pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedFiles}, path)
		if err != nil {
			log.Println(err)
			return nil
		}
		if len(pkgs) != 1 {
			fmt.Printf("More than one package for path: %v\n", path)
			return nil
		}
		pkg := pkgs[0]

		if len(pkg.GoFiles) == 0 {
			// fmt.Printf("No Go files found in package: %v\n", path)
			return nil
		}
		files = pkg.GoFiles
		fgo := files[0]
		pkgPathAbs = filepath.Dir(fgo)
		// fmt.Printf("GO111MODULE: %v  package: %v PkgPath: %s\n", gm, path, pkgPathAbs)
	}

	if !opts.Rebuild {
		csy, cts, err := syms.OpenSymCache(filecat.Go, pkgPathAbs)
		if err == nil && csy != nil {
			sydir := filepath.Dir(csy.Filename)
			diffPath := sydir != pkgPathAbs
			// if diffPath {
			// 	fmt.Printf("rebuilding %v because path: %v != cur path: %v\n", path, sydir, pkgPathAbs)
			// }
			if diffPath || (!gl.Pr.ModTime.IsZero() && cts.Before(gl.Pr.ModTime)) {
				// fmt.Printf("rebuilding %v because parser: %v is newer than cache: %v\n", path, gl.Pr.ModTime, cts)
			} else {
				lstmod := dirs.LatestMod(pkgPathAbs, []string{".go"})
				if lstmod.Before(cts) {
					// fmt.Printf("loaded cache for: %v from: %v\n", pkgPathAbs, cts)
					return csy
				}
			}
		}
	}

	pr := gl.Parser()
	var pkgsym *syms.Symbol
	var fss []*pi.FileState // file states for each file
	for _, fpath := range files {
		fnm := filepath.Base(fpath)
		if strings.HasSuffix(fnm, "_test.go") {
			continue
		}
		// avoid processing long slow files that aren't needed anyway:
		excl := false
		for _, ex := range ParseDirExcludes {
			if strings.Contains(fpath, ex) {
				excl = true
				break
			}
		}
		if excl {
			continue
		}
		fs := pi.NewFileState() // we use a separate fs for each file, so we have full ast
		fss = append(fss, fs)
		// optional monitoring of parsing
		// fs.ParseState.Trace.On = true
		// fs.ParseState.Trace.Match = true
		// fs.ParseState.Trace.NoMatch = true
		// fs.ParseState.Trace.Run = true
		// fs.ParseState.Trace.RunAct = true
		// fs.ParseState.Trace.StdOut()
		err := fs.Src.OpenFile(fpath)
		if err != nil {
			continue
		}
		// fmt.Printf("parsing file: %v\n", fnm)
		// stt := time.Now()
		pr.LexAll(fs)
		// lxdur := time.Now().Sub(stt)
		pr.ParseAll(fs)
		// prdur := time.Now().Sub(stt)
		// pdms := prdur / time.Millisecond
		// if pdms > 500 {
		// 	fmt.Printf("file: %s full parse: %v\n", fpath, prdur)
		// }
		if len(fs.ParseState.Scopes) > 0 { // should be
			pkg := fs.ParseState.Scopes[0]
			gl.DeleteUnexported(pkg, pkg.Name)
			if pkgsym == nil {
				pkgsym = pkg
			} else {
				pkgsym.CopyFromScope(pkg)
				if TraceTypes {
					pkgsym.Types.PrintUnknowns()
				}
			}
			// } else {
			// 	fmt.Printf("\tno parse state scopes!\n")
		}
	}
	if pkgsym == nil || len(fss) == 0 {
		return nil
	}
	pfs := fss[0]                       // pi.NewFileState()            // master overall package file state
	gl.ResolveTypes(pfs, pkgsym, false) // false = don't include function-internal scope items
	gl.DeleteExternalTypes(pkgsym)
	if !opts.Nocache {
		syms.SaveSymCache(pkgsym, filecat.Go, pkgPathAbs)
	}
	return pkgsym
}

/////////////////////////////////////////////////////////////////////////////
// Go util funcs

// DeleteUnexported deletes lower-case unexported items from map, and
// children of symbols on map
func (gl *GoLang) DeleteUnexported(sy *syms.Symbol, pkgsc string) {
	if sy.Kind.SubCat() != token.NameScope { // only for top-level scopes
		return
	}
	for nm, ss := range sy.Children {
		if ss == sy {
			fmt.Printf("warning: child is self!: %v\n", sy.String())
			delete(sy.Children, nm)
			continue
		}
		if ss.Kind.SubCat() != token.NameScope { // typically lowercase
			rn, _ := utf8.DecodeRuneInString(nm)
			if nm == "" || unicode.IsLower(rn) {
				delete(sy.Children, nm)
				continue
			}
			// sc, has := ss.Scopes[token.NamePackage]
			// if has && sc != pkgsc {
			// 	fmt.Printf("excluding out-of-scope symbol: %v  %v\n", sc, ss.String())
			// 	delete(sy.Children, nm)
			// 	continue
			// }
		}
		if ss.HasChildren() {
			gl.DeleteUnexported(ss, pkgsc)
		}
	}
}

// DeleteExternalTypes deletes types from outside current package scope.
// These can be created during ResolveTypes but should be deleted before
// saving symbol type.
func (gl *GoLang) DeleteExternalTypes(sy *syms.Symbol) {
	pkgsc := sy.Name
	for nm, ty := range sy.Types {
		sc, has := ty.Scopes[token.NamePackage]
		if has && sc != pkgsc {
			// fmt.Printf("excluding out-of-scope type: %v  %v\n", sc, ty.String())
			delete(sy.Types, nm)
			continue
		}
	}
}

// ImportPathPkg returns the package (last dir) and base of import path
// from import path string -- removes any quotes around path first.
func (gl *GoLang) ImportPathPkg(im string) (path, base, pkg string) {
	sz := len(im)
	if sz == 0 {
		return
	}
	path = im
	if im[0] == '"' {
		path = im[1 : sz-1]
	}
	base, pkg = filepath.Split(path)
	return
}

// PkgSyms attempts to find package symbols for given package name.
// Is also passed any current package symbol context in psyms which might be
// different from default filestate context.
func (gl *GoLang) PkgSyms(fs *pi.FileState, psyms syms.SymMap, pnm string) (*syms.Symbol, bool) {
	psym, has := fs.ExtSyms[pnm]
	if has {
		return psym, has
	}
	ipsym, has := gl.FindImportPkg(fs, psyms, pnm) // look for import within psyms package symbols
	if has {
		gl.AddImportToExts(fs, ipsym.Name, false) // no lock
		psym, has = fs.ExtSyms[pnm]
	}
	return psym, has
}

// AddImportsToExts adds imports from given package into pi.FileState.ExtSyms list
// imports are coded as NameLibrary symbols with names = import path
func (gl *GoLang) AddImportsToExts(fss *pi.FileStates, pfs *pi.FileState, pkg *syms.Symbol) {
	var imps syms.SymMap
	pfs.SymsMu.RLock()
	pkg.Children.FindKindScoped(token.NameLibrary, &imps)
	pfs.SymsMu.RUnlock()
	if len(imps) == 0 {
		return
	}
	for _, im := range imps {
		if im.Name == "C" {
			continue
		}
		pfs.WaitGp.Add(1)
		go gl.AddImportToExts(pfs, im.Name, true) // lock
	}
	pfs.WaitGp.Wait() // each goroutine will do done when done..
	// now all the info is in place: parse it
	if TraceTypes {
		fmt.Printf("\n#####################\nResolving Types now for: %v\n", pfs.Src.Filename)
	}
	gl.ResolveTypes(pfs, pkg, true) // true = do include function-internal scope items
}

// AddImportToExts adds given import into pi.FileState.ExtSyms list
// assumed to be called as a separate goroutine
func (gl *GoLang) AddImportToExts(fs *pi.FileState, im string, lock bool) {
	im, _, pkg := gl.ImportPathPkg(im)
	psym := gl.ParseDir(im, pi.LangDirOpts{})
	if psym != nil {
		psym.Name = pkg
		if lock {
			fs.SymsMu.Lock()
		}
		gl.AddPkgToExts(fs, psym)
		if lock {
			fs.SymsMu.Unlock()
		}
	}
	if lock {
		fs.WaitGp.Done()
	}
}

// AddPathToSyms adds given path into pi.FileState.Syms list
// Is called as a separate goroutine in ParseFile with WaitGp
func (gl *GoLang) AddPathToSyms(fs *pi.FileState, path string) {
	psym := gl.ParseDir(path, pi.LangDirOpts{})
	if psym != nil {
		gl.AddPkgToSyms(fs, psym)
	}
	fs.WaitGp.Done()
}

// AddPkgToSyms adds given package symbol, with children from package
// to pi.FileState.Syms map -- merges with anything already there
// does NOT add imports -- that is an optional second step.
// Returns true if there was an existing entry for this package.
func (gl *GoLang) AddPkgToSyms(fs *pi.FileState, pkg *syms.Symbol) bool {
	fs.SymsMu.Lock()
	psy, has := fs.Syms[pkg.Name]
	if has {
		// fmt.Printf("AddPkgToSyms: importing pkg types: %v\n", pkg.Name)
		psy.CopyFromScope(pkg)
		if TraceTypes {
			psy.Types.PrintUnknowns()
		}
	} else {
		fs.Syms[pkg.Name] = pkg
	}
	fs.SymsMu.Unlock()
	return has
}

// AddPathToExts adds given path into pi.FileState.ExtSyms list
// assumed to be called as a separate goroutine
func (gl *GoLang) AddPathToExts(fs *pi.FileState, path string) {
	psym := gl.ParseDir(path, pi.LangDirOpts{})
	if psym != nil {
		gl.AddPkgToExts(fs, psym)
	}
}

// AddPkgToExts adds given package symbol, with children from package
// to pi.FileState.ExtSyms map -- merges with anything already there
// does NOT add imports -- that is an optional second step.
// Returns true if there was an existing entry for this package.
func (gl *GoLang) AddPkgToExts(fs *pi.FileState, pkg *syms.Symbol) bool {
	psy, has := fs.ExtSyms[pkg.Name]
	if has {
		psy.CopyFromScope(pkg)
		pkg = psy
	} else {
		if fs.ExtSyms == nil {
			fs.ExtSyms = make(syms.SymMap)
		}
		fs.ExtSyms[pkg.Name] = pkg
	}
	return has
}

// FindImportPkg attempts to find an import package based on symbols in
// an existing package.  For indirect loading of packages from other packages
// that we don't direct import.
func (gl *GoLang) FindImportPkg(fs *pi.FileState, psyms syms.SymMap, nm string) (*syms.Symbol, bool) {
	for _, sy := range psyms {
		if sy.Kind != token.NameLibrary {
			continue
		}
		_, _, pkg := gl.ImportPathPkg(sy.Name)
		if pkg == nm {
			return sy, true
		}
	}
	return nil, false
}