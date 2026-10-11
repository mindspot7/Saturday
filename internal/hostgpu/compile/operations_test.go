// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package compile

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// recorder implements compute.Operations by recording the name of the
// operation called, the buffer, and the operands, and returning the
// name from an operation with a result.
type recorder struct {
	name string
	buf  int
	args []string
}

func (r *recorder) note(name string, buf int, args ...string) string {
	r.name, r.buf, r.args = name, buf, args
	return name
}

func (r *recorder) U8(buf int, a string) string      { return r.note("U8", buf, a) }
func (r *recorder) U16BE(buf int, a string) string   { return r.note("U16BE", buf, a) }
func (r *recorder) U32BE(buf int, a string) string   { return r.note("U32BE", buf, a) }
func (r *recorder) PutU8(buf int, a, v string)       { r.note("PutU8", buf, a, v) }
func (r *recorder) PutU16BE(buf int, a, v string)    { r.note("PutU16BE", buf, a, v) }
func (r *recorder) PutU32BE(buf int, a, v string)    { r.note("PutU32BE", buf, a, v) }
func (r *recorder) Load(buf int, i string) string    { return r.note("Load", buf, i) }
func (r *recorder) Store(buf int, i, v string)       { r.note("Store", buf, i, v) }
func (r *recorder) Length(buf int) string            { return r.note("Length", buf) }
func (r *recorder) Raise(buf int, i, v string)       { r.note("Raise", buf, i, v) }
func (r *recorder) Add(buf int, i, v string) string  { return r.note("Add", buf, i, v) }
func (r *recorder) Swap(buf int, i, v string) string { return r.note("Swap", buf, i, v) }
func (r *recorder) MinI32(x, y string) string        { return r.note("MinI32", 0, x, y) }
func (r *recorder) MaxI32(x, y string) string        { return r.note("MaxI32", 0, x, y) }
func (r *recorder) MinI64(x, y string) string        { return r.note("MinI64", 0, x, y) }
func (r *recorder) MaxI64(x, y string) string        { return r.note("MaxI64", 0, x, y) }
func (r *recorder) MinU32(x, y string) string        { return r.note("MinU32", 0, x, y) }
func (r *recorder) MaxU32(x, y string) string        { return r.note("MaxU32", 0, x, y) }
func (r *recorder) NextPhase()                       { r.note("NextPhase", 0) }

// TestInvokeReachesOperations holds Invoke to calling each operation's
// method with the buffer, when the operation acts on one, and its
// operands in order, and returning the result of an operation that
// has one. The operations and their shapes come from the interface,
// so a method whose signature changes fails here unless the recorder
// follows it.
func TestInvokeReachesOperations(t *testing.T) {
	r := &recorder{}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range operations {
			if !yield(n) {
				return
			}
		}
	}) {
		op := operations[name]
		buf := 0
		if op.Buffered {
			buf = 7
		}
		args := make([]string, op.Operands)
		for i := range args {
			args[i] = "v" + strconv.Itoa(i)
		}
		got := Invoke(r, op, buf, args)
		if r.name != name || r.buf != buf || !slices.Equal(r.args, args) {
			t.Errorf("Invoke(%s) called %s with buffer %d and operands %v, want %d and %v", name, r.name, r.buf, r.args, buf, args)
		}
		want := ""
		if op.Yields {
			want = name
		}
		if got != want {
			t.Errorf("Invoke(%s) = %q, want %q", name, got, want)
		}
	}
}

// TestOperationCallShape holds a call into the parent package to the
// operation as compute.Operations takes it: the name is an operation,
// the operands are the operation's, and the result is present exactly
// when the operation has one. The parent declares whatever the case
// needs, since the lowering holds the call, not the declaration.
func TestOperationCallShape(t *testing.T) {
	const buffer = "package compute\ntype B [4]byte\n"
	const call = "package sub\nimport \"example.com/compute\"\nfunc F(b *compute.B) uint32 {\n\t%s\n}\n"
	cases := []struct {
		name, parent, body, want string
	}{
		{"not an operation", buffer + "func (b *B) Twice(a uint32) uint32 { return a * 2 }\n",
			"return b.Twice(1)", "Twice is not an operation of compute.Operations"},
		{"operand count", buffer + "func (b *B) U8(a, extra uint32) uint8 { return 0 }\n",
			"return uint32(b.U8(1, 2))", "call of U8 has 2 operands, the operation takes 1"},
		{"result on a statement", buffer + "func (b *B) PutU8(a uint32, v uint8) uint8 { return v }\n",
			"return uint32(b.PutU8(1, 2))", "call of PutU8 has a result, the operation has none"},
		{"missing result", buffer + "func (b *B) U8(a uint32) {}\n",
			"b.U8(1)\n\treturn 0", "call of U8 has no result, the operation has one"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := strings.Replace(call, "%s", c.body, 1)
			prog, err := Load(
				Source{Path: testParentPath, Files: fstest.MapFS{"p.go": {Data: []byte(c.parent)}}},
				Source{Path: testParentPath + "/sub", Files: fstest.MapFS{"sub.go": {Data: []byte(sub)}}},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Lower(prog)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want substring %q", err, c.want)
			}
		})
	}
}

// TestLoweringOperations holds that compute.Operations declares every
// operation the lowering names at initialization.
func TestLoweringOperations(t *testing.T) {
	ops := map[string]*Operation{
		"Load": opLoad, "Store": opStore, "Length": opLength,
		"MinI32": opMinI32, "MaxI32": opMaxI32, "MinI64": opMinI64,
		"MaxI64": opMaxI64, "MinU32": opMinU32, "MaxU32": opMaxU32,
	}
	for name, op := range ops {
		if op == nil {
			t.Errorf("compute.Operations has no operation %s", name)
		}
	}
}
