package cmd

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"text/scanner"
	"unicode/utf8"
)

// maxMathBits bounds every intermediate numerator and denominator. The final
// answer is capped at 4096 characters, roughly 13,600 bits, so larger values
// can never be printed anyway.
const maxMathBits = 16384

var errMathTooLarge = fmt.Errorf("result is too large")

func mathSize(value *big.Rat) int {
	return max(value.Num().BitLen(), value.Denom().BitLen())
}

// checkMathOperands rejects a binary operation whose result could exceed the
// bit budget. Sums, products and quotients of a/b and c/d need at most
// size(a/b)+size(c/d)+1 bits.
func checkMathOperands(left, right *big.Rat) error {
	if mathSize(left)+mathSize(right)+1 > maxMathBits {
		return errMathTooLarge
	}
	return nil
}

var superscriptDigits = map[rune]rune{
	'⁰': '0', '¹': '1', '²': '2', '³': '3', '⁴': '4',
	'⁵': '5', '⁶': '6', '⁷': '7', '⁸': '8', '⁹': '9',
}

func Calculate(expression string) (string, error) {
	expression = normalizeMathExpression(strings.TrimSpace(expression))
	if expression == "" {
		return "", fmt.Errorf("expression is required")
	}
	if utf8.RuneCountInString(expression) > 200 {
		return "", fmt.Errorf("expression is too long")
	}
	parser := newMathParser(expression)
	result, err := parser.parseExpression()
	if err != nil {
		return "", err
	}
	if parser.token != scanner.EOF {
		return "", fmt.Errorf("unexpected %q", parser.literal)
	}
	answer := result.RatString()
	if len(answer) > 4096 {
		return "", errMathTooLarge
	}
	return answer, nil
}

func normalizeMathExpression(expression string) string {
	expression = strings.NewReplacer("×", "*", "÷", "/", "−", "-", "–", "-", "—", "-").Replace(expression)
	runes := []rune(expression)
	if len(runes) > 1 {
		if exponent, ok := superscriptDigits[runes[0]]; ok {
			end := 1
			exponents := []rune{exponent}
			for end < len(runes) {
				digit, found := superscriptDigits[runes[end]]
				if !found {
					break
				}
				exponents = append(exponents, digit)
				end++
			}
			baseEnd := end
			for baseEnd < len(runes) && strings.IndexRune("0123456789", runes[baseEnd]) >= 0 {
				baseEnd++
			}
			if baseEnd > end && baseEnd == len(runes) {
				return string(runes[end:baseEnd]) + "^" + string(exponents)
			}
		}
	}
	var normalized strings.Builder
	for index := 0; index < len(runes); index++ {
		digit, superscript := superscriptDigits[runes[index]]
		if !superscript {
			normalized.WriteRune(runes[index])
			continue
		}
		normalized.WriteByte('^')
		normalized.WriteRune(digit)
		for index+1 < len(runes) {
			digit, superscript = superscriptDigits[runes[index+1]]
			if !superscript {
				break
			}
			index++
			normalized.WriteRune(digit)
		}
	}
	return normalized.String()
}

type mathParser struct {
	scanner scanner.Scanner
	token   rune
	literal string
}

func newMathParser(expression string) *mathParser {
	parser := &mathParser{}
	parser.scanner.Init(strings.NewReader(expression))
	parser.scanner.Mode = scanner.ScanInts | scanner.ScanFloats
	parser.next()
	return parser
}

func (p *mathParser) next() {
	p.token = p.scanner.Scan()
	p.literal = p.scanner.TokenText()
}

func (p *mathParser) parseExpression() (*big.Rat, error) {
	left, err := p.parseTerm()
	for err == nil && (p.token == '+' || p.token == '-') {
		operator := p.token
		p.next()
		var right *big.Rat
		right, err = p.parseTerm()
		if err == nil {
			err = checkMathOperands(left, right)
		}
		if err == nil && operator == '+' {
			left.Add(left, right)
		} else if err == nil {
			left.Sub(left, right)
		}
	}
	return left, err
}

func (p *mathParser) parseTerm() (*big.Rat, error) {
	left, err := p.parseUnary()
	for err == nil && (p.token == '*' || p.token == '/' || p.token == '%') {
		operator := p.token
		p.next()
		var right *big.Rat
		right, err = p.parseUnary()
		if err == nil {
			err = checkMathOperands(left, right)
		}
		if err != nil {
			break
		}
		switch operator {
		case '*':
			left.Mul(left, right)
		case '/':
			if right.Sign() == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			left.Quo(left, right)
		case '%':
			if !left.IsInt() || !right.IsInt() || right.Sign() == 0 {
				return nil, fmt.Errorf("modulo requires non-zero integers")
			}
			left.SetInt(new(big.Int).Rem(left.Num(), right.Num()))
		}
	}
	return left, err
}

func (p *mathParser) parseUnary() (*big.Rat, error) {
	if p.token == '+' || p.token == '-' {
		operator := p.token
		p.next()
		value, err := p.parseUnary()
		if err == nil && operator == '-' {
			value.Neg(value)
		}
		return value, err
	}
	return p.parsePower()
}

func (p *mathParser) parsePower() (*big.Rat, error) {
	base, err := p.parsePrimary()
	if err != nil || p.token != '^' {
		return base, err
	}
	p.next()
	exponent, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	return raiseMathPower(base, exponent)
}

func (p *mathParser) parsePrimary() (*big.Rat, error) {
	if p.token == scanner.Int || p.token == scanner.Float {
		literal := p.literal
		p.next()
		if err := checkMathLiteral(literal); err != nil {
			return nil, err
		}
		value, ok := new(big.Rat).SetString(literal)
		if !ok {
			return nil, fmt.Errorf("invalid number %q", literal)
		}
		return value, nil
	}
	if p.token == '(' {
		p.next()
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if p.token != ')' {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.next()
		return value, nil
	}
	return nil, fmt.Errorf("expected number, got %q", p.literal)
}

func raiseMathPower(base, exponent *big.Rat) (*big.Rat, error) {
	if !exponent.IsInt() || !exponent.Num().IsInt64() {
		return nil, fmt.Errorf("exponent must be an integer")
	}
	power := exponent.Num().Int64()
	if power < -1000 || power > 1000 {
		return nil, fmt.Errorf("exponent must be between -1000 and 1000")
	}
	if power < 0 {
		if base.Sign() == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		base = new(big.Rat).Inv(base)
		power = -power
	}
	if int64(mathSize(base))*power > maxMathBits {
		return nil, errMathTooLarge
	}
	exponentValue := big.NewInt(power)
	numerator := new(big.Int).Exp(new(big.Int).Set(base.Num()), exponentValue, nil)
	denominator := new(big.Int).Exp(new(big.Int).Set(base.Denom()), exponentValue, nil)
	return new(big.Rat).SetFrac(numerator, denominator), nil
}

// checkMathLiteral rejects scientific notation whose exponent would make
// big.Rat.SetString compute a huge power of ten or two.
func checkMathLiteral(literal string) error {
	markers, limit := "eE", 4096
	if strings.HasPrefix(literal, "0x") || strings.HasPrefix(literal, "0X") {
		markers, limit = "pP", maxMathBits
	}
	index := strings.IndexAny(literal, markers)
	if index < 0 {
		return nil
	}
	exponent, err := strconv.Atoi(literal[index+1:])
	if err != nil || exponent < -limit || exponent > limit {
		return errMathTooLarge
	}
	return nil
}
