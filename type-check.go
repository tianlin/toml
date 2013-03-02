package toml

import (
	"fmt"
	"strings"
)

// tomlType represents any Go type that corresponds to a TOML type.
// While the first draft of the TOML spec has a simplistic type system that
// probably doesn't need this level of sophistication, we seem to be militating
// toward adding real composite types.
type tomlType interface {
	name() string
	components() []tomlType
	polymorphic() bool
	String() string
}

func typeEqual(t1, t2 tomlType) bool {
	if t1.polymorphic() || t2.polymorphic() {
		return true
	}
	if t1.name() != t2.name() {
		return false
	}

	cs1, cs2 := t1.components(), t2.components()
	if len(cs1) != len(cs2) {
		return false
	}
	for i := 0; i < len(cs1); i++ {
		if !typeEqual(cs1[i], cs2[i]) {
			return false
		}
	}
	return true
}

type tomlBaseType string

var (
	tomlInteger  tomlBaseType = "Integer"
	tomlFloat    tomlBaseType = "Float"
	tomlDatetime tomlBaseType = "Datetime"
	tomlString   tomlBaseType = "String"
	tomlBool     tomlBaseType = "Bool"
)

func (btype tomlBaseType) name() string {
	return string(btype)
}

func (btype tomlBaseType) components() []tomlType {
	return nil
}

func (ptype tomlBaseType) polymorphic() bool {
	return false
}

func (btype tomlBaseType) String() string {
	return btype.name()
}

type tomlPolymorphicType struct{}

var tomlPolymorphic tomlPolymorphicType = struct{}{}

func (ptype tomlPolymorphicType) name() string {
	return "a"
}

func (ptype tomlPolymorphicType) components() []tomlType {
	return nil
}

func (ptype tomlPolymorphicType) polymorphic() bool {
	return true
}

func (ptype tomlPolymorphicType) String() string {
	return ptype.name()
}

type tomlArrayType struct {
	of tomlType
}

func (atype tomlArrayType) name() string {
	return "Array"
}

func (atype tomlArrayType) components() []tomlType {
	return []tomlType{atype.of}
}

func (ptype tomlArrayType) polymorphic() bool {
	return false
}

func (atype tomlArrayType) String() string {
	return fmt.Sprintf("[%s]", atype.of.String())
}

type tomlTupleType struct {
	of []tomlType
}

func (ttype tomlTupleType) name() string {
	return "Tuple"
}

func (ttype tomlTupleType) components() []tomlType {
	return ttype.of
}

func (ptype tomlTupleType) polymorphic() bool {
	return false
}

func (ttype tomlTupleType) String() string {
	componentTypes := make([]string, len(ttype.of))
	for i, ctype := range ttype.of {
		componentTypes[i] = ctype.String()
	}
	return fmt.Sprintf("(%s)", strings.Join(componentTypes, ", "))
}

// typeOfPrimitive returns a tomlType of any primitive value in TOML.
// Primitive values are: Integer, Float, Datetime, String and Bool.
//
// Passing a lexer item other than the following will cause a BUG message
// to occur: itemString, itemBool, itemInteger, itemFloat, itemDatetime.
func (p *parser) typeOfPrimitive(lexItem item) tomlType {
	switch lexItem.typ {
	case itemInteger:
		return tomlInteger
	case itemFloat:
		return tomlFloat
	case itemDatetime:
		return tomlDatetime
	case itemString:
		return tomlString
	case itemBool:
		return tomlBool
	}
	p.bug("Cannot infer primitive type of lex item '%s'.", lexItem)
	panic("unreachable")
}

// typeOfArray returns a tomlType for an array given a list of types of its
// values.
func (p *parser) typeOfArray(types []tomlType) tomlType {
	// Empty arrays are polymorphic!
	if len(types) == 0 {
		return tomlArrayType{tomlPolymorphic}
	}

	theType := types[0]
	for _, t := range types[1:] {
		if !typeEqual(theType, t) {
			p.panic("Array contains values of type '%s' and '%s', but arrays "+
				"must be homogeneous.", theType, t)
		}
	}
	return tomlArrayType{theType}
}

// typeOfTuple returns a tomlType for a tuple given a list of types of its
// values. Any combination of types is valid.
func (p *parser) typeOfTuple(types []tomlType) tomlType {
	return tomlTupleType{types}
}
