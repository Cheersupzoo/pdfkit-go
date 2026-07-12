package svgpath

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Command is a single SVG path command after parsing.
type Command struct {
	Op   byte // uppercase absolute op
	Args []float64
}

// Parse parses an SVG path data string into absolute commands.
func Parse(d string) ([]Command, error) {
	tokens := tokenize(d)
	var cmds []Command
	var cx, cy, startX, startY, lastCX, lastCY float64
	var lastOp byte
	i := 0
	readNums := func(n int) ([]float64, error) {
		if i+n > len(tokens) {
			return nil, fmt.Errorf("svgpath: expected %d numbers", n)
		}
		out := make([]float64, n)
		for j := 0; j < n; j++ {
			v, err := strconv.ParseFloat(tokens[i+j], 64)
			if err != nil {
				return nil, err
			}
			out[j] = v
		}
		i += n
		return out, nil
	}
	for i < len(tokens) {
		tok := tokens[i]
		if len(tok) == 1 && isCmd(tok[0]) {
			i++
			op := tok[0]
			rel := unicode.IsLower(rune(op))
			op = byte(unicode.ToUpper(rune(op)))
			for {
				var args []float64
				var err error
				switch op {
				case 'M':
					args, err = readNums(2)
					if err != nil {
						return nil, err
					}
					x, y := args[0], args[1]
					if rel {
						x += cx
						y += cy
					}
					cx, cy = x, y
					startX, startY = x, y
					cmds = append(cmds, Command{Op: 'M', Args: []float64{x, y}})
					op = 'L' // subsequent pairs are implicit LineTos
				case 'L':
					args, err = readNums(2)
					if err != nil {
						return nil, err
					}
					x, y := args[0], args[1]
					if rel {
						x += cx
						y += cy
					}
					cx, cy = x, y
					cmds = append(cmds, Command{Op: 'L', Args: []float64{x, y}})
				case 'H':
					args, err = readNums(1)
					if err != nil {
						return nil, err
					}
					x := args[0]
					if rel {
						x += cx
					}
					cx = x
					cmds = append(cmds, Command{Op: 'L', Args: []float64{cx, cy}})
				case 'V':
					args, err = readNums(1)
					if err != nil {
						return nil, err
					}
					y := args[0]
					if rel {
						y += cy
					}
					cy = y
					cmds = append(cmds, Command{Op: 'L', Args: []float64{cx, cy}})
				case 'C':
					args, err = readNums(6)
					if err != nil {
						return nil, err
					}
					if rel {
						args[0] += cx
						args[1] += cy
						args[2] += cx
						args[3] += cy
						args[4] += cx
						args[5] += cy
					}
					lastCX, lastCY = args[2], args[3]
					cx, cy = args[4], args[5]
					cmds = append(cmds, Command{Op: 'C', Args: args})
				case 'S':
					args, err = readNums(4)
					if err != nil {
						return nil, err
					}
					if rel {
						args[0] += cx
						args[1] += cy
						args[2] += cx
						args[3] += cy
					}
					var x1, y1 float64
					if lastOp == 'C' || lastOp == 'S' {
						x1 = 2*cx - lastCX
						y1 = 2*cy - lastCY
					} else {
						x1, y1 = cx, cy
					}
					full := []float64{x1, y1, args[0], args[1], args[2], args[3]}
					lastCX, lastCY = args[0], args[1]
					cx, cy = args[2], args[3]
					cmds = append(cmds, Command{Op: 'C', Args: full})
				case 'Q':
					args, err = readNums(4)
					if err != nil {
						return nil, err
					}
					if rel {
						args[0] += cx
						args[1] += cy
						args[2] += cx
						args[3] += cy
					}
					// convert quadratic to cubic
					x0, y0 := cx, cy
					qx, qy := args[0], args[1]
					x, y := args[2], args[3]
					c1x := x0 + 2.0/3.0*(qx-x0)
					c1y := y0 + 2.0/3.0*(qy-y0)
					c2x := x + 2.0/3.0*(qx-x)
					c2y := y + 2.0/3.0*(qy-y)
					lastCX, lastCY = qx, qy
					cx, cy = x, y
					cmds = append(cmds, Command{Op: 'C', Args: []float64{c1x, c1y, c2x, c2y, x, y}})
				case 'T':
					args, err = readNums(2)
					if err != nil {
						return nil, err
					}
					x, y := args[0], args[1]
					if rel {
						x += cx
						y += cy
					}
					var qx, qy float64
					if lastOp == 'Q' || lastOp == 'T' {
						qx = 2*cx - lastCX
						qy = 2*cy - lastCY
					} else {
						qx, qy = cx, cy
					}
					x0, y0 := cx, cy
					c1x := x0 + 2.0/3.0*(qx-x0)
					c1y := y0 + 2.0/3.0*(qy-y0)
					c2x := x + 2.0/3.0*(qx-x)
					c2y := y + 2.0/3.0*(qy-y)
					lastCX, lastCY = qx, qy
					cx, cy = x, y
					cmds = append(cmds, Command{Op: 'C', Args: []float64{c1x, c1y, c2x, c2y, x, y}})
				case 'A':
					args, err = readNums(7)
					if err != nil {
						return nil, err
					}
					// Approximate arc as line for MVP robustness; still valid path.
					x, y := args[5], args[6]
					if rel {
						x += cx
						y += cy
					}
					cx, cy = x, y
					cmds = append(cmds, Command{Op: 'L', Args: []float64{x, y}})
				case 'Z':
					cx, cy = startX, startY
					cmds = append(cmds, Command{Op: 'Z'})
				default:
					return nil, fmt.Errorf("svgpath: unsupported op %c", op)
				}
				lastOp = op
				if op == 'Z' || op == 'M' {
					break
				}
				// continue implicit command if next token is a number
				if i >= len(tokens) || isCmd(tokens[i][0]) && len(tokens[i]) == 1 {
					break
				}
				if _, err := strconv.ParseFloat(tokens[i], 64); err != nil {
					break
				}
				if op == 'M' {
					op = 'L'
				}
			}
		} else {
			return nil, fmt.Errorf("svgpath: expected command, got %q", tok)
		}
	}
	return cmds, nil
}

func isCmd(c byte) bool {
	switch c {
	case 'M', 'm', 'L', 'l', 'H', 'h', 'V', 'v', 'C', 'c', 'S', 's', 'Q', 'q', 'T', 't', 'A', 'a', 'Z', 'z':
		return true
	}
	return false
}

func tokenize(d string) []string {
	var toks []string
	var num strings.Builder
	flush := func() {
		if num.Len() > 0 {
			toks = append(toks, num.String())
			num.Reset()
		}
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if c <= ' ' || c == ',' {
			flush()
			continue
		}
		if isCmd(c) {
			flush()
			toks = append(toks, string(c))
			continue
		}
		if c == '-' || c == '+' {
			// start of number; if mid-number and not exponent, flush first
			if num.Len() > 0 {
				prev := num.String()
				if !strings.HasSuffix(strings.ToLower(prev), "e") {
					flush()
				}
			}
			num.WriteByte(c)
			continue
		}
		if c == '.' && strings.Contains(num.String(), ".") && !strings.ContainsAny(strings.ToLower(num.String()), "e") {
			flush()
			num.WriteByte(c)
			continue
		}
		num.WriteByte(c)
	}
	flush()
	return toks
}
