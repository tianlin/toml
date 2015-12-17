package toml

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type parser struct {
	mapping map[string]interface{}
	types   map[string]tomlType
	lx      *lexer

	// A list of keys in the order that they appear in the TOML data.
	ordered []Key

	// the full key for the current hash in scope
	context Key

	// the base key name for everything except hashes
	currentKey string

	// rough approximation of line number
	approxLine int

	// A map of 'key.group.names' to whether they were created implicitly.
	implicits map[string]bool
}

type parseError string

func (pe parseError) Error() string {
	return string(pe)
}

func parse(data string) (p *parser, err error) {
	defer func() {
		if r := recover(); r != nil {
			var ok bool
			if err, ok = r.(parseError); ok {
				return
			}
			panic(r)
		}
	}()

	p = &parser{
		mapping:   make(map[string]interface{}),
		types:     make(map[string]tomlType),
		lx:        lex(data),
		ordered:   make([]Key, 0),
		implicits: make(map[string]bool),
	}
	for {
		item := p.next()
		if item.typ == itemEOF {
			break
		}
		p.topLevel(item)
	}

	return p, nil
}

func (p *parser) panic(format string, v ...interface{}) {
	msg := fmt.Sprintf("Near line %d, key '%s': %s",
		p.approxLine, p.current(), fmt.Sprintf(format, v...))
	panic(parseError(msg))
}

func (p *parser) next() item {
	it := p.lx.nextItem()
	if it.typ == itemError {
		p.panic("Near line %d: %s", it.line, it.val)
	}
	return it
}

func (p *parser) bug(format string, v ...interface{}) {
	log.Fatalf("BUG: %s\n\n", fmt.Sprintf(format, v...))
}

func (p *parser) expect(typ itemType) item {
	it := p.next()
	p.assertEqual(typ, it.typ)
	return it
}

func (p *parser) assertEqual(expected, got itemType) {
	if expected != got {
		p.bug("Expected '%s' but got '%s'.", expected, got)
	}
}

func (p *parser) topLevel(item item) {
	switch item.typ {
	case itemCommentStart:
		p.approxLine = item.line
		p.expect(itemText)
	case itemKeyGroupStart:
		kg := p.expect(itemText)
		p.approxLine = kg.line

		key := make(Key, 0)
		for ; kg.typ == itemText; kg = p.next() {
			key = append(key, kg.val)
		}
		p.assertEqual(itemKeyGroupEnd, kg.typ)

		p.establishContext(key)
		p.setType("", tomlHash)
		p.ordered = append(p.ordered, key)
	case itemKeyStart:
		kname := p.expect(itemText)
		p.currentKey = kname.val
		p.approxLine = kname.line

		val, typ := p.value(p.next())
		p.setValue(p.currentKey, val)
		p.setType(p.currentKey, typ)
		p.ordered = append(p.ordered, p.context.add(p.currentKey))

		p.currentKey = ""
	default:
		p.bug("Unexpected type at top level: %s", item.typ)
	}
}

func (p *parser) replaceEscapes(str string) string {
	var replaced []rune
	s := []byte(str)
	r := 0
	for r < len(s) {
		if s[r] != '\\' {
			c, size := utf8.DecodeRune(s[r:])
			r += size
			replaced = append(replaced, c)
			continue
		}
		r += 1
		if r >= len(s) {
			p.bug("Escape sequence at end of string.")
			return ""
		}
		switch s[r] {
		default:
			p.bug("Expected valid escape code after \\, but got %q.", s[r])
			return ""
		case 'b':
			replaced = append(replaced, rune(0x0008))
			r += 1
		case 't':
			replaced = append(replaced, rune(0x0009))
			r += 1
		case 'n':
			replaced = append(replaced, rune(0x000A))
			r += 1
		case 'f':
			replaced = append(replaced, rune(0x000C))
			r += 1
		case 'r':
			replaced = append(replaced, rune(0x000D))
			r += 1
		case '"':
			replaced = append(replaced, rune(0x0022))
			r += 1
		case '\\':
			replaced = append(replaced, rune(0x005C))
			r += 1
		case 'u':
			// At this point, we know we have a Unicode escape of the form
			// `uXXXX` at [r, r+5). (Because the lexer guarantees this
			// for us.)
			escaped := p.asciiEscapeToUnicode(s[r+1 : r+5])
			replaced = append(replaced, escaped)
			r += 5
		case 'U':
			// At this point, we know we have a Unicode escape of the form
			// `uXXXX` at [r, r+9). (Because the lexer guarantees this
			// for us.)
			escaped := p.asciiEscapeToUnicode(s[r+1 : r+9])
			replaced = append(replaced, escaped)
			r += 9
		}
	}
	return string(replaced)
}

