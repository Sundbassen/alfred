package answer

import (
	"math"
	"strconv"
	"strings"
	"unicode"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// evaluate computes an arithmetic expression the model wrote: numbers,
// + - * / % ^, parentheses and sqrt(…). Anything else is an error, so the
// model's output is never run as code.
func evaluate(expr string) (float64, error) {
	if len(expr) > 200 {
		return 0, errors.WithMessage(fault.ErrInvalidValue, "expression too long")
	}
	p := &parser{s: strings.ReplaceAll(strings.ToLower(expr), "×", "*")}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	p.space()
	if p.i != len(p.s) {
		return 0, errors.WithMessagef(fault.ErrInvalidValue, "unexpected %q in %q", p.s[p.i:], expr)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.WithMessagef(fault.ErrInvalidValue, "%q has no finite value", expr)
	}
	return v, nil
}

type parser struct {
	s     string
	i     int
	depth int
}

func (p *parser) space() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

func (p *parser) peek() byte {
	p.space()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

// expr = term {("+"|"-") term}
func (p *parser) expr() (float64, error) {
	if p.depth++; p.depth > 50 {
		return 0, errors.WithMessage(fault.ErrInvalidValue, "expression nested too deeply")
	}
	defer func() { p.depth-- }()
	v, err := p.term()
	for err == nil {
		switch p.peek() {
		case '+':
			p.i++
			var w float64
			w, err = p.term()
			v += w
		case '-':
			p.i++
			var w float64
			w, err = p.term()
			v -= w
		default:
			return v, nil
		}
	}
	return 0, err
}

// term = unary {("*"|"/"|"%") unary}
func (p *parser) term() (float64, error) {
	v, err := p.unary()
	for err == nil {
		op := p.peek()
		if op != '*' && op != '/' && op != '%' {
			return v, nil
		}
		p.i++
		var w float64
		if w, err = p.unary(); err != nil {
			break
		}
		switch op {
		case '*':
			v *= w
		case '/', '%':
			if w == 0 {
				return 0, errors.WithMessage(fault.ErrInvalidValue, "division by zero")
			}
			if op == '/' {
				v /= w
			} else {
				v = math.Mod(v, w)
			}
		}
	}
	return 0, err
}

// unary = "-" unary | power. Minus binds looser than "^", so -2^2 is -4.
func (p *parser) unary() (float64, error) {
	if p.peek() == '-' {
		p.i++
		v, err := p.unary()
		return -v, err
	}
	return p.power()
}

// power = primary ["^" unary] (right-associative: 2^3^2 is 2^9)
func (p *parser) power() (float64, error) {
	v, err := p.primary()
	if err != nil || p.peek() != '^' {
		return v, err
	}
	p.i++
	e, err := p.unary()
	return math.Pow(v, e), err
}

// primary = number | "(" expr ")" | "sqrt" "(" expr ")"
func (p *parser) primary() (float64, error) {
	switch c := p.peek(); {
	case c == '(':
		p.i++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, errors.WithMessage(fault.ErrInvalidValue, "missing )")
		}
		p.i++
		return v, nil
	case strings.HasPrefix(p.s[p.i:], "sqrt"):
		p.i += len("sqrt")
		if p.peek() != '(' {
			return 0, errors.WithMessage(fault.ErrInvalidValue, "sqrt needs (")
		}
		v, err := p.primary()
		if err != nil {
			return 0, err
		}
		if v < 0 {
			return 0, errors.WithMessage(fault.ErrInvalidValue, "square root of a negative number")
		}
		return math.Sqrt(v), nil
	case unicode.IsDigit(rune(c)) || c == '.':
		start := p.i
		for p.i < len(p.s) && (unicode.IsDigit(rune(p.s[p.i])) || p.s[p.i] == '.' || p.s[p.i] == ',') {
			p.i++
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(p.s[start:p.i], ",", ""), 64)
		if err != nil {
			return 0, errors.Prefix(errors.WithMessagef(err, "number %q", p.s[start:p.i]), fault.ErrInvalidValue)
		}
		return v, nil
	}
	return 0, errors.WithMessagef(fault.ErrInvalidValue, "unexpected %q", p.s[p.i:])
}

// formatNumber says a number: whole numbers as they are, others rounded to
// a sensible precision, negatives with "minus".
func formatNumber(v float64) string {
	if v < 0 {
		return "minus " + formatNumber(-v)
	}
	if v >= 1e15 || (v > 0 && v < 1e-4) {
		// Said as words a voice can read: "about 1.2 times 10 to the
		// power of minus 5".
		exp := int(math.Floor(math.Log10(v)))
		m := math.Round(v/math.Pow(10, float64(exp))*10) / 10
		if m >= 10 { // 9.96 rounds up to 10
			m, exp = m/10, exp+1
		}
		mantissa := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(m, 'f', 1, 64), "0"), ".")
		power := strconv.Itoa(exp)
		if exp < 0 {
			power = "minus " + strconv.Itoa(-exp)
		}
		return "about " + mantissa + " times 10 to the power of " + power
	}
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	decimals := 2
	if v < 1 {
		decimals = 4
	}
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
