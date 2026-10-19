package lists

import (
	"fmt"
	"regexp/syntax"
)

const (
	maxNativeRegexCount        = 1024
	maxNativeRegexBytes        = 32 << 10
	maxNativeRegexAST          = 64 << 10
	maxNativeRegexInstructions = 64 << 10
	maxNativeRegexRunes        = 128 << 10
	nativeRegexCostCeiling     = maxNativeRegexRunes + 1
)

// Programs are loaded independently in original routing and exact DNS sets.
// Charge both copies before Simplify/Compile can expand counted repetitions.
type (
	nativeRegexBudget struct{ count, bytes, ast, instructions, runes int }
	nativeRegexCost   struct{ ast, instructions, runes int }
)

func regexCostAdd(a, b int) int {
	if a >= nativeRegexCostCeiling-b {
		return nativeRegexCostCeiling
	}
	return a + b
}

func regexCostMultiply(a, b int) int {
	if a != 0 && b > nativeRegexCostCeiling/a {
		return nativeRegexCostCeiling
	}
	return a * b
}

func (b *nativeRegexBudget) admit(pattern string) error {
	bad := func() error { return fmt.Errorf("%w: native regex complexity budget", ErrUnsupportedNative) }
	if b.count >= maxNativeRegexCount || len(pattern) > maxNativeRegexBytes-b.bytes {
		return bad()
	}
	// The total raw-byte limit also bounds parser work for classes which compact
	// after Unicode expansion; post-parse AST size alone would not account for it.
	b.count++
	b.bytes += len(pattern)
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return fmt.Errorf("%w: invalid domain regex", ErrUnsupportedNative)
	}
	cost, err := estimateNativeRegex(parsed)
	if err != nil {
		return err
	}
	ast := regexCostMultiply(cost.ast, 2)
	instructions := regexCostMultiply(regexCostAdd(cost.instructions, 2), 2)
	runes := regexCostMultiply(cost.runes, 2)
	if ast > maxNativeRegexAST-b.ast || instructions > maxNativeRegexInstructions-b.instructions || runes > maxNativeRegexRunes-b.runes {
		return bad()
	}
	b.ast += ast
	b.instructions += instructions
	b.runes += runes
	return nil
}

// This conservative estimate follows Go regexp/syntax's compact-tree calcSize
// rules. Saturating arithmetic runs before repeat expansion or program allocation.
func estimateNativeRegex(re *syntax.Regexp) (nativeRegexCost, error) {
	cost := nativeRegexCost{ast: regexCostAdd(1, len(re.Rune))}
	var children []nativeRegexCost
	for _, sub := range re.Sub {
		child, err := estimateNativeRegex(sub)
		if err != nil {
			return cost, err
		}
		cost.ast = regexCostAdd(cost.ast, child.ast)
		children = append(children, child)
	}
	sum := func() {
		for _, child := range children {
			cost.instructions = regexCostAdd(cost.instructions, child.instructions)
			cost.runes = regexCostAdd(cost.runes, child.runes)
		}
	}
	switch re.Op {
	case syntax.OpNoMatch, syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		cost.instructions = 1
	case syntax.OpLiteral:
		cost.instructions = len(re.Rune)
		cost.runes = len(re.Rune)
	case syntax.OpCharClass:
		cost.instructions = 1
		cost.runes = len(re.Rune)
	case syntax.OpAnyChar:
		cost.instructions = 1
		cost.runes = 2
	case syntax.OpAnyCharNotNL:
		cost.instructions = 1
		cost.runes = 4
	case syntax.OpCapture, syntax.OpStar, syntax.OpPlus, syntax.OpQuest:
		sum()
		extra := 1
		if re.Op == syntax.OpCapture || re.Op == syntax.OpStar {
			extra = 2
		}
		cost.instructions = regexCostAdd(cost.instructions, extra)
	case syntax.OpConcat:
		sum()
	case syntax.OpAlternate:
		sum()
		if len(children) > 1 {
			cost.instructions = regexCostAdd(cost.instructions, len(children)-1)
		}
	case syntax.OpRepeat:
		if len(children) != 1 {
			return cost, fmt.Errorf("%w: regex repeat structure", ErrUnsupportedNative)
		}
		copies, extra := re.Max, re.Max-re.Min
		if re.Max < 0 {
			copies = re.Min
			if copies < 1 {
				copies = 1
			}
			extra = 2
		}
		cost.instructions = regexCostAdd(regexCostMultiply(copies, children[0].instructions), extra)
		cost.runes = regexCostMultiply(copies, children[0].runes)
	default:
		return cost, fmt.Errorf("%w: regex operation", ErrUnsupportedNative)
	}
	if cost.instructions < 1 {
		cost.instructions = 1
	}
	return cost, nil
}
