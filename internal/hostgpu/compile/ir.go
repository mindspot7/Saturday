// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"fmt"
	"go/constant"
	"reflect"
	"strings"

	"github.com/user-none/erings/core/compute"
)

// The IR is a typed, structured statement tree lowered from the
// type-checked compute source: everything Go leaves implicit is
// resolved, references are by index, and every expression carries its
// type. The emitters print it without interpreting Go.

// Kind is a scalar type or the category of a composite type.
type Kind int

const (
	Bool Kind = iota
	I8
	I16
	I32
	I64
	U8
	U16
	U32
	U64
	F32
	ArrayKind
	StructKind
	ByteBufferKind
	WordBufferKind
)

var kindNames = [...]string{"bool", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "f32", "array", "struct", "bytebuf", "wordbuf"}

func (k Kind) String() string { return kindNames[k] }

// Type is an IR type. Scalars are Scalar values; composites and
// buffers are the other implementations.
type Type interface {
	Kind() Kind
	String() string
}

// Scalar is a scalar type.
type Scalar Kind

func (s Scalar) Kind() Kind     { return Kind(s) }
func (s Scalar) String() string { return Kind(s).String() }

// Width is the scalar's bit width, or 0 for bool.
func (s Scalar) Width() int {
	switch Kind(s) {
	case I8, U8:
		return 8
	case I16, U16:
		return 16
	case I32, U32, F32:
		return 32
	case I64, U64:
		return 64
	}
	return 0
}

// Bytes is the scalar's size in a buffer: its width in bytes, and one
// for bool, the byte Go stores.
func (s Scalar) Bytes() int {
	if Kind(s) == Bool {
		return 1
	}
	return s.Width() / 8
}

// Narrow reports an 8 or 16-bit integer, which the targets compute in
// 32-bit registers.
func (s Scalar) Narrow() bool { return s.Width() == 8 || s.Width() == 16 }

// ArrayType is a fixed-size array.
type ArrayType struct {
	Elem Type
	Len  int
}

func (a ArrayType) Kind() Kind     { return ArrayKind }
func (a ArrayType) String() string { return fmt.Sprintf("[%d]%s", a.Len, a.Elem) }

// StructRef refers to a laid-out struct by its index in Module.Structs.
type StructRef struct {
	Index int
}

func (s StructRef) Kind() Kind     { return StructKind }
func (s StructRef) String() string { return fmt.Sprintf("struct#%d", s.Index) }

// BufferType is an opaque buffer handle: byte-addressed or
// word-addressed, with its size constant (bytes or words), or Dynamic
// for a buffer whose length arrives with the binding, in bytes for a
// byte buffer and in words for a word buffer. Params marks the
// wrapper's params buffer, only ever read, and only through
// LoadStruct.
type BufferType struct {
	Name    string
	Word    bool
	Size    int
	Dynamic bool // slice-backed buffer, its length travels with the binding
	Params  bool
}

func (b BufferType) Kind() Kind {
	if b.Word {
		return WordBufferKind
	}
	return ByteBufferKind
}
func (b BufferType) String() string { return b.Name }

// Field is one laid-out struct field.
type Field struct {
	Name   string
	Type   Type
	Offset int
}

// StructType is a laid-out struct: fields in declaration order with
// the offsets the layout rule assigns.
type StructType struct {
	Name   string
	Fields []Field
	Size   int
	Align  int
}

// Table is a package-level lookup array lowered to constant memory.
type Table struct {
	Name   string
	Type   ArrayType
	Values []constant.Value
}

// ParamKind distinguishes how a function parameter is passed.
type ParamKind int

const (
	ValueParam  ParamKind = iota // by value
	RefParam                     // by reference, from a Go pointer
	BufferParam                  // a buffer handle
)

// Local is a function's local or parameter slot.
type Local struct {
	Name string
	Type Type
	Kind ParamKind // meaningful for parameters
}

// Func is a lowered function. The first NumParams locals are the
// parameters in order.
type Func struct {
	Name      string
	Locals    []Local
	NumParams int
	Results   []Type
	Body      *Block
}

// BufferBinding is one buffer parameter of an entry point and its
// binding index, which is the parameter's position.
type BufferBinding struct {
	Param   int
	Type    BufferType
	Binding int
}

// DynamicBindings is the variable-length bindings of an entry, one
// bit per binding index, for a backend to check the lengths it
// supplies against.
func DynamicBindings(buffers []BufferBinding) uint32 {
	var m uint32
	for _, bb := range buffers {
		if bb.Type.Dynamic {
			m |= 1 << bb.Binding
		}
	}
	return m
}

