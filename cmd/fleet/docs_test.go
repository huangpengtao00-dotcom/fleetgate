package main

// The README and docs/design.md make claims about the code: which exit codes
// exist, which worker states exist, which tests prove which rule, which
// commands the CLI takes. Each claim is a second copy of a fact whose first
// copy is in the code. These tests fail when the two copies disagree, in
// either direction.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/huangpengtao00-dotcom/fleetgate/internal/gate"
	"github.com/huangpengtao00-dotcom/fleetgate/internal/ledger"
)

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// table returns the rows of the markdown table whose header line starts with
// header, as cells with the surrounding pipes removed.
func table(t *testing.T, doc, header string) [][]string {
	t.Helper()
	i := strings.Index(doc, header)
	if i < 0 {
		t.Fatalf("no table with header %q", header)
	}
	var rows [][]string
	for _, line := range strings.Split(doc[i:], "\n")[2:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		var cells []string
		for _, c := range strings.Split(strings.Trim(line, "|"), "|") {
			cells = append(cells, strings.TrimSpace(c))
		}
		rows = append(rows, cells)
	}
	return rows
}

func TestReadmeExitCodesMatchTheCode(t *testing.T) {
	want := map[int]string{}
	for _, c := range gate.Codes {
		want[c.Code] = c.Meaning
	}
	got := map[int]string{}
	for _, row := range table(t, readDoc(t, "README.md"), "| Exit | Meaning |") {
		code, err := strconv.Atoi(row[0])
		if err != nil {
			t.Fatalf("bad exit code cell %q", row[0])
		}
		got[code] = row[1]
	}
	for code, meaning := range want {
		if got[code] != meaning {
			t.Errorf("exit %d: README says %q, code says %q", code, got[code], meaning)
		}
	}
	for code := range got {
		if _, ok := want[code]; !ok {
			t.Errorf("README documents exit %d, which gate.Codes does not define", code)
		}
	}
}

// Every Code* constant in the gate package must appear in gate.Codes, so the
// published table cannot silently omit one.
func TestEveryExitCodeConstantIsPublished(t *testing.T) {
	published := map[int]bool{}
	for _, c := range gate.Codes {
		if published[c.Code] {
			t.Errorf("exit %d listed twice in gate.Codes", c.Code)
		}
		published[c.Code] = true
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "..", "internal", "gate", "gate.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		vs, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok {
				continue
			}
			v, _ := strconv.Atoi(lit.Value)
			n++
			if !published[v] {
				t.Errorf("%s = %d is not in gate.Codes", name.Name, v)
			}
		}
		return true
	})
	if n != len(gate.Codes) {
		t.Errorf("%d Code* constants, %d published codes", n, len(gate.Codes))
	}
}

func TestReadmeWorkerStatesMatchTheCode(t *testing.T) {
	want := map[string]string{}
	for _, s := range ledger.States {
		want[string(s.State)] = s.Condition
	}
	got := map[string]string{}
	for _, row := range table(t, readDoc(t, "README.md"), "| State | Condition |") {
		got[row[0]] = row[1]
	}
	for s, cond := range want {
		if got[s] != cond {
			t.Errorf("state %s: README says %q, code says %q", s, got[s], cond)
		}
	}
	for s := range got {
		if _, ok := want[s]; !ok {
			t.Errorf("README documents state %q, which ledger.States does not define", s)
		}
	}
}

// A test named in design.md as the proof of a rule must exist. Otherwise the
// rule reads as tested when nothing tests it.
func TestDesignCitesOnlyRealTests(t *testing.T) {
	real := map[string]bool{}
	err := filepath.Walk(filepath.Join("..", ".."), func(p string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range regexp.MustCompile(`(?m)^func (Test\w+)\(`).FindAllStringSubmatch(string(b), -1) {
			real[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cited := regexp.MustCompile("`(Test\\w+)`").FindAllStringSubmatch(readDoc(t, "docs/design.md"), -1)
	if len(cited) == 0 {
		t.Fatal("design.md cites no tests; the pattern is probably wrong")
	}
	for _, m := range cited {
		if !real[m[1]] {
			t.Errorf("design.md cites %s, which does not exist", m[1])
		}
	}
}

// Every test that mutate.sh expects to kill a mutation must be one design.md
// cites for a rule (and so, by the test above, one that exists). A renamed test
// would make `go test -run` match nothing; the mutation would then show as
// survived, which is the right verdict for the wrong reason.
func TestMutationsNameRealTests(t *testing.T) {
	src := readDoc(t, "scripts/mutate.sh")
	names := regexp.MustCompile(`(?m)'\s+(Test\w+) \./\S+$`).FindAllStringSubmatch(src, -1)
	if want := strings.Count(src, "\nm \""); len(names) != want {
		t.Fatalf("found %d test names for %d mutations; the pattern is probably wrong", len(names), want)
	}
	design := readDoc(t, "docs/design.md")
	for _, m := range names {
		if !strings.Contains(design, "`"+m[1]+"`") {
			t.Errorf("mutate.sh relies on %s, which design.md does not cite for any rule", m[1])
		}
	}
}

func TestReadmeCommandsMatchTheCLI(t *testing.T) {
	cmds := func(s string) []string {
		set := map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^\s*fleet ([a-z]+)`).FindAllStringSubmatch(s, -1) {
			set[m[1]] = true
		}
		var out []string
		for k := range set {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	readme := readDoc(t, "README.md")
	block := readme[strings.Index(readme, "## Usage"):strings.Index(readme, "$ fleet ledger")]
	got, want := strings.Join(cmds(block), " "), strings.Join(cmds(usage), " ")
	if got != want {
		t.Errorf("README usage lists [%s], the CLI's usage lists [%s]", got, want)
	}
}
