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

func (lx *lexer) errorf(format string, values ...interface{}) stateFn {
	for i, value := range values {
		if v, ok := value.(rune); ok {
			values[i] = escapeSpecial(v)
		}
	}
	lx.items <- item{
		itemError,
		fmt.Sprintf(format, values...),
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
	case commentStart:
		lx.push(lexTop)
		return lexCommentStart
	case keyGroupStart:
		lx.emit(itemKeyGroupStart)
		return lexKeyGroupStart
	case eof:
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(itemEOF)
		return nil
	}

	lx.backup()

	lx.push(lexTopValueEnd)
	return lexKeyStart
}

// lexTopValueEnd is entered whenever a top-level value has been consumed.
// It must see only whitespace, and will turn back to lexTop upon a new line.
// If it sees EOF, it will quit the lexer successfully.
func lexTopValueEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == commentStart:
		// a comment will read to a new line for us.
		lx.push(lexTop)
		return lexCommentStart
	case isWhitespace(r):
		return lexTopValueEnd
	case isNL(r):
		lx.ignore()
		return lexTop
	case r == eof:
		lx.ignore()
		return lexTop
	}
	return lx.errorf("Expected a top-level value to end with a new line, "+
		"comment or EOF, but got '%s' instead.", r)
}

// lexKeyGroup lexes the beginning of a key group. Namely, it makes sure that
// it starts with a character other than '.' and ']'.
// It assumes that '[' has already been consumed.
func lexKeyGroupStart(lx *lexer) stateFn {
	switch lx.next() {
	case keyGroupEnd:
		return lx.errorf("Unexpected end of key group. (Key groups cannot " +
			"be empty.)")
	case keyGroupSep:
		return lx.errorf("Unexpected key group separator. (Key groups cannot " +
			"be empty.)")
	}
	return lexKeyGroup
}

// lexKeyGroup lexes the name of a key group. It assumes that at least one
// valid character for the key group has already been read.
func lexKeyGroup(lx *lexer) stateFn {
	switch lx.peek() {
	case keyGroupEnd:
		lx.emit(itemText)
		lx.next()
		lx.emit(itemKeyGroupEnd)
		return lexTop
	case keyGroupSep:
		lx.emit(itemText)
		lx.next()
		lx.ignore()
		return lexKeyGroupStart
	}

	lx.next()
	return lexKeyGroup
}

func lexKeyStart(lx *lexer) stateFn {
	r := lx.peek()
	switch {
	case r == keySep:
		return lx.errorf("Unexpected key separator '%s'.", keySep)
	case isWhitespace(r) || isNL(r):
		return lx.errorf("Unexpected whitespace '%s' at start of key.", r)
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
	return lx.errorf("Expected key separator '%s', but got '%s' instead.",
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
	case r == 't':
		return lexTrue
	case r == 'f':
		return lexFalse
	case r == '-':
		return lexNumberStart
	case isDigit(r):
		lx.backup() // avoid an extra state and use the same as above
		return lexNumberStart
	case r == '.': // special error case, be kind to users
		return lx.errorf("Floats must start with a digit, not '.'.")
	}
	return lx.errorf("Expected value but found '%s' instead.", r)
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
		lx.ignore()
		return lx.pop()
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
	return lx.errorf("Invalid escape character '%s'. Only the following "+
		"escape characters are allowed: \\0, \\t, \\n, \\r, \\\", \\\\.", r)
}

// lexNumberStart consumes either an integer or a float. It assumes that a
// negative sign has already been read, but that *no* digits have been consumed.
// lexNumberStart will move to the appropriate integer or float states.
func lexNumberStart(lx *lexer) stateFn {
	// we MUST see a digit. Even floats have to start with a digit.
	r := lx.next()
	if !isDigit(r) {
		if r == '.' {
			return lx.errorf("Floats must start with a digit, not '.'.")
		} else {
			return lx.errorf("Expected a digit but got '%s'.", r)
		}
	}
	return lexNumber
}

// lexNumber consumes an integer or a float after seeing the first digit.
func lexNumber(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isDigit(r):
		return lexNumber
	case r == '.':
		return lexFloatStart
	case isWhitespace(r) || isNL(r):
		lx.backup()
		lx.emit(itemInteger)
		return lx.pop()
	}
	return lx.errorf("Expected a digit, '.' or the end of a value, but got "+
		"'%s' instead.", r)
}

// lexFloatStart starts the consumption of digits of a float after a '.'.
// Namely, at least one digit is required.
func lexFloatStart(lx *lexer) stateFn {
	r := lx.next()
	if !isDigit(r) {
		return lx.errorf("Floats must have a digit after the '.', but got "+
			"'%s' instead.", r)
	}
	return lexFloat
}

// lexFloat consumes the digits of a float after a '.'.
// Assumes that one digit has been consumed after a '.' already.
func lexFloat(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isDigit(r):
		return lexFloat
	case isWhitespace(r) || isNL(r):
		lx.backup()
		lx.emit(itemFloat)
		return lx.pop()
	}
	return lx.errorf("Expected a digit or the end of a value, but got "+
		"'%s' instead.", r)
}

// lexTrue consumes the "rue" in "true". It assumes that 't' has already
// been consumed.
func lexTrue(lx *lexer) stateFn {
	if r := lx.next(); r != 'r' {
		return lx.errorf("Expected 'tr', but found 't%s' instead.", r)
	}
	if r := lx.next(); r != 'u' {
		return lx.errorf("Expected 'tru', but found 'tr%s' instead.", r)
	}
	if r := lx.next(); r != 'e' {
		return lx.errorf("Expected 'true', but found 'tru%s' instead.", r)
	}
	lx.emit(itemBool)
	return lx.pop()
}

// lexFalse consumes the "alse" in "false". It assumes that 'f' has already
// been consumed.
func lexFalse(lx *lexer) stateFn {
	if r := lx.next(); r != 'a' {
		return lx.errorf("Expected 'fa', but found 'f%s' instead.", r)
	}
	if r := lx.next(); r != 'l' {
		return lx.errorf("Expected 'fal', but found 'fa%s' instead.", r)
	}
	if r := lx.next(); r != 's' {
		return lx.errorf("Expected 'fals', but found 'fal%s' instead.", r)
	}
	if r := lx.next(); r != 'e' {
		return lx.errorf("Expected 'false', but found 'fals%s' instead.", r)
	}
	lx.emit(itemBool)
	return lx.pop()
}

// lexCommentStart begins the lexing of a comment. It will emit
// itemCommentStart and consume no characters, passing control to lexComment.
func lexCommentStart(lx *lexer) stateFn {
	lx.ignore()
	lx.emit(itemCommentStart)
	return lexComment
}

// lexComment lexes an entire comment. It assumes that '#' has been consumed.
// It will consume *up to* the first new line character, and pass control
// back to the last state on the stack.
func lexComment(lx *lexer) stateFn {
	r := lx.peek()
	if isNL(r) || r == eof {
		lx.emit(itemText)
		return lx.pop()
	}
	lx.next()
	return lexComment
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

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
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

func escapeSpecial(c rune) string {
	switch c {
	case '\n':
		return "\\n"
	}
	return string(c)
}