// value translates an expected value from the lexer into a Go value wrapped
// as an empty interface.
func (p *parser) value(it item) (interface{}, tomlType) {
	switch it.typ {
	case itemString:
		return p.replaceEscapes(it.val), p.typeOfPrimitive(it)
	case itemRawString:
		return it.val, p.typeOfPrimitive(it)
	case itemBool:
		switch it.val {
		case "true":
			return true, p.typeOfPrimitive(it)
		case "false":
			return false, p.typeOfPrimitive(it)
		}
		p.bug("Expected boolean value, but got '%s'.", it.val)
	case itemInteger:
		num, err := strconv.ParseInt(it.val, 10, 64)
		if err != nil {
			if e, ok := err.(*strconv.NumError); ok &&
				e.Err == strconv.ErrRange {

				p.panic("Integer '%s' is out of the range of 64-bit "+
					"signed integers.", it.val)
			} else {
				p.bug("Expected integer value, but got '%s'.", it.val)
			}
		}
		return num, p.typeOfPrimitive(it)
	case itemFloat:
		num, err := strconv.ParseFloat(it.val, 64)
		if err != nil {
			if e, ok := err.(*strconv.NumError); ok &&
				e.Err == strconv.ErrRange {

				p.panic("Float '%s' is out of the range of 64-bit "+
					"IEEE-754 floating-point numbers.", it.val)
			} else {
				p.bug("Expected float value, but got '%s'.", it.val)
			}
		}
		return num, p.typeOfPrimitive(it)
	case itemDatetime:
		t, err := time.Parse("2006-01-02T15:04:05Z", it.val)
		if err != nil {
			p.bug("Expected Zulu formatted DateTime, but got '%s'.", it.val)
		}
		return t, p.typeOfPrimitive(it)
	case itemArray:
		array := make([]interface{}, 0)
		types := make([]tomlType, 0)

		for it = p.next(); it.typ != itemArrayEnd; it = p.next() {
			if it.typ == itemCommentStart {
				p.expect(itemText)
				continue
			}

			val, typ := p.value(it)
			array = append(array, val)
			types = append(types, typ)
		}
		return array, p.typeOfArray(types)
	}
	p.bug("Unexpected value type: %s", it.typ)
	panic("unreachable")
}

// establishContext sets the current context of the parser, where the context
// is the hash currently in scope.
//
// Establishing the context also makes sure that the key isn't a duplicate, and
// will create implicit hashes automatically.
func (p *parser) establishContext(key Key) {
	var ok bool

	// Always start at the top level and drill down for our context.
	hashContext := p.mapping
	keyContext := make(Key, 0)

	// We only need implicit hashes for key[0:-1]
	for _, k := range key[0 : len(key)-1] {
		_, ok = hashContext[k]
		keyContext = append(keyContext, k)

		// No key? Make an implicit hash and move on.
		if !ok {
			p.addImplicit(keyContext)
			hashContext[k] = make(map[string]interface{})
		}

		// It better be a hash, since this MUST be a key group (by virtue of
		// it not being the last element in a key).
		if hashContext, ok = hashContext[k].(map[string]interface{}); !ok {
			p.panic("Key '%s' was already created as a hash.", keyContext)
		}
	}

	p.context = keyContext
	p.setValue(key[len(key)-1], make(map[string]interface{}))
	p.context = append(p.context, key[len(key)-1])
}