// Entry is an entry point: a function plus its dispatch shape and the
// synthesized wrapper the emitted shell calls. A span entry ends in two
// int32 coordinates and renders a segment of dots per thread; an index
// entry ends in one uint32 coordinate, the thread's x; a group entry
// ends in three, the thread's index in its group, the group's size,
// and the group's index, and its params row is the group's. The
// workgroup an entry runs in is each backend's choice.
type Entry struct {
	Func    int
	Wrapper int // synthesized wrapper function
	Span    bool
	Group   bool
	Buffers []BufferBinding
	Params  []int // locals holding the struct parameters, in order
	Coords  int   // 1, 2, or 3 trailing coordinates
}

// paramsBuffer is the type of the wrapper's params buffer: a byte
// buffer of no fixed size, read only through LoadStruct.
var paramsBuffer = BufferType{Name: "params", Params: true}

// Module is a lowered program.
type Module struct {
	Structs []StructType
	Tables  []Table
	Funcs   []Func
	Entries []Entry
}

// Stmt is a statement node.
type Stmt interface{ isStmt() }

// Block is a statement list with its own local scope.
type Block struct {
	Stmts []Stmt
}

// VarDecl declares a local with an initializer.
type VarDecl struct {
	Local int
	Init  Expr
}

// Assign stores Values into Targets. Several targets appear only for
// a multi-result call, which is then the single value.
type Assign struct {
	Targets []Expr
	Values  []Expr
}

// ExprStmt evaluates an expression for effect: a call or an operation
// with no result, or the value of a blank assignment.
type ExprStmt struct {
	X Expr
}

// If is a conditional; Else is nil, a Block, or another If.
type If struct {
	Cond Expr
	Then *Block
	Else Stmt
}

// Case is one switch case with constant values.
type Case struct {
	Values []Expr
	Body   *Block
}

// Switch dispatches on an integer or bool tag.
type Switch struct {
	Tag     Expr
	Cases   []Case
	Default *Block
}

// For is a loop. Cond and Post may be nil. A Go loop's init statement
// lowers into the block wrapping the loop.
type For struct {
	Cond Expr
	Post Stmt
	Body *Block
}

// Break leaves the innermost loop.
type Break struct{}

// Continue advances the innermost loop.
type Continue struct{}

// Return returns zero or more values.
type Return struct {
	Values []Expr
}

func (*Block) isStmt()    {}
func (*VarDecl) isStmt()  {}
func (*Assign) isStmt()   {}
func (*ExprStmt) isStmt() {}
func (*If) isStmt()       {}
func (*Switch) isStmt()   {}
func (*For) isStmt()      {}
func (*Break) isStmt()    {}
func (*Continue) isStmt() {}
func (*Return) isStmt()   {}

// Expr is an expression node carrying its type.
type Expr interface {
	Type() Type
}

// Const is a folded constant.
type Const struct {
	T     Type
	Value constant.Value
}

// LocalRef reads or addresses a local or parameter by index. A ref
// parameter denotes the value it refers to.
type LocalRef struct {
	T     Type
	Index int
}

// FieldRef selects a struct field by index.
type FieldRef struct {
	T     Type
	X     Expr
	Field int
}

// IndexRef selects an array element.
type IndexRef struct {
	T     Type
	X     Expr
	Index Expr
}

// TableRef selects a lookup table element.
type TableRef struct {
	T     Type
	Table int
	Index Expr
}

// Zero is the zero value of a composite type.
type Zero struct {
	T Type
}

// Composite builds a struct or array value from positional elements,
// one per field or element.
type Composite struct {
	T     Type
	Elems []Expr
}

// Call calls a module function. A multi-result call has a Tuple type
// and appears only as the single value of a multi-target Assign.
type Call struct {
	T    Type
	Func int
	Args []Expr
}

// OpCall calls an operation of compute.Operations. A buffer operation
// takes the buffer as the first argument.
type OpCall struct {
	T    Type
	Op   *Operation
	Args []Expr
}

// Unary is Neg, Not, or BitComplement.
type Unary struct {
	T  Type
	Op Op
	X  Expr
}

// Binary is an arithmetic, bitwise, shift, comparison, or logical
// operation; the emitter selects the form from the operand type.
type Binary struct {
	T    Type
	Op   Op
	X, Y Expr
}

// Convert is an explicit scalar conversion; its result is canonical.
type Convert struct {
	T Type
	X Expr
}

// Mask re-canonicalizes a narrow value held in a 32-bit register to
// its declared width: sign-extended for signed types, zero-extended
// for unsigned.
type Mask struct {
	T Type
	X Expr
}

// LoadStruct is the struct row at a byte address of a byte buffer.
// Only the synthesized entry wrappers use it, to fetch a row's params.
// It names the row, not a copy of it: an emitter may copy the row into
// a local or bind it by reference and read its fields in place.
type LoadStruct struct {
	T      Type
	Buffer Expr
	Addr   Expr
}

// Tuple is the type of a multi-result call.
type Tuple struct {
	Elems []Type
}

