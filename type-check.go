package toml

import (
	"fmt"
	"strings"
)

// tomlType represents any Go type that corresponds to a TOML type.
// The chief characters of any TOML type are: its base name, its component
// types (if any), a human readable string of the complete type and whether
// the type is polymorphic.
//
// A polymorphic type is a type that can look like any other type. Currently,
// the only way for a polymorphic type to exist in TOML is with an empty array.
type tomlType interface {
	name() string
	components() []tomlType
	polymorphic() bool
	String() string
}

// typeEqual returns true if type t1 is equal to type t2 and false otherwise.
// Two types are equal if one of the types is polymorphic or if all of the
// following criteria are satisfied:
//
//	- The names of the types are equivalent.
//	- Each type has the same number of component types and they are all equal.
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

// tomlBaseType corresponds to any type in TOML that is not polymorphic and
// does not contain any component types.
type tomlBaseType string

var (
	// The basic primitive types in TOML: int, float, datetimes, strings
	// and booleans.
	tomlInteger  tomlBaseType = "Integer"
	tomlFloat    tomlBaseType = "Float"
	tomlDatetime tomlBaseType = "Datetime"
	tomlString   tomlBaseType = "String"
	tomlBool     tomlBaseType = "Bool"

	// Hashes are conceptually composite types, but in TOML, they are treated
	// as opaque types not dependent on the types of its components.
	// (i.e., hashes in TOML are heterogeneous.)
	tomlHash tomlBaseType = "Hash"
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

// tomlPolymorphicType corresponds to any type that is polymorphic. A
// polymorphic type can "look" like any other single type. In TOML, polymorphic
// types manifest when there are empty lists. e.g.,
//
//	data = [[1, 2], [], [3, 4]]
//	nodata = []
//
// where data has type "list of list of integers" and nodata has type "list
// of a".
type tomlPolymorphicType struct{}

// Create a single trivial value.
var tomlPolymorphic tomlPolymorphicType = struct{}{}

// XXX: This is a problem, since not all polymorphic types are equivalent.
// Solving this problem is difficult. We'd need a distinct type variable for
// every distinct polymorphic type.
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

// tomlArrayType corresponds to the type of any TOML array. In particular, the
// type of an array contains one component type: the type of the values the
// array contains.
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

// Haskell syntax ftw.
func (atype tomlArrayType) String() string {
	return fmt.Sprintf("[%s]", atype.of.String())
}

// tomlTupleType corresponds to the type of any TOML tuple. In particular, the
// type of a tuple contains N ordered component types: the type of each value
// at the ith position of the tuple.
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