// setValue sets the given key to the given value in the current context.
// It will make sure that the key hasn't already been defined, account for
// implicit key groups.
func (p *parser) setValue(key string, value interface{}) {
	var tmpHash interface{}
	var ok bool

	hash := p.mapping
	keyContext := make(Key, 0)
	for _, k := range p.context {
		keyContext = append(keyContext, k)
		if tmpHash, ok = hash[k]; !ok {
			p.bug("Context for key '%s' has not been established.", keyContext)
		}
		if hash, ok = tmpHash.(map[string]interface{}); !ok {
			p.bug("Expected hash to have type 'map[string]interface{}', but "+
				"it has '%T' instead.", tmpHash)
		}
	}
	keyContext = append(keyContext, key)

	if _, ok := hash[key]; ok {
		// We need to do some fancy footwork here. If `hash[key]` was implcitly
		// created AND `value` is a hash, then let this go through and stop
		// tagging this keygroup as implicit.
		if p.isImplicit(keyContext) {
			p.removeImplicit(keyContext)
			return
		}

		// Otherwise, we have a concrete key trying to override a previous
		// key, which is *always* wrong.
		p.panic("Key '%s' has already been defined.", keyContext)
	}
	hash[key] = value
}

// setType sets the type of a particular value at a given key.
// It should be called immediately AFTER setValue.
func (p *parser) setType(key string, typ tomlType) {
	keyContext := make(Key, 0, len(p.context)+1)
	for _, k := range p.context {
		keyContext = append(keyContext, k)
	}
	if len(key) > 0 { // allow type setting for hashes
		keyContext = append(keyContext, key)
	}

	fullkey := keyContext.String()
	if _, ok := p.types[fullkey]; ok {
		p.bug("Type for key '%s' has already been set, but it wasn't "+
			"detected as a duplicate in setValue.", fullkey)
	}
	p.types[fullkey] = typ
}

// addImplicit sets the given Key as having been created implicitly.
func (p *parser) addImplicit(key Key) {
	p.implicits[key.String()] = true
}

// removeImplicit stops tagging the given key as having been implicitly created.
func (p *parser) removeImplicit(key Key) {
	p.implicits[key.String()] = false
}

// isImplicit returns true if the key group pointed to by the key was created
// implicitly.
func (p *parser) isImplicit(key Key) bool {
	return p.implicits[key.String()]
}

// current returns the full key name of the current context.
func (p *parser) current() string {
	if len(p.currentKey) == 0 {
		return p.context.String()
	}
	if len(p.context) == 0 {
		return p.currentKey
	}
	return fmt.Sprintf("%s.%s", p.context, p.currentKey)
}

func (p *parser) panicf(format string, v ...interface{}) {
	msg := fmt.Sprintf("Near line %d (last key parsed '%s'): %s",
		p.approxLine, p.current(), fmt.Sprintf(format, v...))
	panic(parseError(msg))
}

func (p *parser) asciiEscapeToUnicode(bs []byte) rune {
	s := string(bs)
	hex, err := strconv.ParseUint(strings.ToLower(s), 16, 32)
	if err != nil {
		p.bug("Could not parse '%s' as a hexadecimal number, but the "+
			"lexer claims it's OK: %s", s, err)
	}

	// BUG(burntsushi)
	// I honestly don't understand how this works. I can't seem
	// to find a way to make this fail. I figured this would fail on invalid
	// UTF-8 characters like U+DCFF, but it doesn't.
	if !utf8.ValidString(string(rune(hex))) {
		p.panicf("Escaped character '\\u%s' is not valid UTF-8.", s)
	}
	return rune(hex)
}
