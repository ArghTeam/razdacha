package lists

import (
	"bytes"
	"errors"
	"os"
	"regexp/syntax"
	"strings"
	"testing"
)

func TestNativeRegexEstimateBoundsPinnedProgram(t *testing.T) {
	for _, pattern := range []string{"", "a", "a{0}", "(?:|a){0,3}", "(a?)*", "(?i:abc)", `[\pL]`, `(?:\pL){3}`, "(?P<c>a){1,3}", "^$", `\b.*\B`, strings.Repeat("a{1000}", 4)} {
		t.Run(pattern, func(t *testing.T) {
			parsed, err := syntax.Parse(pattern, syntax.Perl)
			if err != nil {
				t.Fatal(err)
			}
			cost, err := estimateNativeRegex(parsed)
			if err != nil {
				t.Fatal(err)
			}
			// Only these tiny bounded programs are compiled to check the estimator;
			// admission never allocates an expanded program merely to measure its size.
			program, err := syntax.Compile(parsed.Simplify())
			if err != nil {
				t.Fatal(err)
			}
			runes := 0
			for _, instruction := range program.Inst {
				runes += len(instruction.Rune)
			}
			if len(program.Inst) > cost.instructions+2 || runes > cost.runes || cost.instructions < 1 {
				t.Fatalf("underestimated program: instructions=%d/%d runes=%d/%d", len(program.Inst), cost.instructions+2, runes, cost.runes)
			}
		})
	}
	if regexCostMultiply(50000, 50000) != nativeRegexCostCeiling || regexCostAdd(nativeRegexCostCeiling, 1) != nativeRegexCostCeiling {
		t.Fatal("regex complexity arithmetic did not saturate")
	}
}

func TestNativeRegexAggregateBudgets(t *testing.T) {
	t.Run("instruction-boundary", func(t *testing.T) {
		var budget nativeRegexBudget
		pattern := strings.Repeat("a{1000}", 4)
		for i := 0; i < 8; i++ {
			if err := budget.admit(pattern); err != nil {
				t.Fatalf("bounded program %d: %v", i, err)
			}
		}
		if err := budget.admit(pattern); !errors.Is(err, ErrUnsupportedNative) {
			t.Fatal("ninth duplicate expanded program was admitted")
		}
	})
	t.Run("count", func(t *testing.T) {
		var budget nativeRegexBudget
		for i := 0; i < maxNativeRegexCount; i++ {
			if err := budget.admit("a"); err != nil {
				t.Fatal(err)
			}
		}
		if err := budget.admit("a"); err == nil {
			t.Fatal("many cheap patterns bypassed regex count")
		}
	})
	t.Run("unicode-AST", func(t *testing.T) {
		var budget nativeRegexBudget
		for i := 0; i < 100; i++ {
			if err := budget.admit(`\pL`); err != nil {
				if budget.ast == 0 || budget.ast > maxNativeRegexAST {
					t.Fatal("invalid Unicode AST accounting")
				}
				return
			}
		}
		t.Fatal("short Unicode classes bypassed aggregate AST/rune storage")
	})
	t.Run("expanded-class-runes", func(t *testing.T) {
		var budget nativeRegexBudget
		if err := budget.admit(`(?:\pL){100}`); err == nil {
			t.Fatal("expanded Unicode rune cost bypassed admission")
		}
	})
	t.Run("raw-parser-work", func(t *testing.T) {
		var budget nativeRegexBudget
		pattern := strings.Repeat("(?:a{0})", 400)
		for i := 0; i < maxNativeRegexBytes/len(pattern); i++ {
			if err := budget.admit(pattern); err != nil {
				t.Fatal(err)
			}
		}
		if err := budget.admit(pattern); err == nil {
			t.Fatal("raw parser-work byte budget bypassed")
		}
	})
}

func TestNativeRegexBudgetJSONSRSAndLastGood(t *testing.T) {
	pattern := strings.Repeat("a{1000}", 4)
	quoted := `"` + pattern + `"`
	rules := make([]string, 9)
	for i := range rules {
		rules[i] = `{"domain_regex":[` + quoted + `]}`
	}
	body := nativeBody(strings.Join(rules, ","))
	for _, format := range []Format{FormatJSON, FormatSRS} {
		candidate := body
		if format == FormatSRS {
			candidate = nativeBinary(t, body)
		}
		if _, err := ProjectNative(candidate, format); !errors.Is(err, ErrUnsupportedNative) || !strings.Contains(err.Error(), "regex complexity budget") {
			t.Fatalf("%s cross-leaf regex budget missing: %v", format, err)
		}
	}
	f, err := NewFetcher(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example.invalid/regex.json"
	if _, err = f.acceptNative(url, nativeBody(`{"domain_regex":["^last-good[.]example$"]}`), meta{URL: url, ETag: "good"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.nativeManifest(url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.acceptNative(url, body, meta{URL: url, ETag: "bad"}); !errors.Is(err, ErrUnsupportedNative) {
		t.Fatal("expanded regex candidate accepted")
	}
	after, err := os.ReadFile(f.nativeManifest(url))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("regex admission failure changed last-good body/validators")
	}
}
