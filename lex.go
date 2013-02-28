package toml

import (
	"fmt"
	"unicode/utf8"
)

type itemType int

const (
	itemError itemType = iota
	itemNIL
	itemEOF
	itemText
	itemString
	itemBool
	itemInteger
	itemFloat
	itemArray // used internally to the lexer
	itemDatetime
	itemKeyGroupStart
	itemKeyGroupEnd
	itemKeyStart
	itemArrayStart
	itemArrayEnd
	itemCommentStart
)

const (
	eof           = 0
	keyGroupStart = '['
	keyGroupEnd   = ']'
	keyGroupSep   = '.'
	keySep        = '='
	arrayStart    = '['
	arrayEnd      = ']'
	arrayValTerm  = ','
	commentStart  = '#'
	stringStart   = '"'
	stringEnd     = '"'
)

type stateFn func(lx *lexer) stateFn

type lexer struct {
	input string
	start int
	pos   int
	width int
	line  int
	state stateFn
	items chan item

	stack []stateFn
}

type item struct {
	typ  itemType
	val  string
	line int
}

func (lx *lexer) nextItem() item {
	for {
		select {
		case item := <-lx.items:
			return item
		default:
			lx.state = lx.state(lx)
		}
	}
	panic("not reached")
}

func lex(input string) *lexer {
	lx := &lexer{
		input: input,
		state: lexTop,
		line:  1,
		items: make(chan item, 10),
		stack: make([]stateFn, 0, 10),
	}
	return lx
}

func (lx *lexer) push(state stateFn) {
	lx.stack = append(lx.stack, state)
}

func (lx *lexer) pop() stateFn {
	if len(lx.stack) == 0 {
		return lx.errorf("BUG in lexer: no states to pop.")
	}
	last := lx.stack[len(lx.stack)-1]
	lx.stack = lx.stack[0 : len(lx.stack)-1]
	return last
}

func (lx *lexer) emit(typ itemType) {
	lx.items <- item{typ, lx.input[lx.start:lx.pos], lx.line}
	lx.start = lx.pos
}

func (lx *lexer) next() (r rune) {
	if lx.pos >= len(lx.input) {
		lx.width = 0
		return eof
	}

	if lx.input[lx.pos] == '\n' {
		lx.line++
	}
	r, lx.width = utf8.DecodeRuneInString(lx.input[lx.pos:])
	lx.pos += lx.width
	return r
}

// ignore skips over the pending input before this point.
func (lx *lexer) ignore() {
	lx.start = lx.pos
}

// backup steps back one rune. Can be called only once per call of next.
func (lx *lexer) backup() {
	lx.pos -= lx.width
	if lx.pos < len(lx.input) && lx.input[lx.pos] == '\n' {
		lx.line--
	}
}

// accept consumes the next rune if it's equal to `valid`.
func (lx *lexer) accept(valid rune) bool {
	if lx.next() == valid {
		return true
	}
	lx.backup()
	return false
}

// peek returns but does not consume the next rune in the input.
func (lx *lexer) peek() rune {
	r := lx.next()
	lx.backup()
	return r
}

func (lx *lexer) errorf(format string, v ...interface{}) stateFn {
	lx.items <- item{
		itemError,
		fmt.Sprintf(format, v...),
		lx.line,
	}
	return nil
}

func lexTop(lx *lexer) stateFn {
	r := lx.next()
	if isWhitespace(r) || isNL(r) {
		return lexSkip(lx, lexTop)
	}

	switch r {
	case eof:
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(itemEOF)
		return nil
	}

	lx.backup()

	lx.push(lexTop)
	return lexKeyStart
}

func lexKeyStart(lx *lexer) stateFn {
	r := lx.peek()
	switch {
	case r == keySep:
		return lx.errorf("Unexpected key separator '%c'.", keySep)
	case isWhitespace(r) || isNL(r):
		return lx.errorf("Unexpected whitespace '%c' at start of key.", r)
	}

	lx.emit(itemKeyStart)
	lx.next()
	return lexKey
}

func lexKey(lx *lexer) stateFn {
	r := lx.peek()

	// XXX: Possible divergence from spec?
	// "Keys start with the first non-whitespace character and end with the
	// last non-whitespace character before the equals sign."
	// Note here that whitespace is either a tab or a space.
	// But we'll call it quits if we see a new line too.
	if isWhitespace(r) || isNL(r) {
		lx.emit(itemText)
		return lexKeyEnd
	}

	lx.next()
	return lexKey
}

func lexKeyEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isWhitespace(r) || isNL(r):
		return lexSkip(lx, lexKeyEnd)
	case r == keySep:
		return lexValue
	}
	return lx.errorf("Expected key separator '%c', but got '%c' instead.",
		keySep, r)
}

func lexValue(lx *lexer) stateFn {
	// We allow whitespace to precede a value, but NOT new lines.
	// In array syntax, the array states are responsible for ignoring new lines.
	r := lx.next()
	if isWhitespace(r) {
		return lexSkip(lx, lexValue)
	}

	switch {
	case r == stringStart:
		lx.ignore() // ignore the '"'
		return lexString
	}
	return lx.errorf("Expected value but found '%c' instead.", r)
}

// lexString consumes the inner contents of a string. It assumes that the
// beginning '"' has already been consumed and ignored.
func lexString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isNL(r):
		return lx.errorf("Strings cannot contain new lines.")
	case r == '\\':
		return lexStringEscape
	case r == stringEnd:
		lx.backup()
		lx.emit(itemString)
		lx.next()
		return lexSkip(lx, lx.pop())
	}
	return lexString
}

// lexStringEscape consumes an escaped character. It assumes that the preceding
// '\\' has already been consumed.
func lexStringEscape(lx *lexer) stateFn {
	r := lx.next()
	switch r {
	case '0':
		fallthrough
	case 't':
		fallthrough
	case 'n':
		fallthrough
	case 'r':
		fallthrough
	case '"':
		fallthrough
	case '\\':
		return lexString
	}
	return lx.errorf("Invalid escape character '%c'. Only the following "+
		"escape characters are allowed: \\0, \\t, \\n, \\r, \\\", \\\\.", r)
}

// lexSkip ignores all slurped input and moves on to the next state.
func lexSkip(lx *lexer, nextState stateFn) stateFn {
	return func(lx *lexer) stateFn {
		lx.ignore()
		return nextState
	}
}

// isWhitespace returns true if `r` is a whitespace character according
// to the spec.
func isWhitespace(r rune) bool {
	return r == '\t' || r == ' '
}

func isNL(r rune) bool {
	return r == '\n' || r == '\r'
}

func (itype itemType) String() string {
	switch itype {
	case itemError:
		return "Error"
	case itemEOF:
		return "EOF"
	case itemText:
		return "Text"
	case itemString:
		return "String"
	case itemBool:
		return "Bool"
	case itemInteger:
		return "Integer"
	case itemFloat:
		return "Float"
	case itemDatetime:
		return "DateTime"
	case itemKeyGroupStart:
		return "KeyGroupStart"
	case itemKeyGroupEnd:
		return "KeyGroupEnd"
	case itemKeyStart:
		return "KeyStart"
	case itemArrayStart:
		return "Array"
	case itemArrayEnd:
		return "ArrayEnd"
	case itemCommentStart:
		return "CommentStart"
	}
	panic(fmt.Sprintf("BUG: Unknown type '%s'.", itype))
}

func (item item) String() string {
	return fmt.Sprintf("(%s, %s)", item.typ.String(), item.val)
}