func (t Tuple) Kind() Kind { return StructKind }
func (t Tuple) String() string {
	parts := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		parts[i] = e.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func (e *Const) Type() Type      { return e.T }
func (e *LocalRef) Type() Type   { return e.T }
func (e *FieldRef) Type() Type   { return e.T }
func (e *IndexRef) Type() Type   { return e.T }
func (e *TableRef) Type() Type   { return e.T }
func (e *Zero) Type() Type       { return e.T }
func (e *Composite) Type() Type  { return e.T }
func (e *Call) Type() Type       { return e.T }
func (e *OpCall) Type() Type     { return e.T }
func (e *Unary) Type() Type      { return e.T }
func (e *Binary) Type() Type     { return e.T }
func (e *Convert) Type() Type    { return e.T }
func (e *Mask) Type() Type       { return e.T }
func (e *LoadStruct) Type() Type { return e.T }

// Op is an operator.
type Op int

const (
	Add Op = iota
	Sub
	Mul
	Div
	Rem
	BitAnd
	BitOr
	BitXor
	AndNot
	Shl
	Shr
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
	LogicalAnd
	LogicalOr
	Neg
	Not
	BitComplement
)

var opNames = [...]string{"+", "-", "*", "/", "%", "&", "|", "^", "&^", "<<", ">>", "==", "!=", "<", "<=", ">", ">=", "&&", "||", "neg", "!", "~"}

func (o Op) String() string { return opNames[o] }

// Operation is an operation of compute.Operations as the interface
// declares it, read from the interface's method: its name, whether
// its first parameter is the buffer it acts on, its value operands
// after that, and whether it has a result. The compile stage states
// nothing about an operation itself; an OpCall node names one, and
// Invoke calls it on a backend.
type Operation struct {
	Name     string
	Buffered bool
	Operands int
	Yields   bool
}

func (o *Operation) String() string { return o.Name }

// Pure reports an operation that reads and writes no buffer
// contents, so its evaluation order is not observable: one that acts
// on no buffer and yields a value (min and max), and Length, which
// takes a buffer but reads its length, not its contents. NextPhase
// acts on no buffer and yields nothing, and orders the buffer stores
// around it.
func (o *Operation) Pure() bool { return !o.Buffered && o.Yields || o == opLength }

// bufferOperand and valueOperand instantiate compute.Operations so a
// method's buffer parameter can be told from its value parameters.
type (
	bufferOperand struct{}
	valueOperand  struct{}
)

// operations is every operation of compute.Operations by name.
var operations = rendererOperations()

func rendererOperations() map[string]*Operation {
	it := reflect.TypeFor[compute.Operations[bufferOperand, valueOperand]]()
	buffer := reflect.TypeFor[bufferOperand]()
	ops := make(map[string]*Operation, it.NumMethod())
	for i := range it.NumMethod() {
		m := it.Method(i)
		op := &Operation{Name: m.Name, Operands: m.Type.NumIn(), Yields: m.Type.NumOut() == 1}
		if op.Operands > 0 && m.Type.In(0) == buffer {
			op.Buffered = true
			op.Operands--
		}
		ops[m.Name] = op
	}
	return ops
}

// The operations the compile stage lowers Go's own forms to, each
// named from compute.Operations at initialization: a word buffer
// index and store, len of a variable-length buffer, and the builtins
// min and max on the operand kinds the subset admits them for.
// TestLoweringOperations holds that the interface declares each.
var (
	opLoad   = operations["Load"]
	opStore  = operations["Store"]
	opLength = operations["Length"]
	opMinI32 = operations["MinI32"]
	opMaxI32 = operations["MaxI32"]
	opMinI64 = operations["MinI64"]
	opMaxI64 = operations["MaxI64"]
	opMinU32 = operations["MinU32"]
	opMaxU32 = operations["MaxU32"]
)

// builtins is the lowering of the Go builtins min and max by the
// operands' kind. A kind absent here is one the validator rejects.
var builtins = map[string]map[Kind]*Operation{
	"min": {I32: opMinI32, I64: opMinI64, U32: opMinU32},
	"max": {I32: opMaxI32, I64: opMaxI64, U32: opMaxU32},
}

// Invoke calls the renderer's method for op with the buffer, when
// the operation acts on one, and the value operands in order, and
// returns the result, or the zero value for an operation with none.
// An emitter implements compute.Operations over its own operand types
// and passes the operands it evaluated.
func Invoke[B, V any](r compute.Operations[B, V], op *Operation, buf B, args []V) V {
	in := make([]reflect.Value, 0, len(args)+1)
	if op.Buffered {
		in = append(in, reflect.ValueOf(buf))
	}
	for _, a := range args {
		in = append(in, reflect.ValueOf(a))
	}
	out := reflect.ValueOf(r).MethodByName(op.Name).Call(in)
	var v V
	if len(out) == 1 {
		v = out[0].Interface().(V)
	}
	return v
}
