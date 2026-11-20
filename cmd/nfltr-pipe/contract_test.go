// Copyright 2026 nfltr
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nfltr/relay/client/nfnet"
	"github.com/nfltr/relay/client/relaytrust"
)

// docs/v1-contract.md lists exactly what the code has: each command's
// flags (its -h), every environment variable the command reads, and the
// exit table, the same as the usage text's and named as the relay SDK
// names its codes, every code of the SDK's in it.
func TestContractDoc(t *testing.T) {
	b, err := os.ReadFile("../../docs/v1-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	code := map[string][]string{}
	for _, cmd := range []string{"listen", "dial"} {
		for _, f := range flagsOf(t, cmd) {
			code[f] = append(code[f], cmd)
		}
	}
	documented := map[string][]string{}
	for _, m := range regexp.MustCompile("(?m)^\\| `--([a-z0-9-]+)[^`]*` \\| ([a-z, ]+) \\|").FindAllStringSubmatch(doc, -1) {
		documented[m[1]] = strings.Split(m[2], ", ")
	}
	sameMap(t, "flags (name: commands)", code, documented)

	var docEnv []string
	for _, m := range regexp.MustCompile("(?m)^\\| `\\$([A-Z0-9_]+)` \\|").FindAllStringSubmatch(doc, -1) {
		docEnv = append(docEnv, m[1])
	}
	same(t, "environment variables", envRead(t), docEnv)

	exits := map[string]string{} // exit -> name, from the doc
	for _, m := range regexp.MustCompile("(?m)^\\| (\\d+) \\|\\s*`?([a-z_0-9]*)`?\\s*\\|").FindAllStringSubmatch(doc, -1) {
		exits[m[1]] = m[2]
	}
	usageExits := map[string]string{}
	block := usage[strings.Index(usage, "Exit status"):strings.Index(usage, "Flags:")]
	for _, m := range regexp.MustCompile(`(?m)^  (\d+)\s+(?:([a-z_0-9]+):)?`).FindAllStringSubmatch(block, -1) {
		usageExits[m[1]] = m[2]
	}
	sameMap(t, "exit codes (usage text against the doc)", one(usageExits), one(exits))
	for n := 1; n < 32; n++ {
		name := nfnet.Code(n).String()
		if strings.HasPrefix(name, "code(") {
			continue
		}
		if exits[strconv.Itoa(n)] != name {
			t.Errorf("the relay SDK's code %d (%s) is %q in the doc's exit table", n, name, exits[strconv.Itoa(n)])
		}
	}
	if !strings.Contains(doc, "`nfltr-pipe version`") || !strings.Contains(usage, "nfltr-pipe version") {
		t.Error("the version command is missing from the doc or the usage text")
	}
}

// flagsOf is cmd's flag names, as its -h prints them.
func flagsOf(t *testing.T, cmd string) []string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	cli([]string{cmd, "-h"})
	os.Stderr = stderr
	_ = w.Close()
	out, _ := io.ReadAll(r)
	var fs []string
	for _, m := range regexp.MustCompile(`(?m)^  -([a-z0-9-]+)`).FindAllStringSubmatch(string(out), -1) {
		fs = append(fs, m[1])
	}
	if len(fs) == 0 {
		t.Fatalf("%s -h listed no flag:\n%s", cmd, out)
	}
	return fs
}

// envRead is every variable the command's code reads: the first argument
// of each os.Getenv, os.LookupEnv and env call outside the tests and the
// trace build.
func envRead(t *testing.T) []string {
	t.Helper()
	named := map[string]string{"relaytrust.PinEnv": relaytrust.PinEnv}
	files, _ := filepath.Glob("*.go")
	var vars []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "trace_") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || len(c.Args) == 0 {
				return true
			}
			fn := ""
			switch x := c.Fun.(type) {
			case *ast.Ident:
				fn = x.Name
			case *ast.SelectorExpr:
				if p, ok := x.X.(*ast.Ident); ok {
					fn = p.Name + "." + x.Sel.Name
				}
			}
			if fn != "env" && fn != "os.Getenv" && fn != "os.LookupEnv" {
				return true
			}
			switch a := c.Args[0].(type) {
			case *ast.BasicLit:
				v, _ := strconv.Unquote(a.Value)
				vars = append(vars, v)
			case *ast.SelectorExpr:
				k := a.X.(*ast.Ident).Name + "." + a.Sel.Name
				if named[k] == "" {
					t.Errorf("%s reads the variable %s: name its value in envRead", f, k)
				}
				vars = append(vars, named[k])
			case *ast.Ident: // env's own parameter
			default:
				t.Errorf("%s: an environment read this test cannot name", f)
			}
			return true
		})
	}
	return vars
}

func one(m map[string]string) map[string][]string {
	o := map[string][]string{}
	for k, v := range m {
		o[k] = []string{v}
	}
	return o
}

func same(t *testing.T, what string, code, doc []string) {
	t.Helper()
	slices.Sort(code)
	slices.Sort(doc)
	if !slices.Equal(slices.Compact(code), slices.Compact(doc)) {
		t.Errorf("%s: the code has %q, docs/v1-contract.md %q", what, code, doc)
	}
}

func sameMap(t *testing.T, what string, code, doc map[string][]string) {
	t.Helper()
	for k, v := range code {
		slices.Sort(v)
		d := slices.Clone(doc[k])
		slices.Sort(d)
		if !slices.Equal(v, d) {
			t.Errorf("%s: %s is %q in the code, %q in docs/v1-contract.md", what, k, v, d)
		}
	}
	for k := range doc {
		if _, ok := code[k]; !ok {
			t.Errorf("%s: docs/v1-contract.md has %s, the code does not", what, k)
		}
	}
}
